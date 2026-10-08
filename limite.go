package main

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Límite de peticiones por IP para lo que gasta cupo de la API. El cupo es
// diario y compartido: un script de búsquedas podría agotarlo en minutos y
// dejar la instancia sin servicio hasta el día siguiente. Las fotos de los
// anuncios (/img/, /ext) y los estáticos no gastan cupo y no se limitan aquí.
//
// Cubeta de fichas por IP y por clase: cada petición gasta una ficha y las
// fichas se reponen a ritmo constante. La capacidad es la ráfaga permitida.
type claseLimite struct {
	nombre    string
	capacidad float64
	porMinuto float64
}

var (
	// Una persona navegando no se acerca: pasar de página dentro de un
	// bloque, volver atrás o abrir fichas en pestañas cabe de sobra.
	limiteConsultas = claseLimite{"consultas", 30, 30}

	// Cada foto son hasta 5 MB de subida y una llamada a la API.
	limiteFotos = claseLimite{"fotos", 5, 5}
)

const (
	// Las IP que llevan este tiempo sin pedir nada se olvidan.
	olvidoLimite = 10 * time.Minute

	// Tope de IP recordadas, para que una avalancha de direcciones
	// distintas no haga crecer la tabla sin fin.
	maxIPsLimite = 10000
)

type cubeta struct {
	fichas float64
	ultima time.Time
}

type limitador struct {
	mu      sync.Mutex
	cubetas map[string]*cubeta
}

var limitadorIP = &limitador{cubetas: map[string]*cubeta{}}

// permitir descuenta una ficha si la hay. Si no, devuelve cuántos segundos
// faltan para la siguiente.
func (l *limitador) permitir(clase claseLimite, ip string, ahora time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	clave := clase.nombre + "|" + ip
	c, ok := l.cubetas[clave]
	if !ok {
		if len(l.cubetas) >= maxIPsLimite {
			l.limpiar(ahora)
		}
		c = &cubeta{fichas: clase.capacidad, ultima: ahora}
		l.cubetas[clave] = c
	}
	porSegundo := clase.porMinuto / 60
	c.fichas = math.Min(clase.capacidad, c.fichas+ahora.Sub(c.ultima).Seconds()*porSegundo)
	c.ultima = ahora
	if c.fichas >= 1 {
		c.fichas--
		return true, 0
	}
	return false, int(math.Ceil((1 - c.fichas) / porSegundo))
}

// limpiar olvida las IP inactivas y, si aun así no hay sitio, empieza de
// cero: acotar la memoria importa más que recordar a todo el mundo.
func (l *limitador) limpiar(ahora time.Time) {
	for clave, c := range l.cubetas {
		if ahora.Sub(c.ultima) > olvidoLimite {
			delete(l.cubetas, clave)
		}
	}
	if len(l.cubetas) >= maxIPsLimite {
		l.cubetas = map[string]*cubeta{}
	}
}

// ipCliente devuelve la IP de quien hace la petición. El contenedor no
// publica puertos: todo llega por el proxy inverso, que pone la IP real en
// X-Real-IP. Solo se hace caso a esa cabecera si la conexión viene de una
// dirección privada, es decir, del propio proxy; si alguien llegase directo,
// se usa su dirección de conexión.
func ipCliente(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	conexion := net.ParseIP(host)
	if conexion != nil && (conexion.IsPrivate() || conexion.IsLoopback()) {
		if real := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); real != nil {
			return real.String()
		}
	}
	return host
}

// limitar envuelve un manejador con el límite de una clase.
func limitar(clase claseLimite, siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, espera := limitadorIP.permitir(clase, ipCliente(r), time.Now())
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(espera))
			renderizar(w, http.StatusTooManyRequests, "error", datosPagina{
				Titulo:  "Demasiadas peticiones",
				Mensaje: "Has hecho muchas peticiones seguidas. Espera " + strconv.Itoa(espera) + " s y vuelve a intentarlo.",
			})
			return
		}
		siguiente.ServeHTTP(w, r)
	})
}
