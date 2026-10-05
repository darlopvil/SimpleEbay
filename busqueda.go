package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// 60 llena filas completas en rejillas de 2, 3, 4, 5 y 6 columnas.
	porPagina = 60

	// A eBay se le piden bloques de tres páginas en una sola llamada. La
	// paginación por offset de la API no es estable entre llamadas: un mismo
	// artículo puede salir al final de una página y al principio de la
	// siguiente. Dentro de un bloque se eliminan esas repeticiones, y de paso
	// pasar de página no gasta cupo hasta cambiar de bloque.
	paginasPorBloque = 3
	porBloque        = porPagina * paginasPorBloque

	// La API no devuelve más allá del resultado 10.000 de una búsqueda.
	maxResultados = 10000
	maxPaginas    = maxResultados / porPagina

	// eBay rechaza consultas muy largas; se recorta antes de enviarla.
	maxConsulta = 200

	cookieMarketplace = "mp"
)

type opcion struct {
	Valor    string
	Etiqueta string
}

var (
	// eBay ordena por precio con el envío incluido, igual que se aplica el
	// rango de precio.
	opcionesOrden = []opcion{
		{"", "Relevancia"},
		{"price", "Más barato"},
		{"-price", "Más caro"},
		{"newlyListed", "Más recientes"},
		{"endingSoonest", "Terminan antes"},
	}
	opcionesEstado = []opcion{
		{"", "Cualquiera"},
		{"NEW", "Nuevo"},
		{"USED", "Usado"},
	}
	opcionesCompra = []opcion{
		{"", "Todos"},
		{"FIXED_PRICE", "¡Cómpralo ya!"},
		{"AUCTION", "Subastas"},
	}
	// "UE" es la región de la Unión Europea; el resto son países. eBay
	// filtra por la ubicación del artículo, que suele ser la del vendedor.
	opcionesUbicacion = []opcion{
		{"", "Cualquiera"},
		{"UE", "Unión Europea"},
		{"ES", "España"},
		{"PT", "Portugal"},
		{"FR", "Francia"},
		{"IT", "Italia"},
		{"DE", "Alemania"},
		{"NL", "Países Bajos"},
		{"BE", "Bélgica"},
		{"AT", "Austria"},
		{"IE", "Irlanda"},
		{"PL", "Polonia"},
		{"GB", "Reino Unido"},
		{"CH", "Suiza"},
		{"US", "Estados Unidos"},
		{"CA", "Canadá"},
		{"JP", "Japón"},
		{"CN", "China"},
		{"HK", "Hong Kong"},
		{"AU", "Australia"},
	}
)

func opcionValida(lista []opcion, valor string) bool {
	for _, o := range lista {
		if o.Valor == valor {
			return true
		}
	}
	return false
}

// filtrosBusqueda es lo que el usuario ha pedido, ya validado. Todo valor que
// no se reconoce se descarta en lugar de pasarlo a la API.
type filtrosBusqueda struct {
	Consulta    string
	Marketplace marketplace
	Orden       string
	Estado      string
	Compra      string
	Desde       string
	// Rango de precio en euros, envío incluido.
	Min    string
	Max    string
	Pagina int
}

// normalizarPrecio acepta "12,5" o "12.5" y devuelve "12.5". Cualquier cosa
// que no sea un importe positivo se ignora.
func normalizarPrecio(s string) string {
	s = strings.TrimSpace(strings.Replace(s, ",", ".", 1))
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 || n > 1e9 {
		return ""
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// esHTTPS indica si la petición llegó cifrada al proxy inverso, para marcar
// la cookie como Secure solo cuando tiene sentido.
func esHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// marketplaceElegido toma el marketplace del parámetro mp si es válido, y en
// ese caso lo recuerda en una cookie. Sin parámetro, usa la cookie.
func marketplaceElegido(w http.ResponseWriter, r *http.Request) marketplace {
	if id := r.URL.Query().Get("mp"); id != "" {
		if m, ok := buscarMarketplace(id); ok {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieMarketplace,
				Value:    m.ID,
				Path:     "/",
				MaxAge:   365 * 24 * 3600,
				HttpOnly: true,
				Secure:   esHTTPS(r),
				SameSite: http.SameSiteLaxMode,
			})
			return m
		}
	}
	if c, err := r.Cookie(cookieMarketplace); err == nil {
		if m, ok := buscarMarketplace(c.Value); ok {
			return m
		}
	}
	m, _ := buscarMarketplace(marketplacePorDefecto)
	return m
}

