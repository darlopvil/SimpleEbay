package main

import (
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Búsqueda por foto con search_by_image, que la API marca como experimental.
// Comprobado con el keyset: solo responde en EBAY_DE, EBAY_GB, EBAY_US y
// EBAY_AU (en los demás, error 12019). Ordena por parecido y es bueno: con la
// carátula de un juego, los primeros resultados son ese juego. El total que
// devuelve no significa nada (millones: puntúa todo el catálogo) y ordenar
// por precio deja el parecido de lado, así que se muestra solo la primera
// tanda, por parecido y sin filtros.
//
// La foto se lee en memoria, se manda a eBay y se descarta: no se escribe en
// disco ni se registra.
const (
	// Una foto de móvil normal cabe. En base64 crece un tercio, y con los
	// 64 MB del contenedor no conviene ir mucho más allá.
	maxFoto = 5 << 20

	resultadosFoto = 60
)

var marketplacesFoto = []string{"EBAY_DE", "EBAY_GB", "EBAY_US", "EBAY_AU"}

// Tipos de imagen que se aceptan, según los primeros bytes del fichero y no
// según lo que diga el navegador.
var tiposFoto = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

var errFotoGrande = errors.New("la foto pasa de 5 MB")

// Fotos que se procesan a la vez, entre todos los visitantes. Cada una ocupa
// hasta unos 12 MB mientras se lee y se codifica; con este tope la memoria
// queda acotada aunque lleguen muchas a la vez.
var turnosFoto = make(chan struct{}, 2)

// mismoOrigen comprueba que el formulario se ha enviado desde esta misma web,
// para que otra página no pueda usar la instancia como buscador por foto. Los
// navegadores actuales lo dicen en Sec-Fetch-Site. Con Origin no basta: con la
// política no-referrer, los navegadores lo mandan como «null» en los POST.
func mismoOrigen(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
		// Navegador sin Sec-Fetch-Site: se mira Origin.
	default:
		return false
	}
	esquema := "http"
	if esHTTPS(r) {
		esquema = "https"
	}
	return r.Header.Get("Origin") == esquema+"://"+r.Host
}

type resultadoFoto struct {
	Marketplace marketplace
	Tarjetas    []tarjeta
}

func opcionesFoto() []marketplace {
	var lista []marketplace
	for _, id := range marketplacesFoto {
		if m, ok := buscarMarketplace(id); ok {
			lista = append(lista, m)
		}
	}
	return lista
}

// leerFormularioFoto recorre el multipart a mano en lugar de usar
// ParseMultipartForm, que vuelca a disco lo que no cabe en memoria: la
// imagen es scratch y no tiene /tmp, y además no se quiere que la foto toque
// el disco.
func leerFormularioFoto(r *http.Request) (foto []byte, marketplace string, err error) {
	partes, err := r.MultipartReader()
	if err != nil {
		return nil, "", err
	}
	for {
		parte, err := partes.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", err
		}
		switch parte.FormName() {
		case "foto":
			foto, err = io.ReadAll(io.LimitReader(parte, maxFoto+1))
			if err != nil {
				return nil, "", err
			}
			if len(foto) > maxFoto {
				return nil, "", errFotoGrande
			}
		case "mp":
			v, err := io.ReadAll(io.LimitReader(parte, 32))
			if err != nil {
				return nil, "", err
			}
			marketplace = string(v)
		}
		parte.Close()
	}
	return foto, marketplace, nil
}

func manejadorFoto(ebay *clienteEbay) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		fallo := func(estado int, mensaje, detalle string) {
			renderizar(w, estado, "error", datosPagina{Titulo: "Búsqueda por foto", Mensaje: mensaje, Detalle: detalle})
		}
		if !mismoOrigen(r) {
			fallo(http.StatusForbidden, "La foto tiene que enviarse desde el formulario de esta web.", "")
			return
		}
		select {
		case turnosFoto <- struct{}{}:
			defer func() { <-turnosFoto }()
		default:
			w.Header().Set("Retry-After", "5")
			fallo(http.StatusServiceUnavailable, "Ahora mismo se están procesando otras fotos. Inténtalo en unos segundos.", "")
			return
		}

		// Subir unos megas desde el móvil puede tardar más que el límite
		// general de lectura del servidor.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
		r.Body = http.MaxBytesReader(w, r.Body, maxFoto+(64<<10))

		foto, id, err := leerFormularioFoto(r)
		var demasiado *http.MaxBytesError
		switch {
		case errors.Is(err, errFotoGrande) || errors.As(err, &demasiado):
			fallo(http.StatusRequestEntityTooLarge, "La foto pasa de 5 MB. Prueba con una más pequeña o una captura.", "")
			return
		case err != nil:
			fallo(http.StatusBadRequest, "No se ha podido leer la foto enviada.", err.Error())
			return
		case len(foto) == 0:
			fallo(http.StatusBadRequest, "No se ha enviado ninguna foto.", "")
			return
		}
		if tipo := http.DetectContentType(foto); !tiposFoto[tipo] {
			fallo(http.StatusUnsupportedMediaType, "Solo se admiten fotos JPEG, PNG o WebP.", "Tipo detectado: "+tipo)
			return
		}
		m, ok := buscarMarketplace(id)
		if !ok || !contiene(marketplacesFoto, m.ID) {
			m, _ = buscarMarketplace(marketplacesFoto[0])
		}

		// El JSON se monta a mano para no tener a la vez la foto, su base64 y
		// una copia serializada: el base64 no lleva caracteres que escapar.
		cuerpo := make([]byte, 0, base64.StdEncoding.EncodedLen(len(foto))+16)
		cuerpo = append(cuerpo, `{"image":"`...)
		cuerpo = base64.StdEncoding.AppendEncode(cuerpo, foto)
		cuerpo = append(cuerpo, `"}`...)
		foto = nil

		var resp respuestaBusquedaAPI
		consulta := url.Values{"limit": {strconv.Itoa(resultadosFoto)}}
		if err := ebay.peticionJSON(r.Context(), m.ID, "/buy/browse/v1/item_summary/search_by_image", consulta, cuerpo, &resp); err != nil {
			log.Printf("búsqueda por foto en %s: %v", m.ID, err)
			mensaje, detalle := mensajeError(err)
			fallo(http.StatusBadGateway, mensaje, detalle)
			return
		}

		res := &resultadoFoto{Marketplace: m}
		vistos := map[string]bool{}
		for _, r := range resp.Resumenes {
			if vistos[r.ItemID] || len(res.Tarjetas) == resultadosFoto {
				continue
			}
			vistos[r.ItemID] = true
			res.Tarjetas = append(res.Tarjetas, nuevaTarjeta(r))
		}
		renderizar(w, http.StatusOK, "foto", datosPagina{Titulo: "Búsqueda por foto", Foto: res})
	}
}
