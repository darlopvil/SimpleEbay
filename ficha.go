package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Estructuras de la respuesta de getItem y getItemsByItemGroup, solo con los
// campos que se usan.

type envioAPI struct {
	Servicio    string      `json:"shippingServiceCode"`
	Tipo        string      `json:"type"`
	Coste       *importeAPI `json:"shippingCost"`
	Importacion *importeAPI `json:"importCharges"`
	EntregaMin  string      `json:"minEstimatedDeliveryDate"`
	EntregaMax  string      `json:"maxEstimatedDeliveryDate"`
}

type direccionAPI struct {
	Linea1    string `json:"addressLine1"`
	Linea2    string `json:"addressLine2"`
	Ciudad    string `json:"city"`
	Provincia string `json:"stateOrProvince"`
	CP        string `json:"postalCode"`
	Pais      string `json:"countryName"`
}

type legalAPI struct {
	Nombre     string        `json:"name"`
	NombrePila string        `json:"legalContactFirstName"`
	Apellido   string        `json:"legalContactLastName"`
	Direccion  *direccionAPI `json:"sellerProvidedLegalAddress"`
	Telefono   string        `json:"phone"`
	Email      string        `json:"email"`
	Registro   string        `json:"tradeRegistrationNumber"`
	IVA        []struct {
		ID   string `json:"vatId"`
		Pais string `json:"issuingCountry"`
	} `json:"vatDetails"`
}

type itemAPI struct {
	ItemID           string      `json:"itemId"`
	LegacyID         string      `json:"legacyItemId"`
	Titulo           string      `json:"title"`
	DescripcionCorta string      `json:"shortDescription"`
	Precio           *importeAPI `json:"price"`
	PujaActual       *importeAPI `json:"currentBidPrice"`
	PujaMinima       *importeAPI `json:"minimumPriceToBid"`
	Pujadores        int         `json:"uniqueBidderCount"`
	Fin              string      `json:"itemEndDate"`
	Formatos         []string    `json:"buyingOptions"`
	Estado           string      `json:"condition"`
	EstadoID         string      `json:"conditionId"`
	NotaEstado       string      `json:"conditionDescription"`
	Marketing        *struct {
		Original  *importeAPI `json:"originalPrice"`
		Descuento string      `json:"discountPercentage"`
	} `json:"marketingPrice"`
	Imagen      *imagenAPI  `json:"image"`
	Adicionales []imagenAPI `json:"additionalImages"`
	Categoria   string      `json:"categoryPath"`
	Ubicacion   *struct {
		Ciudad    string `json:"city"`
		Provincia string `json:"stateOrProvince"`
		Pais      string `json:"country"`
	} `json:"itemLocation"`
	Vendedor *struct {
		vendedorAPI
		TipoCuenta string    `json:"sellerAccountType"`
		Legal      *legalAPI `json:"sellerLegalInfo"`
	} `json:"seller"`
	Disponibilidad []struct {
		Estado      string `json:"estimatedAvailabilityStatus"`
		Disponibles int    `json:"estimatedAvailableQuantity"`
		Vendidos    int    `json:"estimatedSoldQuantity"`
	} `json:"estimatedAvailabilities"`
	Envios       []envioAPI `json:"shippingOptions"`
	Devoluciones *struct {
		Acepta        bool   `json:"returnsAccepted"`
		Paga          string `json:"returnShippingCostPayer"`
		Instrucciones string `json:"returnInstructions"`
		Periodo       *struct {
			Valor  int    `json:"value"`
			Unidad string `json:"unit"`
		} `json:"returnPeriod"`
	} `json:"returnTerms"`
	Aspectos []struct {
		Nombre string `json:"name"`
		Valor  string `json:"value"`
	} `json:"localizedAspects"`
	Opiniones *struct {
		Numero int    `json:"reviewCount"`
		Media  string `json:"averageRating"`
	} `json:"primaryProductReviewRating"`
	Pagos []struct {
		Tipo   string `json:"paymentMethodType"`
		Marcas []struct {
			Marca string `json:"paymentMethodBrandType"`
		} `json:"paymentMethodBrands"`
	} `json:"paymentMethods"`
	Grupo *struct {
		ID          string      `json:"itemGroupId"`
		Imagen      *imagenAPI  `json:"itemGroupImage"`
		Adicionales []imagenAPI `json:"itemGroupAdditionalImages"`
	} `json:"primaryItemGroup"`
	Responsables []struct {
		Empresa string `json:"companyName"`
		direccionAPI
		Email string `json:"email"`
	} `json:"responsiblePersons"`
	URL         string `json:"itemWebUrl"`
	Marketplace string `json:"listingMarketplaceId"`
}