func leerFiltros(w http.ResponseWriter, r *http.Request) filtrosBusqueda {
	q := r.URL.Query()
	f := filtrosBusqueda{
		Consulta:    strings.TrimSpace(q.Get("q")),
		Marketplace: marketplaceElegido(w, r),
		Min:         normalizarPrecio(q.Get("min")),
		Max:         normalizarPrecio(q.Get("max")),
		Pagina:      1,
	}
	if utf8.RuneCountInString(f.Consulta) > maxConsulta {
		f.Consulta = string([]rune(f.Consulta)[:maxConsulta])
	}
	if v := q.Get("orden"); opcionValida(opcionesOrden, v) {
		f.Orden = v
	}
	if v := q.Get("estado"); opcionValida(opcionesEstado, v) {
		f.Estado = v
	}
	if v := q.Get("compra"); opcionValida(opcionesCompra, v) {
		f.Compra = v
	}
	if v := q.Get("desde"); opcionValida(opcionesUbicacion, v) {
		f.Desde = v
	}
	if n, err := strconv.Atoi(q.Get("pagina")); err == nil && n > 1 {
		f.Pagina = min(n, maxPaginas)
	}
	return f
}

// enlace devuelve la URL de esta misma búsqueda en otra página. Se incluye
// el marketplace para que los enlaces no dependan de la cookie.
func (f filtrosBusqueda) enlace(pagina int) string {
	v := url.Values{}
	v.Set("q", f.Consulta)
	v.Set("mp", f.Marketplace.ID)
	for clave, valor := range map[string]string{
		"orden": f.Orden, "estado": f.Estado, "compra": f.Compra,
		"desde": f.Desde, "min": f.Min, "max": f.Max,
	} {
		if valor != "" {
			v.Set(clave, valor)
		}
	}
	if pagina > 1 {
		v.Set("pagina", strconv.Itoa(pagina))
	}
	return "/s?" + v.Encode()
}

// bloque devuelve el número de bloque (desde 0) al que pertenece la página.
func (f filtrosBusqueda) bloque() int {
	return (f.Pagina - 1) / paginasPorBloque
}

// consultaAPI traduce los filtros a los parámetros de item_summary/search
// para el bloque de la página pedida. El último bloque se recorta para no
// pasar del resultado 10.000, que la API rechaza.
func (f filtrosBusqueda) consultaAPI() url.Values {
	offset := f.bloque() * porBloque
	v := url.Values{}
	v.Set("q", f.Consulta)
	v.Set("limit", strconv.Itoa(min(porBloque, maxResultados-offset)))
	v.Set("offset", strconv.Itoa(offset))
	if f.Orden != "" {
		v.Set("sort", f.Orden)
	}

	var filtro []string
	if f.Estado != "" {
		filtro = append(filtro, "conditions:{"+f.Estado+"}")
	}
	if f.Compra != "" {
		filtro = append(filtro, "buyingOptions:{"+f.Compra+"}")
	}
	switch f.Desde {
	case "":
	case "UE":
		filtro = append(filtro, "itemLocationRegion:EUROPEAN_UNION")
	default:
		filtro = append(filtro, "itemLocationCountry:"+f.Desde)
	}
	// El filtro de eBay se aplica al precio sin envío, y el rango del usuario
	// es con envío incluido: sirve de primera criba, y fueraDeRango termina
	// el trabajo. Con el máximo no se pierde nada, porque el precio nunca
	// supera al total. Con el mínimo se pierde algún artículo barato con un
	// envío caro que lo haría entrar; es un caso raro y a cambio la criba
	// sigue siendo útil. eBay acepta el rango en euros en cualquier
	// marketplace y lo convierte él. Solo con el máximo hacen falta los dos
	// puntos delante; solo con el mínimo, ninguno.
	if f.Min != "" || f.Max != "" {
		rango := f.Min
		if f.Max != "" {
			rango += ".." + f.Max
		}
		filtro = append(filtro, "price:["+rango+"]", "priceCurrency:"+monedaMostrada)
	}
	if len(filtro) > 0 {
		v.Set("filter", strings.Join(filtro, ","))
	}
	return v
}

// Estructuras de la respuesta de la API, solo con los campos que se usan.

type imagenAPI struct {
	URL string `json:"imageUrl"`
}

type vendedorAPI struct {
	Usuario    string `json:"username"`
	Porcentaje string `json:"feedbackPercentage"`
	Puntos     int    `json:"feedbackScore"`
}

type resumenAPI struct {
	ItemID     string      `json:"itemId"`
	LegacyID   string      `json:"legacyItemId"`
	Titulo     string      `json:"title"`
	Imagen     *imagenAPI  `json:"image"`
	Precio     *importeAPI `json:"price"`
	PujaActual *importeAPI `json:"currentBidPrice"`
	Pujas      int         `json:"bidCount"`
	Fin        string      `json:"itemEndDate"`
	Formatos   []string    `json:"buyingOptions"`
	Estado     string      `json:"condition"`
	EstadoID   string      `json:"conditionId"`
	Marketing  *struct {
		Original  *importeAPI `json:"originalPrice"`
		Descuento string      `json:"discountPercentage"`
	} `json:"marketingPrice"`
	Envios []struct {
		Coste *importeAPI `json:"shippingCost"`
	} `json:"shippingOptions"`
	Ubicacion *struct {
		Pais string `json:"country"`
	} `json:"itemLocation"`
	Vendedor *vendedorAPI `json:"seller"`
}

