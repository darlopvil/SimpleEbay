package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	urlToken = "https://api.ebay.com/identity/v1/oauth2/token"
	urlAPI   = "https://api.ebay.com"

	// Scope del flujo client credentials. Es el único que necesita la Browse
	// API para buscar y leer fichas: no hay usuario de eBay de por medio.
	scopeAplicacion = "https://api.ebay.com/oauth/api_scope"

	// El token se da por caducado un minuto antes de tiempo, para que una
	// petición no salga con un token que muere por el camino.
	margenToken = time.Minute

	// Una página de resultados con 200 artículos ronda el medio mega. El
	// límite cubre de sobra las respuestas legítimas y evita que una anómala
	// agote la memoria del proceso.
	maxRespuesta = 8 << 20
)

// errorEbay recoge los errores con el formato estándar de las APIs REST de
// eBay. Se conserva el errorId porque algunos llevan instrucciones útiles: el
// 11006, por ejemplo, avisa de que un ID heredado pertenece a un grupo de
// variaciones.
type errorEbay struct {
	Estado  int
	Errores []detalleErrorEbay
}

type detalleErrorEbay struct {
	ErrorID    int    `json:"errorId"`
	Mensaje    string `json:"message"`
	Parametros []struct {
		Nombre string `json:"name"`
		Valor  string `json:"value"`
	} `json:"parameters"`
}

func (e *errorEbay) Error() string {
	if len(e.Errores) == 0 {
		return fmt.Sprintf("eBay respondió %d", e.Estado)
	}
	return fmt.Sprintf("eBay respondió %d: error %d: %s", e.Estado, e.Errores[0].ErrorID, e.Errores[0].Mensaje)
}

// tieneError indica si la respuesta incluye un errorId concreto.
func (e *errorEbay) tieneError(id int) bool {
	for _, d := range e.Errores {
		if d.ErrorID == id {
			return true
		}
	}
	return false
}

type clienteEbay struct {
	id      string
	secreto string
	http    *http.Client

	// Cabecera X-EBAY-C-ENDUSERCTX ya codificada. Sin ella la API no calcula
	// costes de envío, tasas de importación ni fechas de entrega.
	contexto string

	mu     sync.Mutex
	token  string
	caduca time.Time
}

func nuevoClienteEbay(id, secreto, pais, cp string) (*clienteEbay, error) {
	if id == "" || secreto == "" {
		return nil, errors.New("faltan EBAY_CLIENT_ID o EBAY_CLIENT_SECRET")
	}
	c := &clienteEbay{
		id:      id,
		secreto: secreto,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
	if pais != "" {
		ubicacion := "country=" + strings.ToUpper(pais)
		if cp != "" {
			ubicacion += ",zip=" + cp
		}
		c.contexto = "contextualLocation=" + url.QueryEscape(ubicacion)
	}
	return c, nil
}

// tokenVigente devuelve el token en caché o pide uno nuevo. El cerrojo se
// mantiene durante la petición a propósito: si varias peticiones encuentran
// el token caducado a la vez, solo una lo renueva y las demás lo reutilizan.
func (c *clienteEbay) tokenVigente(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.caduca) {
		return c.token, nil
	}

	cuerpo := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {scopeAplicacion},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlToken, strings.NewReader(cuerpo))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.id, c.secreto)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("petición del token: %w", err)
	}
	defer resp.Body.Close()
	datos, err := io.ReadAll(io.LimitReader(resp.Body, maxRespuesta))
	if err != nil {
		return "", fmt.Errorf("lectura del token: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// El servidor de OAuth no usa el formato de errores de la API, sino
		// el de la especificación de OAuth.
		var e struct {
			Error       string `json:"error"`
			Descripcion string `json:"error_description"`
		}
		if json.Unmarshal(datos, &e) == nil && e.Error != "" {
			return "", fmt.Errorf("token rechazado (%d): %s: %s", resp.StatusCode, e.Error, e.Descripcion)
		}
		return "", fmt.Errorf("token rechazado (%d)", resp.StatusCode)
	}

	var t struct {
		Token    string `json:"access_token"`
		ExpiraEn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(datos, &t); err != nil {
		return "", fmt.Errorf("token ilegible: %w", err)
	}
	if t.Token == "" || t.ExpiraEn <= 0 {
		return "", errors.New("respuesta de token incompleta")
	}

	c.token = t.Token
	c.caduca = time.Now().Add(time.Duration(t.ExpiraEn)*time.Second - margenToken)
	return c.token, nil
}

// comprobarToken fuerza la obtención del token y devuelve su caducidad.
func (c *clienteEbay) comprobarToken(ctx context.Context) (time.Time, error) {
	if _, err := c.tokenVigente(ctx); err != nil {
		return time.Time{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caduca.Add(margenToken), nil
}

// invalidarToken descarta el token en caché, para el caso en que eBay lo
// rechace antes de su caducidad teórica.
func (c *clienteEbay) invalidarToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// peticion hace un GET a la API y decodifica la respuesta en destino. Si
// eBay rechaza el token con un 401, se renueva y se reintenta una sola vez.
func (c *clienteEbay) peticion(ctx context.Context, marketplace, ruta string, consulta url.Values, destino any) error {
	return c.llamar(ctx, http.MethodGet, marketplace, ruta, consulta, nil, destino)
}

// peticionJSON es lo mismo con un POST y un cuerpo JSON ya serializado.
func (c *clienteEbay) peticionJSON(ctx context.Context, marketplace, ruta string, consulta url.Values, cuerpo []byte, destino any) error {
	return c.llamar(ctx, http.MethodPost, marketplace, ruta, consulta, cuerpo, destino)
}

func (c *clienteEbay) llamar(ctx context.Context, metodo, marketplace, ruta string, consulta url.Values, cuerpo []byte, destino any) error {
	for intento := 0; ; intento++ {
		token, err := c.tokenVigente(ctx)
		if err != nil {
			return err
		}

		direccion := urlAPI + ruta
		if len(consulta) > 0 {
			direccion += "?" + consulta.Encode()
		}
		// El lector se crea en cada intento: tras un 401 hay que reenviar el
		// cuerpo entero.
		var lector io.Reader
		if cuerpo != nil {
			lector = bytes.NewReader(cuerpo)
		}
		req, err := http.NewRequestWithContext(ctx, metodo, direccion, lector)
		if err != nil {
			return err
		}
		if cuerpo != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-EBAY-C-MARKETPLACE-ID", marketplace)
		if c.contexto != "" {
			req.Header.Set("X-EBAY-C-ENDUSERCTX", c.contexto)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("petición a eBay: %w", err)
		}
		datos, err := io.ReadAll(io.LimitReader(resp.Body, maxRespuesta))
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("lectura de la respuesta: %w", err)
		}

		if resp.StatusCode == http.StatusUnauthorized && intento == 0 {
			c.invalidarToken()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			e := &errorEbay{Estado: resp.StatusCode}
			var cuerpo struct {
				Errores []detalleErrorEbay `json:"errors"`
			}
			if json.Unmarshal(datos, &cuerpo) == nil {
				e.Errores = cuerpo.Errores
			}
			return e
		}
		if err := json.Unmarshal(datos, destino); err != nil {
			return fmt.Errorf("respuesta ilegible: %w", err)
		}
		return nil
	}
}
