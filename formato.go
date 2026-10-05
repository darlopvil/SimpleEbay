package main

import (
	"strconv"
	"strings"
	"time"
)

// Símbolos de las monedas de los marketplaces soportados. Las que comparten
// el signo del dólar se distinguen por el prefijo del país.
var simbolosMoneda = map[string]string{
	"EUR": "€",
	"USD": "US$",
	"GBP": "£",
	"CHF": "CHF",
	"PLN": "zł",
	"CAD": "C$",
	"AUD": "AU$",
	"HKD": "HK$",
	"SGD": "S$",
}

// formatoDinero convierte el importe de la API ("1234.5", "EUR") al formato
// español ("1.234,50 €"). Si el valor no es un número, se devuelve tal cual
// antes que inventar uno.
func formatoDinero(valor, moneda string) string {
	if valor == "" {
		return ""
	}
	simbolo := simbolosMoneda[moneda]
	if simbolo == "" {
		simbolo = moneda
	}
	n, err := strconv.ParseFloat(valor, 64)
	if err != nil {
		return valor + " " + simbolo
	}
	entero, decimales, _ := strings.Cut(strconv.FormatFloat(n, 'f', 2, 64), ".")
	negativo := strings.HasPrefix(entero, "-")
	entero = strings.TrimPrefix(entero, "-")

	var b strings.Builder
	for i, c := range entero {
		if i > 0 && (len(entero)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	signo := ""
	if negativo {
		signo = "-"
	}
	return signo + b.String() + "," + decimales + " " + simbolo
}

// esCero indica si un importe de la API vale cero, que en los gastos de
// envío significa envío gratis.
func esCero(valor string) bool {
	n, err := strconv.ParseFloat(valor, 64)
	return err == nil && n == 0
}

var mesesCortos = [...]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}

// formatoFechaHora da la hora local del servidor, que se fija con la
// variable TZ. La imagen embebe la base de zonas horarias, de modo que TZ
// funciona aunque el contenedor sea scratch.
func formatoFechaHora(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	t = t.Local()
	return strconv.Itoa(t.Day()) + " " + mesesCortos[t.Month()-1] + ", " + t.Format("15:04")
}

// tiempoRestante resume lo que queda de una subasta. La página se cachea
// unos minutos, así que no tiene sentido afinar más allá del minuto.
func tiempoRestante(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	d := time.Until(t)
	switch {
	case d <= 0:
		return "terminada"
	case d < time.Hour:
		return "quedan " + strconv.Itoa(int(d.Minutes())+1) + " min"
	case d < 24*time.Hour:
		return "quedan " + strconv.Itoa(int(d.Hours())) + " h " + strconv.Itoa(int(d.Minutes())%60) + " min"
	default:
		return "quedan " + strconv.Itoa(int(d.Hours())/24) + " d " + strconv.Itoa(int(d.Hours())%24) + " h"
	}
}

// formatoPorcentaje convierte "99.6" en "99,6 %".
func formatoPorcentaje(valor string) string {
	if valor == "" {
		return ""
	}
	valor = strings.TrimSuffix(valor, ".0")
	return strings.Replace(valor, ".", ",", 1) + " %"
}

// formatoEntero añade separador de miles: 20792 → "20.792".
func formatoEntero(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	return b.String()
}
