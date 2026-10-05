package main

import (
	"bytes"
	"html/template"
	"net/url"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// La descripción de un anuncio es HTML escrito por el vendedor, a menudo con
// plantillas de terceros: hojas de estilo y fuentes externas, bloques ocultos
// con galerías duplicadas, imágenes en otros servidores y scripts. Se limpia
// en dos pasos. Primero se recorre el árbol para quitar lo que no debe
// mostrarse aunque fuera inofensivo y llevar las imágenes a los proxies.
// Después bluemonday aplica una lista blanca de etiquetas y atributos, que es
// la barrera de seguridad de verdad.

// Elementos que se eliminan con todo su contenido.
var elementosDescartados = map[atom.Atom]bool{
	atom.Head: true, atom.Title: true, atom.Meta: true, atom.Link: true,
	atom.Base: true, atom.Style: true, atom.Script: true, atom.Noscript: true,
	atom.Template: true, atom.Iframe: true, atom.Frame: true, atom.Frameset: true,
	atom.Object: true, atom.Embed: true, atom.Applet: true, atom.Svg: true,
	atom.Math: true, atom.Video: true, atom.Audio: true, atom.Source: true,
	atom.Track: true, atom.Canvas: true, atom.Form: true, atom.Input: true,
	atom.Button: true, atom.Select: true, atom.Textarea: true, atom.Label: true,
}

// Las plantillas de los vendedores esconden con estilos bloques enteros que
// eBay no muestra. Al quitar los estilos aparecerían, así que se eliminan
// antes.
var patronOculto = regexp.MustCompile(`(?i)(display\s*:\s*none|visibility\s*:\s*hidden)`)

// La política de contenido de la página prohíbe los estilos en línea, así que
// los pocos que importan para leer el texto se traducen a clases definidas en
// la hoja de estilos. Colores, tipos de letra y tamaños se descartan: chocarían
// con el tema claro u oscuro y con la maquetación. white-space es
// imprescindible, porque muchos vendedores escriben con saltos de línea y
// confían en un pre-line para que se vean.
var clasesEstilo = map[string]map[string]string{
	"white-space": {
		"pre": "d-pre", "pre-line": "d-pre-line", "pre-wrap": "d-pre-wrap", "nowrap": "d-nowrap",
	},
	"text-align": {
		"left": "d-izquierda", "right": "d-derecha", "center": "d-centro", "justify": "d-justificado",
	},
	"font-weight": {
		"bold": "d-negrita", "bolder": "d-negrita", "600": "d-negrita", "700": "d-negrita", "800": "d-negrita", "900": "d-negrita",
	},
	"font-style": {
		"italic": "d-cursiva", "oblique": "d-cursiva",
	},
	"text-decoration": {
		"underline": "d-subrayado", "line-through": "d-tachado",
	},
}

// clasesDeEstilo traduce el atributo style a las clases equivalentes.
func clasesDeEstilo(estilo string) []string {
	var clases []string
	for _, declaracion := range strings.Split(estilo, ";") {
		propiedad, valor, ok := strings.Cut(declaracion, ":")
		if !ok {
			continue
		}
		propiedad = strings.ToLower(strings.TrimSpace(propiedad))
		valor = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(valor), "!important")))
		if clase := clasesEstilo[propiedad][valor]; clase != "" {
			clases = append(clases, clase)
		}
	}
	return clases
}

func quitarAtributo(n *html.Node, nombre string) {
	attrs := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Key != nombre {
			attrs = append(attrs, a)
		}
	}
	n.Attr = attrs
}

// Rutas de un anuncio en eBay: /itm/123456789012 o /itm/titulo/123456789012.
var patronRutaAnuncio = regexp.MustCompile(`^/itm/(?:[^/]+/)?(\d{9,15})/?$`)

func atributo(n *html.Node, nombre string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == nombre {
			return a.Val, true
		}
	}
	return "", false
}

func fijarAtributo(n *html.Node, nombre, valor string) {
	for i, a := range n.Attr {
		if a.Key == nombre {
			n.Attr[i].Val = valor
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: nombre, Val: valor})
}

func esDeEbay(host string) bool {
	return strings.HasPrefix(host, "ebay.") || strings.Contains(host, ".ebay.")
}