type grupoAPI struct {
	Items []itemAPI `json:"items"`
}

// Lo que se pinta en la ficha. Como en las tarjetas, todo el formato se
// resuelve aquí y la plantilla solo coloca textos.

type foto struct {
	Grande    string
	Miniatura string
	Original  string
}

type filaEnvio struct {
	Servicio    string
	Coste       string
	Gratis      bool
	Importacion string
	Entrega     string
}

type variante struct {
	Etiqueta string
	Precio   string
	Enlace   string
	Actual   bool
}

type dato struct {
	Nombre string
	Valor  string
}

type ficha struct {
	Titulo        string
	Categoria     string
	Fotos         []foto
	FotosExternas int

	Precio       string
	PrecioOrigen string
	Tachado      string
	Descuento    string
	Opiniones    int
	NotaMedia    string

	Subasta    bool
	Pujadores  int
	PujaMinima string
	Cierre     string
	Restante   string
	Ofertas    bool

	EjeVariacion string
	Variantes    []variante

	Estado         string
	NotaEstado     string
	Disponibilidad string
	Ubicacion      string
	Devoluciones   string
	NotaDevolucion string
	Pagos          string
	Envios         []filaEnvio

	Vendedor     string
	Valoracion   string
	Profesional  bool
	Legal        []dato
	Responsables []string

	Aspectos         []dato
	DescripcionCorta string

	URLeBay            string
	MarketplaceAnuncio string
}

// formatoFecha da solo el día: "3 nov".
func formatoFecha(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	t = t.Local()
	return strconv.Itoa(t.Day()) + " " + mesesCortos[t.Month()-1]
}

func textoEntrega(min, max string) string {
	desde, hasta := formatoFecha(min), formatoFecha(max)
	switch {
	case desde == "" && hasta == "":
		return ""
	case desde == hasta || hasta == "":
		return "el " + desde
	case desde == "":
		return "antes del " + hasta
	default:
		return "entre el " + desde + " y el " + hasta
	}
}

var nombresPago = map[string]string{
	"PAYPAL":           "PayPal",
	"APPLE_PAY":        "Apple Pay",
	"GOOGLE_PAY":       "Google Pay",
	"VISA":             "Visa",
	"MASTERCARD":       "Mastercard",
	"AMERICAN_EXPRESS": "American Express",
	"DINERS_CLUB":      "Diners Club",
	"DISCOVER":         "Discover",
	"MAESTRO":          "Maestro",
}

// textoDevoluciones resume la política: plazo y quién paga el envío.
func (it *itemAPI) textoDevoluciones() string {
	d := it.Devoluciones
	if d == nil {
		return ""
	}
	if !d.Acepta {
		return "No se aceptan devoluciones"
	}
	texto := "Se aceptan devoluciones"
	if d.Periodo != nil && d.Periodo.Valor > 0 {
		unidad := "días"
		switch d.Periodo.Unidad {
		case "BUSINESS_DAY":
			unidad = "días hábiles"
		case "MONTH":
			unidad = "meses"
		}
		texto = "Devolución en " + strconv.Itoa(d.Periodo.Valor) + " " + unidad
	}
	switch d.Paga {
	case "BUYER":
		texto += "; el envío de vuelta lo paga el comprador"
	case "SELLER":
		texto += "; el envío de vuelta lo paga el vendedor"
	}
	return texto
}

func (it *itemAPI) textoPagos() string {
	var nombres []string
	visto := map[string]bool{}
	for _, p := range it.Pagos {
		for _, m := range p.Marcas {
			n := nombresPago[m.Marca]
			if n == "" {
				n = m.Marca
			}
			if !visto[n] {
				visto[n] = true
				nombres = append(nombres, n)
			}
		}
	}
	return strings.Join(nombres, ", ")
}

func (it *itemAPI) textoDisponibilidad(subasta bool) string {
	if len(it.Disponibilidad) == 0 {
		return ""
	}
	d := it.Disponibilidad[0]
	if d.Estado == "OUT_OF_STOCK" {
		return "Agotado"
	}
	// En una subasta la cantidad siempre es una y no aporta nada.
	if subasta {
		return ""
	}
	var partes []string
	if d.Disponibles > 1 {
		partes = append(partes, strconv.Itoa(d.Disponibles)+" disponibles")
	} else if d.Estado == "LIMITED_STOCK" {
		partes = append(partes, "Pocas unidades")
	}
	if d.Vendidos > 0 {
		partes = append(partes, formatoEntero(d.Vendidos)+" vendidos")
	}
	return strings.Join(partes, " · ")
}

