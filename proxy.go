package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Las imágenes nunca se cargan desde el navegador: todas pasan por este
// servidor, para que eBay y los demás sitios no vean la IP del usuario. Hay
// dos proxies. /img/ reenvía solo fotos de i.ebayimg.com, que es donde viven
// las de los anuncios. /ext reenvía las imágenes que los vendedores enlazan
// en sus descripciones desde cualquier otro servidor, con dos protecciones
// para que no se convierta en un proxy abierto: solo acepta direcciones
// firmadas por este mismo servidor al sanear una descripción, y nunca se
// conecta a direcciones de redes privadas.

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
	reenviarImagen(w, r, clienteImagenes, destino)
}

// Clave con la que se firman las direcciones de /ext. Se genera al arrancar:
// tras un reinicio, las direcciones firmadas antes dejan de valer, pero las
// páginas se vuelven a generar con firmas nuevas en cuanto se recargan.
var claveFirma = func() []byte {
	clave := make([]byte, 32)
	if _, err := rand.Read(clave); err != nil {
		panic("no se pudo generar la clave de firma: " + err.Error())
	}
	return clave
}()

func firmar(direccion string) string {
	m := hmac.New(sha256.New, claveFirma)
	m.Write([]byte(direccion))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// direccionExternaValida acepta solo http y https en sus puertos estándar.
func direccionExternaValida(u *url.URL) bool {
	if u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return false
	}
	puerto := u.Port()
	return puerto == "" || puerto == "80" || puerto == "443"
}

// rutaImagenExterna devuelve la ruta firmada de /ext para una imagen de otro
// servidor, o "" si la dirección no es aceptable.
func rutaImagenExterna(src string) string {
	u, err := url.Parse(src)
	if err != nil || !direccionExternaValida(u) {
		return ""
	}
	direccion := u.String()
	return "/ext?u=" + url.QueryEscape(direccion) + "&f=" + firmar(direccion)
}

var errDireccionPrivada = errors.New("dirección de red no pública")

// esPublica descarta las direcciones de la red local, del propio servidor y
// de los contenedores. Sin esto, una descripción con una imagen apuntando a
// 192.168.x.x o a otro contenedor haría que el servidor les pidiera datos.
func esPublica(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// Espacio compartido de los operadores (CGNAT), 100.64.0.0/10.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1]&0xc0 == 64 {
		return false
	}
	return true
}

// La comprobación se hace al abrir cada conexión, con la IP ya resuelta, y
// no al validar el nombre: así tampoco cuela un nombre que resuelva a una IP
// privada ni una redirección hacia una.
var clienteExterno = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, direccion string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(direccion)
				if err != nil {
					return err
				}
				if ip := net.ParseIP(host); ip == nil || !esPublica(ip) {
					return errDireccionPrivada
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !direccionExternaValida(req.URL) {
			return errors.New("redirección no permitida")
		}
		return nil
	},
}

func proxyExterno(w http.ResponseWriter, r *http.Request) {
	direccion := r.URL.Query().Get("u")
	firma := r.URL.Query().Get("f")
	if direccion == "" || !hmac.Equal([]byte(firma), []byte(firmar(direccion))) {
		http.Error(w, "Dirección no firmada", http.StatusForbidden)
		return
	}
	u, err := url.Parse(direccion)
	if err != nil || !direccionExternaValida(u) {
		http.Error(w, "Dirección no válida", http.StatusBadRequest)
		return
	}
	reenviarImagen(w, r, clienteExterno, u.String())
}

// reenviarImagen descarga una imagen y la devuelve al navegador.
func reenviarImagen(w http.ResponseWriter, r *http.Request, cliente *http.Client, destino string) {
	ctx, cancelar := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancelar()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, destino, nil)
	if err != nil {
		http.Error(w, "Ruta de imagen no válida", http.StatusBadRequest)
		return
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/*")

	resp, err := cliente.Do(req)
	if err != nil {
		if errors.Is(err, errDireccionPrivada) {
			http.Error(w, "Dirección no permitida", http.StatusForbidden)
			return
		}
		http.Error(w, "No se pudo obtener la imagen", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "La imagen no está disponible", http.StatusBadGateway)
		return
	}
	// Solo se reenvían imágenes: cualquier otra cosa podría interpretarse en
	// el navegador con un tipo que no controlamos. SVG se rechaza porque
	// puede llevar scripts.
	tipo := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(tipo, "image/") || strings.HasPrefix(tipo, "image/svg") {
		http.Error(w, "El recurso no es una imagen", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", tipo)
	// Cada dirección identifica una foto concreta, así que el navegador
	// puede guardarla una semana.
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	if resp.ContentLength > 0 && resp.ContentLength <= maxImagen {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if _, err := io.Copy(w, io.LimitReader(resp.Body, maxImagen)); err != nil {
		// La cabecera ya salió: solo cabe dejar constancia.
		log.Printf("error al reenviar la imagen %s: %v", destino, err)
	}
}
