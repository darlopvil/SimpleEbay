package main

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
)

// datosPagina es lo que reciben todas las plantillas. Cada página nueva
// añadirá aquí sus propios campos.
type datosPagina struct {
	Titulo   string
	Mensaje  string
	Consulta string
	// Inicio oculta el buscador de la cabecera en la portada, que ya tiene
	// el suyo en grande.
	Inicio bool

	// Error y Detalle describen un fallo de la API: el primero para leerlo,
	// el segundo con el mensaje técnico de eBay.
	Error   string
	Detalle string

	Filtros    *filtrosBusqueda
	Resultados *resultadoBusqueda
	Ficha      *ficha
}

var funcionesPlantilla = template.FuncMap{
	"marketplaces":      func() []marketplace { return marketplaces },
	"opcionesOrden":     func() []opcion { return opcionesOrden },
	"opcionesEstado":    func() []opcion { return opcionesEstado },
	"opcionesCompra":    func() []opcion { return opcionesCompra },
	"opcionesUbicacion": func() []opcion { return opcionesUbicacion },
	"miles":             formatoEntero,
	"add":               func(a, b int) int { return a + b },
}

// Cada página se parsea en su propio conjunto junto a la base, porque todas
// definen el mismo bloque "contenido".
func cargarPlantilla(pagina string) *template.Template {
	return template.Must(template.New("base.html").Funcs(funcionesPlantilla).ParseFS(
		plantillasFS,
		"templates/base.html",
		"templates/"+pagina+".html",
	))
}

var plantillas = map[string]*template.Template{
	"inicio":     cargarPlantilla("inicio"),
	"error":      cargarPlantilla("error"),
	"resultados": cargarPlantilla("resultados"),
	"ficha":      cargarPlantilla("ficha"),
}

// renderizar ejecuta la plantilla en un búfer antes de escribir nada. Así un
// fallo a mitad de página se convierte en un 500 limpio y no en media página
// servida con un 200.
func renderizar(w http.ResponseWriter, estado int, pagina string, datos datosPagina) {
	var buf bytes.Buffer
	if err := plantillas[pagina].ExecuteTemplate(&buf, "base.html", datos); err != nil {
		log.Printf("error al renderizar %s: %v", pagina, err)
		http.Error(w, "Error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(estado)
	_, _ = buf.WriteTo(w)
}
