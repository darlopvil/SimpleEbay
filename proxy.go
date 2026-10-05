package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Único origen de imágenes que se reenvía. Las fotos de los anuncios y las
// de sus galerías viven siempre aquí; lo que los vendedores enlazan desde
// otros servidores no pasa por el proxy, para que este no sirva de puerta
// abierta a cualquier sitio.
const hostImagenes = "i.ebayimg.com"

// Las fotos de eBay pueden pesar más de un mega en su tamaño máximo. El
// límite cubre eso con margen y acota lo que una sola petición transfiere.
const maxImagen = 10 << 20

var clienteImagenes = &http.Client{Timeout: 20 * time.Second}

// El sufijo s-lNNN de las URL de eBay elige el tamaño de la foto.
var patronTamano = regexp.MustCompile(`/s-l\d+\.`)

// rutaImagen convierte una URL de i.ebayimg.com en su ruta a través del
// proxy, con el tamaño pedido si se indica. Devuelve "" para cualquier otro
// origen, de modo que la plantilla puede omitir la imagen.
func rutaImagen(src string, tamano int) string {
	u, err := url.Parse(src)
	if err != nil || u.Host != hostImagenes || !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	ruta := u.EscapedPath()
	if tamano > 0 {
		ruta = patronTamano.ReplaceAllString(ruta, "/s-l"+strconv.Itoa(tamano)+".")
	}
	if u.RawQuery != "" {
		ruta += "?" + u.RawQuery
	}
	return "/img" + ruta
}

func proxyImagen(w http.ResponseWriter, r *http.Request) {
	ruta := strings.TrimPrefix(r.URL.EscapedPath(), "/img")
	if !strings.HasPrefix(ruta, "/") || strings.Contains(ruta, "..") {
		http.Error(w, "Ruta de imagen no válida", http.StatusBadRequest)
		return
	}
	destino := "https://" + hostImagenes + ruta
	if r.URL.RawQuery != "" {
		destino += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, destino, nil)
	if err != nil {
		http.Error(w, "Ruta de imagen no válida", http.StatusBadRequest)
		return
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/*")

	resp, err := clienteImagenes.Do(req)
	if err != nil {
		http.Error(w, "No se pudo obtener la imagen", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "La imagen no está disponible", http.StatusBadGateway)
		return
	}
	// Solo se reenvían imágenes: cualquier otra cosa podría interpretarse en
	// el navegador con un tipo que no controlamos.
	tipo := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(tipo, "image/") {
		http.Error(w, "El recurso no es una imagen", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", tipo)
	// La URL de cada foto incluye su identificador, así que su contenido no
	// cambia nunca y el navegador puede guardarla una semana.
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	if resp.ContentLength > 0 && resp.ContentLength <= maxImagen {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if _, err := io.Copy(w, io.LimitReader(resp.Body, maxImagen)); err != nil {
		// La cabecera ya salió: solo cabe dejar constancia.
		fmt.Println("error al reenviar la imagen:", err)
	}
}