// fotos reúne las imágenes del artículo que están en eBay. Las variaciones a
// veces enlazan su foto desde otro servidor; en ese caso se completan con
// las del grupo, que siempre están en eBay.
func (it *itemAPI) fotos() ([]foto, int) {
	var fuentes []imagenAPI
	if it.Imagen != nil {
		fuentes = append(fuentes, *it.Imagen)
	}
	fuentes = append(fuentes, it.Adicionales...)
	if it.Grupo != nil {
		if it.Grupo.Imagen != nil {
			fuentes = append(fuentes, *it.Grupo.Imagen)
		}
		fuentes = append(fuentes, it.Grupo.Adicionales...)
	}

	var fotos []foto
	externas := 0
	vistas := map[string]bool{}
	for _, f := range fuentes {
		original := rutaImagen(f.URL, 1600)
		if original == "" {
			externas++
			continue
		}
		if vistas[original] {
			continue
		}
		vistas[original] = true
		fotos = append(fotos, foto{
			Grande:    rutaImagen(f.URL, 960),
			Miniatura: rutaImagen(f.URL, 140),
			Original:  original,
		})
	}
	return fotos, externas
}

// urlLimpia deja la dirección del anuncio en eBay sin parámetros de
// seguimiento, salvo el de la variación.
func urlLimpia(direccion string) string {
	u, err := url.Parse(direccion)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if !strings.HasPrefix(host, "ebay.") && !strings.Contains(host, ".ebay.") {
		return ""
	}
	limpia := url.URL{Scheme: "https", Host: u.Host, Path: u.Path}
	if v := u.Query().Get("var"); v != "" {
		limpia.RawQuery = "var=" + url.QueryEscape(v)
	}
	return limpia.String()
}

func nombreMarketplace(id string) string {
	if m, ok := buscarMarketplace(id); ok {
		return "eBay " + m.Nombre
	}
	return id
}

// variantes construye el selector a partir de los aspectos que cambian entre
// los artículos del grupo (color, talla…), que son los ejes de la variación.
func variantes(grupo *grupoAPI, actual string) (string, []variante) {
	if grupo == nil || len(grupo.Items) < 2 {
		return "", nil
	}
	valores := make([]map[string]string, len(grupo.Items))
	var orden []string
	distintos := map[string]map[string]bool{}
	for i, it := range grupo.Items {
		valores[i] = map[string]string{}
		for _, a := range it.Aspectos {
			valores[i][a.Nombre] = a.Valor
			if distintos[a.Nombre] == nil {
				distintos[a.Nombre] = map[string]bool{}
				orden = append(orden, a.Nombre)
			}
			distintos[a.Nombre][a.Valor] = true
		}
	}
	var ejes []string
	for _, nombre := range orden {
		if len(distintos[nombre]) > 1 {
			ejes = append(ejes, nombre)
		}
	}

	precios := map[string]bool{}
	for _, it := range grupo.Items {
		precios[it.Precio.texto()] = true
	}

	var lista []variante
	for i, it := range grupo.Items {
		var partes []string
		for _, eje := range ejes {
			if v := valores[i][eje]; v != "" {
				partes = append(partes, v)
			}
		}
		etiqueta := strings.Join(partes, " / ")
		if etiqueta == "" {
			etiqueta = "Opción " + strconv.Itoa(i+1)
		}
		v := variante{
			Etiqueta: etiqueta,
			Enlace:   enlaceFicha(it.LegacyID, it.ItemID),
			Actual:   it.ItemID == actual,
		}
		if len(precios) > 1 {
			v.Precio = it.Precio.texto()
		}
		lista = append(lista, v)
	}
	return strings.Join(ejes, " / "), lista
}

