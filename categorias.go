package main

import (
	"context"
	"log"
	"net/url"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"sync"
	"time"
)

// El desglose por categorías de una búsqueda llega como una lista plana en la
// que nada distingue una categoría raíz de sus hijas, y dentro de una
// categoría no se dice cuál es su madre. Esa estructura sale del árbol de
// categorías de la Taxonomy API, que tiene su propio cupo: se descarga entero
// (de 2,7 MB en EBAY_ES a 4,3 MB en EBAY_US, entre 10.000 y 17.000
// categorías) y se guarda como una tabla id → (nombre, madre).
const (
	// El árbol cambia muy poco: basta con renovarlo una vez al día.
	ttlArbol = 24 * time.Hour

	// Tras un fallo no se reintenta en cada página, sino pasado este rato.
	esperaTrasFallo = 10 * time.Minute

	// Guardado en compacto, cada árbol ocupa en torno a un mega y medio; la
	// descarga es lo que pesa (la respuesta entera más su decodificación).
	// Con este tope y una sola descarga a la vez, el proceso cabe en los
	// 64 MB del contenedor: medido con seis árboles de 5,4 MB y 17.000
	// categorías, como el de EBAY_US.
	maxArboles = 4

	// Identificador del nodo raíz del árbol, que no es una categoría real.
	raizArbol = "0"

	// Categorías que se ven de entrada; el resto queda plegado.
	categoriasVisibles = 12
)

var patronCategoria = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

type categoria struct {
	ID     string
	Nombre string
	Madre  string
}

// Los ID de categoría son numéricos: guardarlos como enteros reduce a la
// mitad lo que ocupa el árbol en memoria.
type nodoCategoria struct {
	nombre string
	madre  uint32
}

type arbolCategorias struct {
	nodos   map[uint32]nodoCategoria
	raices  []categoria // en el orden de eBay
	cargado time.Time
}

func idCategoria(id string) (uint32, bool) {
	n, err := strconv.ParseUint(id, 10, 32)
	return uint32(n), err == nil
}

// buscar devuelve una categoría del árbol por su ID.
func (a *arbolCategorias) buscar(id string) (categoria, bool) {
	n, ok := idCategoria(id)
	if !ok {
		return categoria{}, false
	}
	nodo, ok := a.nodos[n]
	if !ok {
		return categoria{}, false
	}
	return categoria{ID: id, Nombre: nodo.nombre, Madre: strconv.FormatUint(uint64(nodo.madre), 10)}, true
}

// ruta devuelve los antepasados de una categoría, de la raíz a ella misma.
func (a *arbolCategorias) ruta(id string) []categoria {
	var ruta []categoria
	// El tope evita un bucle infinito si el árbol llegase corrupto.
	for i := 0; i < 16 && id != raizArbol; i++ {
		c, ok := a.buscar(id)
		if !ok {
			break
		}
		ruta = append([]categoria{c}, ruta...)
		id = c.Madre
	}
	return ruta
}

type almacenArboles struct {
	ebay *clienteEbay

	mu       sync.Mutex
	arboles  map[string]*arbolCategorias // por marketplace
	cargando map[string]bool
	fallo    map[string]time.Time

	// Solo una descarga a la vez, para acotar el pico de memoria.
	descarga sync.Mutex
}

var arboles = &almacenArboles{
	arboles:  map[string]*arbolCategorias{},
	cargando: map[string]bool{},
	fallo:    map[string]time.Time{},
}

// obtener devuelve el árbol del marketplace sin esperar nunca. Si no está
// cargado, o está caducado, pide la descarga en segundo plano y devuelve lo
// que haya, que puede ser nil: la página sale sin el bloque de categorías y
// lo tendrá en la siguiente visita.
func (a *almacenArboles) obtener(marketplace string) *arbolCategorias {
	a.mu.Lock()
	defer a.mu.Unlock()
	arbol := a.arboles[marketplace]
	caducado := arbol == nil || time.Since(arbol.cargado) > ttlArbol
	enEspera := time.Since(a.fallo[marketplace]) < esperaTrasFallo
	if caducado && a.ebay != nil && !a.cargando[marketplace] && !enEspera {
		a.cargando[marketplace] = true
		go a.cargar(marketplace)
	}
	return arbol
}

func (a *almacenArboles) cargar(marketplace string) {
	a.descarga.Lock()
	defer a.descarga.Unlock()

	ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
	defer cancelar()
	arbol, err := descargarArbol(ctx, a.ebay, marketplace)

	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.cargando, marketplace)
	if err != nil {
		// Se conserva el árbol anterior, si lo había: caducado sirve igual.
		a.fallo[marketplace] = time.Now()
		log.Printf("categorías de %s: %v", marketplace, err)
		return
	}
	if _, ya := a.arboles[marketplace]; !ya && len(a.arboles) >= maxArboles {
		var viejo string
		for mp, ar := range a.arboles {
			if viejo == "" || ar.cargado.Before(a.arboles[viejo].cargado) {
				viejo = mp
			}
		}
		delete(a.arboles, viejo)
	}
	a.arboles[marketplace] = arbol
	log.Printf("categorías de %s: %d cargadas", marketplace, len(arbol.nodos))
	// La respuesta y su decodificación ya son basura: se devuelve la memoria
	// al sistema en vez de esperar al recolector. Pasa una vez al día.
	debug.FreeOSMemory()
}

// Respuesta de la Taxonomy API, solo con los campos que se usan.
type nodoArbolAPI struct {
	Categoria struct {
		ID     string `json:"categoryId"`
		Nombre string `json:"categoryName"`
	} `json:"category"`
	Hijas []nodoArbolAPI `json:"childCategoryTreeNodes"`
}

