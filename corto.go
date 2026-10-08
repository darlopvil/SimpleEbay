package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Enlaces cortos del botón «Compartir» de eBay: ebay.io/m/{código}, tanto en
// la app como en la web. El primer salto ya es un 301 a la ficha
// (www.ebay.es/itm/{id}?…), así que basta con pedir ese enlace sin seguir la
// redirección ni descargar nada. Solo funciona con GET: con HEAD, ebay.io
// entra en un bucle de páginas de error. Un código que no existe da 404.
//
// La ruta /m/{código} es la misma que en ebay.io, para que la regla de
// Redirector solo tenga que cambiar el dominio. Los parámetros de rastreo del
// destino (ssuid, sssrc…, que identifican a quien compartió) se descartan.
const urlEnlaceCorto = "https://ebay.io/m/"

var (
	patronCodigoCorto = regexp.MustCompile(`^[A-Za-z0-9]{4,32}$`)

	// Solo se aceptan destinos de eBay: así la ruta no puede usarse para
	// redirigir a ninguna otra parte.
	patronHostEbay = regexp.MustCompile(`^(?:www\.)?ebay\.(?:[a-z]{2,3}|co\.uk|com\.[a-z]{2})$`)
	patronRutaItm  = regexp.MustCompile(`^/itm/(?:[^/]+/)?([0-9]{9,15})$`)

	errCortoInexistente = errors.New("el enlace corto no existe o ha caducado")

	clienteCorto = &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	cacheCortos = nuevaCache[string]()
)

// resolverCorto devuelve la ruta de la ficha propia a la que apunta un
// enlace corto.
func resolverCorto(ctx context.Context, codigo string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlEnlaceCorto+codigo, nil)
	if err != nil {
		return "", err
	}
	resp, err := clienteCorto.Do(req)
	if err != nil {
		return "", fmt.Errorf("petición a ebay.io: %w", err)
	}
	// El cuerpo no interesa: solo la cabecera Location.
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		return "", errCortoInexistente
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		return "", fmt.Errorf("ebay.io respondió %d", resp.StatusCode)
	}

	destino, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || destino.Scheme != "https" || !patronHostEbay.MatchString(destino.Hostname()) {
		return "", fmt.Errorf("el enlace no lleva a eBay: %q", resp.Header.Get("Location"))
	}
	m := patronRutaItm.FindStringSubmatch(destino.Path)
	if m == nil {
		return "", fmt.Errorf("el enlace no lleva a un anuncio: %q", destino.Path)
	}
	ruta := "/itm/" + m[1]
	if v := destino.Query().Get("var"); soloDigitos(v) && v != "0" {
		ruta += "?var=" + v
	}
	return ruta, nil
}

func manejadorCorto(w http.ResponseWriter, r *http.Request) {
	codigo := strings.TrimPrefix(r.URL.Path, "/m/")
	if !patronCodigoCorto.MatchString(codigo) {
		renderizar(w, http.StatusNotFound, "error", datosPagina{
			Titulo:  "Enlace no válido",
			Mensaje: "Esto no parece un enlace corto de eBay (ebay.io/m/…).",
		})
		return
	}
	ruta, ok := cacheCortos.obtener(codigo)
	if !ok {
		var err error
		ruta, err = resolverCorto(r.Context(), codigo)
		if err != nil {
			log.Printf("enlace corto %s: %v", codigo, err)
			estado, mensaje := http.StatusBadGateway, "No se ha podido averiguar a qué anuncio lleva este enlace."
			if errors.Is(err, errCortoInexistente) {
				estado, mensaje = http.StatusNotFound, "eBay no reconoce este enlace corto: no existe o ha caducado."
			}
			renderizar(w, estado, "error", datosPagina{Titulo: "Enlace corto", Mensaje: mensaje, Detalle: err.Error()})
			return
		}
		cacheCortos.guardar(codigo, ruta)
	}
	http.Redirect(w, r, ruta, http.StatusFound)
}