// enlaceInterno convierte el enlace a un anuncio de eBay en su ficha propia.
// Cualquier otro enlace se deja como está.
func enlaceInterno(href string) string {
	u, err := url.Parse(href)
	if err != nil || !esDeEbay(u.Hostname()) {
		return href
	}
	m := patronRutaAnuncio.FindStringSubmatch(u.Path)
	if m == nil {
		return href
	}
	interno := "/itm/" + m[1]
	if v := u.Query().Get("var"); soloDigitos(v) {
		interno += "?var=" + v
	}
	return interno
}

// depurar recorre el árbol eliminando y reescribiendo nodos.
func depurar(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		siguiente := c.NextSibling
		if c.Type == html.CommentNode {
			n.RemoveChild(c)
		} else if c.Type == html.ElementNode {
			depurarElemento(n, c)
		}
		c = siguiente
	}
}

func depurarElemento(padre, c *html.Node) {
	estilo, _ := atributo(c, "style")
	if elementosDescartados[c.DataAtom] || patronOculto.MatchString(estilo) {
		padre.RemoveChild(c)
		return
	}

	// Las clases del vendedor no significan nada aquí: solo quedan las que
	// salen de sus estilos.
	quitarAtributo(c, "style")
	quitarAtributo(c, "class")
	if clases := clasesDeEstilo(estilo); len(clases) > 0 {
		fijarAtributo(c, "class", strings.Join(clases, " "))
	}

	switch c.DataAtom {
	case atom.Img:
		// Las fotos de eBay van por su proxy y las demás por el de imágenes
		// externas. Lo que no sea una dirección web válida se elimina.
		src, _ := atributo(c, "src")
		src = strings.TrimSpace(src)
		ruta := rutaImagen(src, 0)
		if ruta == "" {
			ruta = rutaImagenExterna(src)
		}
		if ruta == "" {
			padre.RemoveChild(c)
			return
		}
		fijarAtributo(c, "src", ruta)
		fijarAtributo(c, "loading", "lazy")
		// srcset apuntaría directamente al servidor original.
		quitarAtributo(c, "srcset")
	case atom.A:
		if href, ok := atributo(c, "href"); ok {
			fijarAtributo(c, "href", enlaceInterno(strings.TrimSpace(href)))
		}
		depurar(c)
	default:
		depurar(c)
	}
}

var (
	alineaciones = regexp.MustCompile(`(?i)^(left|right|center|justify)$`)
	politica     = nuevaPolitica()
)

func nuevaPolitica() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// Etiquetas antiguas que siguen muy presentes en las descripciones. Se
	// conservan sin atributos: font sin size ni color, solo como contenedor.
	p.AllowElements("center", "font", "span", "div", "u")
	p.AllowAttrs("align").Matching(alineaciones).OnElements("p", "div", "td", "th", "h1", "h2", "h3", "h4", "h5", "h6")
	// Al depurar se quitan todas las clases del vendedor; las únicas que
	// quedan son las propias, traducidas de sus estilos.
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^d-[a-z-]+( d-[a-z-]+)*$`)).Globally()
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^lazy$`)).OnElements("img")

	// Los enlaces externos se abren aparte y sin enviar la página de origen.
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}

// sanearDescripcion devuelve la descripción lista para insertarla en la
// página, o "" si después de limpiarla no queda nada visible.
func sanearDescripcion(crudo string) template.HTML {
	if strings.TrimSpace(crudo) == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(crudo))
	if err != nil {
		return ""
	}
	depurar(doc)

	var buf bytes.Buffer
	var cuerpo func(*html.Node) *html.Node
	cuerpo = func(n *html.Node) *html.Node {
		if n.Type == html.ElementNode && n.DataAtom == atom.Body {
			return n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if b := cuerpo(c); b != nil {
				return b
			}
		}
		return nil
	}
	if b := cuerpo(doc); b != nil {
		for c := b.FirstChild; c != nil; c = c.NextSibling {
			if err := html.Render(&buf, c); err != nil {
				return ""
			}
		}
	}

	limpio := strings.TrimSpace(politica.Sanitize(buf.String()))
	if limpio == "" {
		return ""
	}
	return template.HTML(limpio)
}