type respuestaBusquedaAPI struct {
	Total     int          `json:"total"`
	Resumenes []resumenAPI `json:"itemSummaries"`
}

// tarjeta es un resultado listo para pintar: todo el formato se resuelve
// aquí y la plantilla solo coloca textos.
type tarjeta struct {
	Enlace       string
	Titulo       string
	Imagen       string
	Precio       string
	PrecioOrigen string
	Tachado      string
	Descuento    string
	Subasta      bool
	Pujas        int
	Cierre       string
	Restante     string
	Ofertas      bool
	Envio        string
	EnvioGratis  bool
	Estado       string
	Pais         string
	Vendedor     string
	Valoracion   string
}

func contiene(lista []string, valor string) bool {
	for _, v := range lista {
		if v == valor {
			return true
		}
	}
	return false
}

// enlaceFicha apunta a la ficha propia del artículo. El ID es el mismo que
// usa eBay en sus URL, de modo que /itm/{id} se corresponde con la de eBay.
// Las variaciones llevan su identificador en ?var=, igual que allí.
func enlaceFicha(legacyID, itemID string) string {
	enlace := "/itm/" + url.PathEscape(legacyID)
	partes := strings.Split(itemID, "|")
	if len(partes) == 3 && partes[2] != "0" && partes[2] != "" {
		enlace += "?var=" + url.QueryEscape(partes[2])
	}
	return enlace
}

// totalEuros es lo que el usuario pagaría en euros: el precio que ve en la
// tarjeta (la puja actual en las subastas) más el primer envío, si se sabe.
func totalEuros(r resumenAPI) (float64, bool) {
	importe := r.Precio
	if contiene(r.Formatos, "AUCTION") && r.PujaActual != nil {
		importe = r.PujaActual
	}
	total, _, ok := importe.euros()
	if !ok {
		return 0, false
	}
	if len(r.Envios) > 0 {
		if envio, _, ok := r.Envios[0].Coste.euros(); ok {
			total += envio
		}
	}
	return total, true
}

// fueraDeRango aplica el filtro de precio al total con envío, que eBay no
// sabe hacer. Además corrige un fallo suyo: con subastas en otra moneda deja
// pasar pujas por debajo del mínimo (en EBAY_ES, con un rango de 1 a 10 €,
// cuelan pujas de 0,99 US$, que son 0,88 €).
func (f filtrosBusqueda) fueraDeRango(r resumenAPI) bool {
	if f.Min == "" && f.Max == "" {
		return false
	}
	precio, ok := totalEuros(r)
	if !ok {
		return false
	}
	if minimo, err := strconv.ParseFloat(f.Min, 64); err == nil && precio < minimo {
		return true
	}
	if maximo, err := strconv.ParseFloat(f.Max, 64); err == nil && precio > maximo {
		return true
	}
	return false
}

func nuevaTarjeta(r resumenAPI) tarjeta {
	t := tarjeta{
		Enlace:  enlaceFicha(r.LegacyID, r.ItemID),
		Titulo:  r.Titulo,
		Subasta: contiene(r.Formatos, "AUCTION"),
		Ofertas: contiene(r.Formatos, "BEST_OFFER"),
		Estado:  nombreEstado(r.Estado, r.EstadoID),
	}
	if r.Imagen != nil {
		t.Imagen = rutaImagen(r.Imagen.URL, 500)
	}

	precio := r.Precio
	if t.Subasta && r.PujaActual != nil {
		precio = r.PujaActual
		t.Pujas = r.Pujas
		t.Cierre = formatoFechaHora(r.Fin)
		t.Restante = tiempoRestante(r.Fin)
	}
	t.Precio = precio.texto()
	t.PrecioOrigen = precio.textoOrigen()

	if r.Marketing != nil && r.Marketing.Original != nil {
		t.Tachado = r.Marketing.Original.texto()
		if r.Marketing.Descuento != "" {
			t.Descuento = "-" + strings.TrimSuffix(r.Marketing.Descuento, ".0") + " %"
		}
	}

	if len(r.Envios) > 0 && r.Envios[0].Coste != nil {
		if esCero(r.Envios[0].Coste.Valor) {
			t.EnvioGratis = true
		} else {
			t.Envio = r.Envios[0].Coste.texto()
		}
	}

	if r.Ubicacion != nil {
		t.Pais = nombrePais(r.Ubicacion.Pais)
	}
	if r.Vendedor != nil {
		t.Vendedor = r.Vendedor.Usuario
		t.Valoracion = formatoPorcentaje(r.Vendedor.Porcentaje)
		if r.Vendedor.Puntos > 0 {
			t.Valoracion += " · " + formatoEntero(r.Vendedor.Puntos)
		}
	}
	return t
}