func nuevaFicha(it *itemAPI, grupo *grupoAPI) *ficha {
	f := &ficha{
		Titulo:           it.Titulo,
		Categoria:        strings.ReplaceAll(it.Categoria, "|", " › "),
		Subasta:          contiene(it.Formatos, "AUCTION"),
		Ofertas:          contiene(it.Formatos, "BEST_OFFER"),
		Estado:           nombreEstado(it.Estado, it.EstadoID),
		NotaEstado:       it.NotaEstado,
		Devoluciones:     it.textoDevoluciones(),
		Pagos:            it.textoPagos(),
		DescripcionCorta: it.DescripcionCorta,
		URLeBay:          urlLimpia(it.URL),
	}
	if it.Marketplace != "" {
		f.MarketplaceAnuncio = nombreMarketplace(it.Marketplace)
	}
	if it.Devoluciones != nil {
		f.NotaDevolucion = it.Devoluciones.Instrucciones
	}
	f.Fotos, f.FotosExternas = it.fotos()
	f.Disponibilidad = it.textoDisponibilidad(f.Subasta)

	precio := it.Precio
	if f.Subasta && it.PujaActual != nil {
		precio = it.PujaActual
		f.Pujadores = it.Pujadores
		f.Cierre = formatoFechaHora(it.Fin)
		f.Restante = tiempoRestante(it.Fin)
		f.PujaMinima = it.PujaMinima.texto()
	}
	f.Precio = precio.texto()
	f.PrecioOrigen = precio.textoOrigen()
	if it.Marketing != nil && it.Marketing.Original != nil {
		f.Tachado = it.Marketing.Original.texto()
		if it.Marketing.Descuento != "" {
			f.Descuento = "-" + strings.TrimSuffix(it.Marketing.Descuento, ".0") + " %"
		}
	}
	if it.Opiniones != nil && it.Opiniones.Numero > 0 {
		f.Opiniones = it.Opiniones.Numero
		f.NotaMedia = strings.Replace(it.Opiniones.Media, ".", ",", 1)
	}

	for _, e := range it.Envios {
		fila := filaEnvio{
			Servicio: e.Servicio,
			Entrega:  textoEntrega(e.EntregaMin, e.EntregaMax),
		}
		if e.Coste != nil {
			if esCero(e.Coste.Valor) {
				fila.Gratis = true
			} else {
				fila.Coste = e.Coste.texto()
			}
		}
		if e.Importacion != nil && !esCero(e.Importacion.Valor) {
			fila.Importacion = e.Importacion.texto()
		}
		f.Envios = append(f.Envios, fila)
	}

	if u := it.Ubicacion; u != nil {
		var partes []string
		for _, p := range []string{u.Ciudad, u.Provincia, nombrePais(u.Pais)} {
			if p != "" {
				partes = append(partes, p)
			}
		}
		f.Ubicacion = strings.Join(partes, ", ")
	}

	if v := it.Vendedor; v != nil {
		f.Vendedor = v.Usuario
		if v.Porcentaje != "" {
			f.Valoracion = formatoPorcentaje(v.Porcentaje) + " positivas"
		}
		if v.Puntos > 0 {
			f.Valoracion = unirNoVacios([]string{f.Valoracion, formatoEntero(v.Puntos) + " valoraciones"}, " · ")
		}
		f.Profesional = v.TipoCuenta == "BUSINESS"
		if l := v.Legal; l != nil {
			f.Legal = datosLegales(l)
		}
	}
	for _, r := range it.Responsables {
		partes := []string{r.Empresa, r.Linea1, strings.TrimSpace(r.CP + " " + r.Ciudad), r.Pais, r.Email}
		f.Responsables = append(f.Responsables, unirNoVacios(partes, ", "))
	}

	for _, a := range it.Aspectos {
		f.Aspectos = append(f.Aspectos, dato{Nombre: a.Nombre, Valor: a.Valor})
	}
	f.EjeVariacion, f.Variantes = variantes(grupo, it.ItemID)
	return f
}

func unirNoVacios(partes []string, sep string) string {
	var r []string
	for _, p := range partes {
		if strings.TrimSpace(p) != "" {
			r = append(r, strings.TrimSpace(p))
		}
	}
	return strings.Join(r, sep)
}

// datosLegales recoge la información que la normativa europea obliga a
// mostrar de los vendedores profesionales. eBay también la muestra, plegada.
func datosLegales(l *legalAPI) []dato {
	var datos []dato
	if l.Nombre != "" {
		datos = append(datos, dato{"Nombre", l.Nombre})
	}
	if c := unirNoVacios([]string{l.NombrePila, l.Apellido}, " "); c != "" {
		datos = append(datos, dato{"Contacto", c})
	}
	if d := l.Direccion; d != nil {
		dir := unirNoVacios([]string{d.Linea1, d.Linea2, strings.TrimSpace(d.CP + " " + d.Ciudad), d.Provincia, d.Pais}, ", ")
		if dir != "" {
			datos = append(datos, dato{"Dirección", dir})
		}
	}
	if l.Telefono != "" {
		datos = append(datos, dato{"Teléfono", l.Telefono})
	}
	if l.Email != "" {
		datos = append(datos, dato{"Correo", l.Email})
	}
	for _, iva := range l.IVA {
		datos = append(datos, dato{"IVA", unirNoVacios([]string{iva.ID, iva.Pais}, " · ")})
	}
	if l.Registro != "" {
		datos = append(datos, dato{"Registro mercantil", l.Registro})
	}
	return datos
}

