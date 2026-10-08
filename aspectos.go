package main

import (
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// Filtros por características («aspectos» en la API): Plataforma, Región,
// Marca, Talla… Cada categoría tiene los suyos. Con
// fieldgroups=ASPECT_REFINEMENTS la búsqueda trae los de su categoría
// dominante con sus valores y recuentos, en la misma respuesta y sin alterar
// los resultados. Se filtra con aspect_filter=categoryId:C,Nombre:{v1|v2}:
// varios valores de un aspecto suman (O) y aspectos distintos se combinan
// (Y). Comprobado con «tomb raider» en Videojuegos: PS1 3.260, PAL 4.457,
// PS1 y PAL 1.194; «2013» 1.374 y «2013|2006» 2.352.
const (
	// Valores que se ofrecen por aspecto: los más frecuentes. «Nombre del
	// videojuego» llega con 379 valores; pintarlos todos haría la página
	// pesada sin servir de nada.
	valoresPorAspecto = 12

	// Tope de casillas marcadas, para que la URL y el filtro no crezcan sin
	// límite.
	maxAspectos = 30

	maxNombreAspecto = 64
	maxValorAspecto  = 100
)

type aspecto struct {
	Nombre string
	Valor  string
}

func (a aspecto) clave() string { return a.Nombre + ":" + a.Valor }

// La sintaxis de aspect_filter usa la coma, la barra y las llaves como
// separadores, y la API no documenta cómo escaparlos. Un valor que los
// contenga no se ofrece ni se acepta: si eBay no entiende un aspecto, lo
// ignora en silencio y la página mentiría diciendo que está filtrada.
func aspectoUtilizable(nombre, valor string) bool {
	return nombre != "" && valor != "" &&
		utf8.RuneCountInString(nombre) <= maxNombreAspecto &&
		utf8.RuneCountInString(valor) <= maxValorAspecto &&
		!strings.ContainsAny(nombre, ":,|{}") &&
		!strings.ContainsAny(valor, ",|{}")
}

// leerAspectos interpreta los parámetros asp=Nombre:Valor. El nombre va hasta
// los primeros dos puntos; el valor puede llevarlos («Tomb Raider: Legend»).
func leerAspectos(valores []string) []aspecto {
	var lista []aspecto
	vistos := map[string]bool{}
	for _, v := range valores {
		nombre, valor, ok := strings.Cut(strings.TrimSpace(v), ":")
		if !ok || !aspectoUtilizable(nombre, valor) {
			continue
		}
		a := aspecto{Nombre: nombre, Valor: valor}
		if vistos[a.clave()] {
			continue
		}
		vistos[a.clave()] = true
		lista = append(lista, a)
		if len(lista) == maxAspectos {
			break
		}
	}
	return lista
}

// filtroAspectos monta el aspect_filter, agrupando los valores de cada
// aspecto en el orden en que aparecen.
func filtroAspectos(categoria string, lista []aspecto) string {
	var orden []string
	valores := map[string][]string{}
	for _, a := range lista {
		if _, ok := valores[a.Nombre]; !ok {
			orden = append(orden, a.Nombre)
		}
		valores[a.Nombre] = append(valores[a.Nombre], a.Valor)
	}
	partes := []string{"categoryId:" + categoria}
	for _, n := range orden {
		partes = append(partes, n+":{"+strings.Join(valores[n], "|")+"}")
	}
	return strings.Join(partes, ",")
}

// Parte del desglose de la API con los aspectos.
type aspectoAPI struct {
	Nombre  string `json:"localizedAspectName"`
	Valores []struct {
		Valor  string `json:"localizedAspectValue"`
		Cuenta int    `json:"matchCount"`
	} `json:"aspectValueDistributions"`
}

// recortarAspectos deja en cada aspecto solo los valores que se pueden
// ofrecer y, de esos, los más frecuentes. Se hace antes de guardar la
// respuesta en caché: cientos de valores por búsqueda ocuparían memoria para
// nada.
func recortarAspectos(d *desgloseAPI) {
	if d == nil {
		return
	}
	for i := range d.Aspectos {
		a := &d.Aspectos[i]
		utiles := a.Valores[:0]
		for _, v := range a.Valores {
			if v.Cuenta > 0 && aspectoUtilizable(a.Nombre, v.Valor) {
				utiles = append(utiles, v)
			}
		}
		sort.SliceStable(utiles, func(x, y int) bool { return utiles[x].Cuenta > utiles[y].Cuenta })
		if len(utiles) > valoresPorAspecto {
			utiles = utiles[:valoresPorAspecto]
		}
		// Copia nueva: así el resto del array original queda libre.
		a.Valores = append(a.Valores[:0:0], utiles...)
	}
}

type valorAspecto struct {
	Valor   string
	Clave   string
	Cuenta  int
	Marcado bool
}

type grupoAspecto struct {
	Nombre  string
	Valores []valorAspecto
}

type campoOculto struct {
	Nombre string
	Valor  string
}

// panelAspectos es el formulario de características. Lleva el resto de la
// búsqueda en campos ocultos y su propia categoría: la dominante, que es la
// de los aspectos ofrecidos.
type panelAspectos struct {
	Categoria string
	Ocultos   []campoOculto
	Grupos    []grupoAspecto
	Resumen   string
	Marcados  int
}

// camposOcultos repite en el formulario de características los filtros que
// ya hay, salvo la categoría y los aspectos, que pone el propio formulario.
func (f filtrosBusqueda) camposOcultos() []campoOculto {
	var campos []campoOculto
	for _, c := range []campoOculto{
		{"q", f.Consulta}, {"vendedor", f.Vendedor}, {"mp", f.Marketplace.ID},
		{"orden", f.Orden}, {"estado", f.Estado}, {"compra", f.Compra},
		{"desde", f.Desde}, {"min", f.Min}, {"max", f.Max},
	} {
		if c.Valor != "" {
			campos = append(campos, c)
		}
	}
	return campos
}

func nuevoPanelAspectos(f filtrosBusqueda, d *desgloseAPI) *panelAspectos {
	if d == nil || len(d.Aspectos) == 0 || !patronCategoria.MatchString(d.Dominante) {
		return nil
	}
	marcado := map[string]bool{}
	for _, a := range f.Aspectos {
		marcado[a.clave()] = true
	}

	p := &panelAspectos{Categoria: d.Dominante, Ocultos: f.camposOcultos()}
	var nombres []string
	for _, a := range d.Aspectos {
		g := grupoAspecto{Nombre: a.Nombre}
		ofrecidos := map[string]bool{}
		for _, v := range a.Valores {
			va := valorAspecto{Valor: v.Valor, Clave: aspecto{a.Nombre, v.Valor}.clave(), Cuenta: v.Cuenta}
			va.Marcado = marcado[va.Clave]
			ofrecidos[va.Clave] = true
			g.Valores = append(g.Valores, va)
		}
		// Lo marcado se ofrece siempre, aunque no esté entre los más
		// frecuentes, para poder desmarcarlo.
		for _, m := range f.Aspectos {
			if m.Nombre == a.Nombre && !ofrecidos[m.clave()] {
				g.Valores = append([]valorAspecto{{Valor: m.Valor, Clave: m.clave(), Marcado: true}}, g.Valores...)
			}
		}
		if len(g.Valores) == 0 {
			continue
		}
		p.Grupos = append(p.Grupos, g)
		nombres = append(nombres, a.Nombre)
	}
	if len(p.Grupos) == 0 {
		return nil
	}
	p.Marcados = len(f.Aspectos)
	if len(nombres) > 3 {
		nombres = append(nombres[:3], "…")
	}
	p.Resumen = strings.Join(nombres, ", ")
	return p
}

// aspectosActivos enlaza cada característica aplicada con la misma búsqueda
// sin ella, para quitarla de un clic.
func aspectosActivos(f filtrosBusqueda) []enlaceCategoria {
	var activos []enlaceCategoria
	for i, a := range f.Aspectos {
		sin := f
		sin.Aspectos = append(append([]aspecto{}, f.Aspectos[:i]...), f.Aspectos[i+1:]...)
		activos = append(activos, enlaceCategoria{Nombre: a.Nombre + ": " + a.Valor, Enlace: sin.enlace(1)})
	}
	return activos
}

// valoresAspectos añade los aspectos a los parámetros de un enlace.
func valoresAspectos(v url.Values, lista []aspecto) {
	for _, a := range lista {
		v.Add("asp", a.clave())
	}
}
