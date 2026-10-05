package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	// Base de zonas horarias embebida: la imagen es scratch y no trae
	// /usr/share/zoneinfo, pero así la variable TZ funciona igual.
	_ "time/tzdata"
)

var (
	//go:embed templates
	plantillasFS embed.FS

	//go:embed static
	estaticosFS embed.FS
)

// entorno devuelve el valor de una variable de entorno, o el valor por
// defecto si no está definida o está vacía.
func entorno(nombre, porDefecto string) string {
	if v := os.Getenv(nombre); v != "" {
		return v
	}
	return porDefecto
}

// cabecerasSeguridad envuelve el multiplexor. La interfaz no usa JavaScript
// ni recursos de terceros, así que la política de contenido puede ser
// estricta: todo sale de este mismo servidor, incluidas las imágenes, que
// pasarán por el proxy de medios.
func cabecerasSeguridad(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		siguiente.ServeHTTP(w, r)
	})
}

// probar vuelca la respuesta en crudo de una ruta de la API. Antes de mapear
// cualquier campo nuevo hay que ver qué devuelve eBay de verdad, y así se
// hace con las mismas credenciales y cabeceras que usa el servicio.
func probar(ebay *clienteEbay, marketplace, ruta string) int {
	camino, query, _ := strings.Cut(ruta, "?")
	consulta, err := url.ParseQuery(query)
	if err != nil {
		fmt.Fprintln(os.Stderr, "query inválida:", err)
		return 2
	}
	ctx, cancelar := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelar()

	var crudo json.RawMessage
	if err := ebay.peticion(ctx, marketplace, camino, consulta, &crudo); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var bonito bytes.Buffer
	if err := json.Indent(&bonito, crudo, "", "  "); err != nil {
		os.Stdout.Write(crudo)
		return 0
	}
	bonito.WriteTo(os.Stdout)
	fmt.Println()
	return 0
}

func main() {
	// El host por defecto es 0.0.0.0 y no localhost: dentro de un contenedor,
	// localhost deja el proceso inalcanzable aunque el puerto esté publicado.
	puertoPorDefecto, err := strconv.Atoi(entorno("SIMPLEEBAY_PORT", "8080"))
	if err != nil {
		log.Fatalf("SIMPLEEBAY_PORT no es un número válido: %v", err)
	}
	puerto := flag.Int("p", puertoPorDefecto, "Puerto de escucha")
	host := flag.String("h", entorno("SIMPLEEBAY_HOST", "0.0.0.0"), "Dirección de escucha")
	sonda := flag.String("probe", "", "Hace un GET a esta ruta de la API (con su query), imprime el JSON en crudo y termina")
	marketplace := flag.String("mp", "EBAY_ES", "Marketplace para -probe")
	flag.Parse()

	ebay, err := nuevoClienteEbay(
		os.Getenv("EBAY_CLIENT_ID"),
		os.Getenv("EBAY_CLIENT_SECRET"),
		os.Getenv("SIMPLEEBAY_ENVIO_PAIS"),
		os.Getenv("SIMPLEEBAY_ENVIO_CP"),
	)
	if err != nil {
		log.Fatal(err)
	}

	if *sonda != "" {
		os.Exit(probar(ebay, *marketplace, *sonda))
	}

	// Se pide el token al arrancar para que un keyset mal copiado o sin
	// activar salga en los logs desde el primer momento, y no con la primera
	// búsqueda. Un fallo aquí no detiene el servicio: puede ser un corte de
	// red pasajero, y el token se volverá a pedir en la siguiente petición.
	// En segundo plano, para que el servidor escuche desde el primer momento
	// aunque eBay tarde en responder.
	go func() {
		ctx, cancelar := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancelar()
		if caduca, err := ebay.comprobarToken(ctx); err != nil {
			log.Printf("eBay: no se pudo obtener el token de aplicación: %v", err)
		} else {
			log.Printf("eBay: token de aplicación obtenido, válido hasta %s", caduca.Format(time.RFC3339))
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			renderizar(w, http.StatusNotFound, "error", datosPagina{
				Titulo:  "Página no encontrada",
				Mensaje: "Esta dirección no existe.",
			})
			return
		}
		renderizar(w, http.StatusOK, "inicio", datosPagina{Inicio: true})
	})

	// Los navegadores piden /favicon.ico sin mirar el <head>.
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/favicon.svg", http.StatusMovedPermanently)
	})

	// Cada página indexada por un buscador se traduciría en llamadas a la API
	// que gastan el cupo diario del keyset.
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
	})

	mux.HandleFunc("/s", manejadorBusqueda(ebay))
	mux.HandleFunc("/itm/", manejadorFicha(ebay))
	mux.HandleFunc("/img/", proxyImagen)
	mux.Handle("/static/", http.FileServer(http.FS(estaticosFS)))

	srv := &http.Server{
		Addr:              *host + ":" + strconv.Itoa(*puerto),
		Handler:           cabecerasSeguridad(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("Escuchando en %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