type enlacePagina struct {
	Numero int
	Enlace string
	Actual bool
	Hueco  bool
}

type resultadoBusqueda struct {
	Total int
	// Descartados cuenta los artículos del bloque actual que, con el envío
	// incluido, quedan fuera del rango de precio pedido.
	Descartados int
	Tarjetas    []tarjeta
	Paginas     []enlacePagina
	Anterior    string
	Siguiente   string
}

// paginacion devuelve la primera y la última página, y las dos vecinas de la
// actual a cada lado, con huecos donde se salta.
func paginacion(f filtrosBusqueda, total int) ([]enlacePagina, string, string) {
	ultima := min((total+porPagina-1)/porPagina, maxPaginas)
	if ultima <= 1 {
		return nil, "", ""
	}
	var paginas []enlacePagina
	anterior := 0
	for n := 1; n <= ultima; n++ {
		if n != 1 && n != ultima && (n < f.Pagina-2 || n > f.Pagina+2) {
			continue
		}
		if anterior != 0 && n-anterior > 1 {
			paginas = append(paginas, enlacePagina{Hueco: true})
		}
		paginas = append(paginas, enlacePagina{Numero: n, Enlace: f.enlace(n), Actual: n == f.Pagina})
		anterior = n
	}
	var previa, siguiente string
	if f.Pagina > 1 {
		previa = f.enlace(f.Pagina - 1)
	}
	if f.Pagina < ultima {
		siguiente = f.enlace(f.Pagina + 1)
	}
	return paginas, previa, siguiente
}

var cacheBusquedas = nuevaCache[respuestaBusquedaAPI]()

func buscar(ctx context.Context, ebay *clienteEbay, f filtrosBusqueda) (*resultadoBusqueda, error) {
	consulta := f.consultaAPI()
	clave := f.Marketplace.ID + "\x00" + consulta.Encode()

	resp, ok := cacheBusquedas.obtener(clave)
	if !ok {
		if err := ebay.peticion(ctx, f.Marketplace.ID, "/buy/browse/v1/item_summary/search", consulta, &resp); err != nil {
			return nil, err
		}
		cacheBusquedas.guardar(clave, resp)
	}

	// Se limpia el bloque entero antes de repartirlo en páginas, para que
	// las páginas salgan completas aunque haya repetidos o descartados.
	res := &resultadoBusqueda{Total: resp.Total}
	vistos := map[string]bool{}
	var validos []resumenAPI
	for _, r := range resp.Resumenes {
		if vistos[r.ItemID] {
			continue
		}
		vistos[r.ItemID] = true
		if f.fueraDeRango(r) {
			res.Descartados++
			continue
		}
		validos = append(validos, r)
	}

	inicio := ((f.Pagina - 1) % paginasPorBloque) * porPagina
	if inicio < len(validos) {
		for _, r := range validos[inicio:min(inicio+porPagina, len(validos))] {
			res.Tarjetas = append(res.Tarjetas, nuevaTarjeta(r))
		}
	}
	res.Paginas, res.Anterior, res.Siguiente = paginacion(f, resp.Total)
	return res, nil
}

// mensajeError explica al usuario un fallo de la API sin ocultar el detalle
// técnico, que en una instancia personal es justo lo que hace falta ver.
func mensajeError(err error) (string, string) {
	var e *errorEbay
	if errors.As(err, &e) {
		if e.Estado == http.StatusTooManyRequests {
			return "Se ha agotado el cupo diario de la API de eBay. Vuelve a intentarlo cuando se reinicie.", e.Error()
		}
		return "eBay no ha podido atender la petición.", e.Error()
	}
	return "No se pudo contactar con eBay.", err.Error()
}

func manejadorBusqueda(ebay *clienteEbay) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := leerFiltros(w, r)
		if f.Consulta == "" {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		datos := datosPagina{
			Titulo:   f.Consulta,
			Consulta: f.Consulta,
			Filtros:  &f,
		}
		res, err := buscar(r.Context(), ebay, f)
		if err != nil {
			log.Printf("búsqueda %q en %s: %v", f.Consulta, f.Marketplace.ID, err)
			datos.Error, datos.Detalle = mensajeError(err)
			renderizar(w, http.StatusBadGateway, "resultados", datos)
			return
		}
		datos.Resultados = res
		renderizar(w, http.StatusOK, "resultados", datos)
	}
}
