package main

// marketplace describe un sitio de eBay en el que la Browse API admite
// búsquedas. La moneda hace falta para el filtro de precio, que eBay solo
// acepta acompañado de priceCurrency.
type marketplace struct {
	ID     string
	Nombre string
	Moneda string
}

// Los dieciséis marketplaces que la documentación de las Buy APIs da como
// soportados. El orden es el del selector.
var marketplaces = []marketplace{
	{"EBAY_ES", "España", "EUR"},
	{"EBAY_US", "Estados Unidos", "USD"},
	{"EBAY_GB", "Reino Unido", "GBP"},
	{"EBAY_DE", "Alemania", "EUR"},
	{"EBAY_FR", "Francia", "EUR"},
	{"EBAY_IT", "Italia", "EUR"},
	{"EBAY_NL", "Países Bajos", "EUR"},
	{"EBAY_BE", "Bélgica", "EUR"},
	{"EBAY_AT", "Austria", "EUR"},
	{"EBAY_IE", "Irlanda", "EUR"},
	{"EBAY_CH", "Suiza", "CHF"},
	{"EBAY_PL", "Polonia", "PLN"},
	{"EBAY_CA", "Canadá", "CAD"},
	{"EBAY_AU", "Australia", "AUD"},
	{"EBAY_HK", "Hong Kong", "HKD"},
	{"EBAY_SG", "Singapur", "SGD"},
}

const marketplacePorDefecto = "EBAY_ES"

func buscarMarketplace(id string) (marketplace, bool) {
	for _, m := range marketplaces {
		if m.ID == id {
			return m, true
		}
	}
	return marketplace{}, false
}

// eBay manda el texto del estado en el idioma del anuncio ("Gebraucht",
// "Like New"), y a veces ni lo manda. El conditionId es común a todos los
// marketplaces, así que se traduce desde aquí.
var nombresEstado = map[string]string{
	"1000": "Nuevo",
	"1500": "Nuevo (otro)",
	"1750": "Nuevo con defectos",
	"2000": "Reacondicionado certificado",
	"2010": "Reacondicionado: excelente",
	"2020": "Reacondicionado: muy bueno",
	"2030": "Reacondicionado: bueno",
	"2500": "Reacondicionado por el vendedor",
	"2750": "Como nuevo",
	"2990": "Usado: excelente",
	"3000": "Usado",
	"3010": "Usado: aceptable",
	"4000": "Muy bueno",
	"5000": "Bueno",
	"6000": "Aceptable",
	"7000": "Para piezas o no funciona",
}

func nombreEstado(texto, id string) string {
	if nombre, ok := nombresEstado[id]; ok {
		return nombre
	}
	return texto
}

// Nombres de los países que más aparecen como origen de los artículos. Para
// el resto se muestra el código ISO, que sigue siendo legible.
var nombresPais = map[string]string{
	"ES": "España", "PT": "Portugal", "FR": "Francia", "IT": "Italia",
	"DE": "Alemania", "AT": "Austria", "CH": "Suiza", "BE": "Bélgica",
	"NL": "Países Bajos", "LU": "Luxemburgo", "IE": "Irlanda", "GB": "Reino Unido",
	"PL": "Polonia", "CZ": "Chequia", "SE": "Suecia", "DK": "Dinamarca",
	"FI": "Finlandia", "NO": "Noruega", "GR": "Grecia", "RO": "Rumanía",
	"HU": "Hungría", "LT": "Lituania", "LV": "Letonia", "EE": "Estonia",
	"US": "Estados Unidos", "CA": "Canadá", "MX": "México", "BR": "Brasil",
	"AU": "Australia", "NZ": "Nueva Zelanda", "JP": "Japón", "CN": "China",
	"HK": "Hong Kong", "TW": "Taiwán", "KR": "Corea del Sur", "SG": "Singapur",
	"MY": "Malasia", "TH": "Tailandia", "IN": "India", "IL": "Israel",
	"TR": "Turquía", "UA": "Ucrania",
}

func nombrePais(codigo string) string {
	if n, ok := nombresPais[codigo]; ok {
		return n
	}
	return codigo
}