func descargarArbol(ctx context.Context, ebay *clienteEbay, marketplace string) (*arbolCategorias, error) {
	var id struct {
		ID string `json:"categoryTreeId"`
	}
	if err := ebay.peticion(ctx, marketplace, "/commerce/taxonomy/v1/get_default_category_tree_id",
		url.Values{"marketplace_id": {marketplace}}, &id); err != nil {
		return nil, err
	}
	var respuesta struct {
		Raiz nodoArbolAPI `json:"rootCategoryNode"`
	}
	if err := ebay.peticion(ctx, marketplace, "/commerce/taxonomy/v1/category_tree/"+url.PathEscape(id.ID), nil, &respuesta); err != nil {
		return nil, err
	}

	arbol := &arbolCategorias{nodos: make(map[uint32]nodoCategoria, 1<<14), cargado: time.Now()}
	var recorrer func(n *nodoArbolAPI, madre uint32)
	recorrer = func(n *nodoArbolAPI, madre uint32) {
		for i := range n.Hijas {
			h := &n.Hijas[i]
			id, ok := idCategoria(h.Categoria.ID)
			if !ok {
				continue
			}
			arbol.nodos[id] = nodoCategoria{nombre: h.Categoria.Nombre, madre: madre}
			if madre == 0 {
				arbol.raices = append(arbol.raices, categoria{ID: h.Categoria.ID, Nombre: h.Categoria.Nombre, Madre: raizArbol})
			}
			recorrer(h, id)
		}
	}
	recorrer(&respuesta.Raiz, 0)
	return arbol, nil
}

// Desglose por categorías que acompaña a la búsqueda cuando se pide
// fieldgroups=CATEGORY_REFINEMENTS. Sin categoría elegida trae cada raíz
// seguida de sus hijas; dentro de una categoría, ella misma sin recuento y
// después sus hijas. Los recuentos son aproximados.
type desgloseAPI struct {
	Categorias []struct {
		ID     string `json:"categoryId"`
		Nombre string `json:"categoryName"`
		Cuenta int    `json:"matchCount"`
	} `json:"categoryDistributions"`

	// Con ASPECT_REFINEMENTS: los aspectos de la categoría dominante, que
	// llega incluso sin categoría elegida.
	Dominante string       `json:"dominantCategoryId"`
	Aspectos  []aspectoAPI `json:"aspectDistributions"`
}

type enlaceCategoria struct {
	Nombre string
	Enlace string
	Cuenta int
}

// conCategoria devuelve el enlace a esta búsqueda en otra categoría ("" para
// quitarla). Sin consulta ni vendedor, quitar la categoría deja la búsqueda
// vacía, así que se vuelve a la portada.
func (f filtrosBusqueda) conCategoria(id string) string {
	f.Categoria = id
	// Cada categoría tiene sus propias características.
	f.Aspectos = nil
	if f.Categoria == "" && f.Consulta == "" && f.Vendedor == "" && f.Epid == "" {
		return "/"
	}
	return f.enlace(1)
}

// raicesPortada enlaza las categorías raíz del marketplace elegido, para
// explorar desde la portada sin buscar nada. No gasta cupo de la Browse API.
func raicesPortada(m marketplace) []enlaceCategoria {
	arbol := arboles.obtener(m.ID)
	if arbol == nil {
		return nil
	}
	enlaces := make([]enlaceCategoria, 0, len(arbol.raices))
	for _, c := range arbol.raices {
		v := url.Values{"categoria": {c.ID}, "mp": {m.ID}}
		enlaces = append(enlaces, enlaceCategoria{Nombre: c.Nombre, Enlace: "/s?" + v.Encode()})
	}
	return enlaces
}

// navegacionCategorias prepara la ruta hasta la categoría actual y las
// categorías a las que se puede bajar, ordenadas por número de resultados.
func navegacionCategorias(f filtrosBusqueda, d *desgloseAPI, arbol *arbolCategorias) (ruta, opciones []enlaceCategoria) {
	if f.Categoria != "" {
		ruta = append(ruta, enlaceCategoria{Nombre: "Todas las categorías", Enlace: f.conCategoria("")})
		var antepasados []categoria
		if arbol != nil {
			antepasados = arbol.ruta(f.Categoria)
		}
		if len(antepasados) == 0 {
			// Sin árbol, el nombre de la actual sale del propio desglose.
			nombre := "Categoría " + f.Categoria
			if d != nil {
				for _, c := range d.Categorias {
					if c.ID == f.Categoria {
						nombre = c.Nombre
					}
				}
			}
			antepasados = []categoria{{ID: f.Categoria, Nombre: nombre}}
		}
		for _, c := range antepasados {
			e := enlaceCategoria{Nombre: c.Nombre}
			if c.ID != f.Categoria {
				e.Enlace = f.conCategoria(c.ID)
			}
			ruta = append(ruta, e)
		}
	}

	if d == nil {
		return ruta, nil
	}
	for _, c := range d.Categorias {
		if c.Cuenta <= 0 || c.ID == f.Categoria {
			continue
		}
		madre := f.Categoria
		if madre == "" {
			madre = raizArbol
		}
		if arbol != nil {
			if n, ok := arbol.buscar(c.ID); !ok || n.Madre != madre {
				continue
			}
		} else if f.Categoria == "" {
			// Sin árbol no se sabe cuáles son raíces.
			continue
		}
		opciones = append(opciones, enlaceCategoria{Nombre: c.Nombre, Enlace: f.conCategoria(c.ID), Cuenta: c.Cuenta})
	}
	sort.SliceStable(opciones, func(i, j int) bool { return opciones[i].Cuenta > opciones[j].Cuenta })
	return ruta, opciones
}