var (
	cacheItems  = nuevaCache[*itemAPI]()
	cacheGrupos = nuevaCache[*grupoAPI]()
)

func obtenerGrupo(ctx context.Context, ebay *clienteEbay, mp, id string) (*grupoAPI, error) {
	clave := mp + "\x00" + id
	if g, ok := cacheGrupos.obtener(clave); ok {
		return g, nil
	}
	var g grupoAPI
	consulta := url.Values{"item_group_id": {id}}
	if err := ebay.peticion(ctx, mp, "/buy/browse/v1/item/get_items_by_item_group", consulta, &g); err != nil {
		return nil, err
	}
	if len(g.Items) == 0 {
		return nil, errors.New("el grupo de variaciones está vacío")
	}
	cacheGrupos.guardar(clave, &g)
	return &g, nil
}

func obtenerItem(ctx context.Context, ebay *clienteEbay, mp, id string) (*itemAPI, error) {
	clave := mp + "\x00" + id
	if it, ok := cacheItems.obtener(clave); ok {
		return it, nil
	}
	var it itemAPI
	consulta := url.Values{"legacy_item_id": {id}, "fieldgroups": {"PRODUCT"}}
	if err := ebay.peticion(ctx, mp, "/buy/browse/v1/item/get_item_by_legacy_id", consulta, &it); err != nil {
		return nil, err
	}
	cacheItems.guardar(clave, &it)
	return &it, nil
}

// errorEsGrupo es el error 11006: el ID pertenece a un anuncio con
// variaciones y hay que pedir el grupo entero.
const errorEsGrupo = 11006

// obtenerFicha resuelve un ID de eBay. Con ?var= se pide directamente el
// grupo, que trae todas las variaciones en una sola llamada y sirve también
// para el selector. Sin él se pide el artículo, y si eBay responde que es un
// grupo, se pide el grupo y se muestra su primera variación.
func obtenerFicha(ctx context.Context, ebay *clienteEbay, mp, id, variacion string) (*itemAPI, *grupoAPI, error) {
	if variacion != "" {
		if g, err := obtenerGrupo(ctx, ebay, mp, id); err == nil {
			for i := range g.Items {
				if strings.HasSuffix(g.Items[i].ItemID, "|"+variacion) {
					return &g.Items[i], g, nil
				}
			}
			return &g.Items[0], g, nil
		}
	}

	it, err := obtenerItem(ctx, ebay, mp, id)
	var e *errorEbay
	if errors.As(err, &e) && e.tieneError(errorEsGrupo) {
		g, err := obtenerGrupo(ctx, ebay, mp, id)
		if err != nil {
			return nil, nil, err
		}
		return &g.Items[0], g, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return it, nil, nil
}

func soloDigitos(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func manejadorFicha(ebay *clienteEbay) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/itm/")
		variacion := r.URL.Query().Get("var")
		if !soloDigitos(id) || (variacion != "" && !soloDigitos(variacion)) {
			renderizar(w, http.StatusNotFound, "error", datosPagina{
				Titulo:  "Anuncio no encontrado",
				Mensaje: "La dirección no corresponde a ningún anuncio de eBay.",
			})
			return
		}
		mp := marketplaceElegido(w, r)

		it, grupo, err := obtenerFicha(r.Context(), ebay, mp.ID, id, variacion)
		if err != nil {
			log.Printf("ficha %s (var %q) en %s: %v", id, variacion, mp.ID, err)
			var e *errorEbay
			if errors.As(err, &e) && e.Estado == http.StatusNotFound {
				renderizar(w, http.StatusNotFound, "error", datosPagina{
					Titulo:  "Anuncio no disponible",
					Mensaje: "Este anuncio no existe o ya ha terminado.",
				})
				return
			}
			mensaje, detalle := mensajeError(err)
			renderizar(w, http.StatusBadGateway, "error", datosPagina{
				Titulo:  "Algo ha fallado",
				Mensaje: mensaje,
				Detalle: detalle,
			})
			return
		}
		f := nuevaFicha(it, grupo)
		renderizar(w, http.StatusOK, "ficha", datosPagina{Titulo: f.Titulo, Ficha: f})
	}
}
