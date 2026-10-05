package main

import (
	"sync"
	"time"
)

// Las respuestas se guardan unos minutos en memoria. Repetir una búsqueda o
// volver atrás en el navegador no gasta cupo de la API. La licencia de eBay
// exige que los anuncios mostrados no vayan más de seis horas por detrás del
// sitio; diez minutos quedan muy lejos de ese límite.
const (
	ttlCache         = 10 * time.Minute
	maxEntradasCache = 256
)

type entradaCache[T any] struct {
	valor  T
	expira time.Time
}

type cache[T any] struct {
	mu       sync.Mutex
	entradas map[string]entradaCache[T]
}

func nuevaCache[T any]() *cache[T] {
	return &cache[T]{entradas: map[string]entradaCache[T]{}}
}

func (c *cache[T]) obtener(clave string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entradas[clave]
	if !ok || time.Now().After(e.expira) {
		delete(c.entradas, clave)
		var cero T
		return cero, false
	}
	return e.valor, true
}

func (c *cache[T]) guardar(clave string, valor T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entradas) >= maxEntradasCache {
		ahora := time.Now()
		for k, e := range c.entradas {
			if ahora.After(e.expira) {
				delete(c.entradas, k)
			}
		}
		// Si no había nada caducado se vacía entera: acotar la memoria
		// importa más que conservar entradas concretas.
		if len(c.entradas) >= maxEntradasCache {
			c.entradas = map[string]entradaCache[T]{}
		}
	}
	c.entradas[clave] = entradaCache[T]{valor: valor, expira: time.Now().Add(ttlCache)}
}
