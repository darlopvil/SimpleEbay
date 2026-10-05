package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// La API da los precios en la moneda del marketplace, más el original del
// vendedor cuando es otra, y no tiene forma de pedirlos en una moneda
// concreta. Para mostrarlo todo en euros se usan los tipos de referencia que
// publica a diario el Banco Central Europeo. Se descargan en segundo plano y
// se guardan en memoria: convertir un precio es una multiplicación y nunca
// retrasa una página. Si aún no se tienen, los precios salen en la moneda del
// marketplace, como sin conversión.

const urlTiposBCE = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"

// El BCE publica una vez al día, hacia las 16:00 CET.
const (
	refrescoTipos   = 6 * time.Hour
	reintentoTipos  = 15 * time.Minute
	monedaMostrada  = "EUR"
	maxRespuestaBCE = 1 << 20
)

type tiposCambio struct {
	mu    sync.RWMutex
	tasas map[string]float64 // unidades de cada moneda por euro
	fecha string
}

var cambio = &tiposCambio{}

// El XML del BCE anida tres niveles de <Cube>: el contenedor, el del día con
// su fecha y uno por moneda.
type xmlBCE struct {
	Cubo struct {
		Dia struct {
			Fecha   string `xml:"time,attr"`
			Monedas []struct {
				Moneda string `xml:"currency,attr"`
				Tasa   string `xml:"rate,attr"`
			} `xml:"Cube"`
		} `xml:"Cube"`
	} `xml:"Cube"`
}

func (t *tiposCambio) actualizar(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlTiposBCE, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("el BCE respondió %d", resp.StatusCode)
	}
	datos, err := io.ReadAll(io.LimitReader(resp.Body, maxRespuestaBCE))
	if err != nil {
		return err
	}

	var doc xmlBCE
	if err := xml.Unmarshal(datos, &doc); err != nil {
		return fmt.Errorf("XML del BCE ilegible: %w", err)
	}
	tasas := map[string]float64{"EUR": 1}
	for _, m := range doc.Cubo.Dia.Monedas {
		if tasa, err := strconv.ParseFloat(m.Tasa, 64); err == nil && tasa > 0 {
			tasas[m.Moneda] = tasa
		}
	}
	if len(tasas) < 2 {
		return errors.New("el BCE no devolvió ningún tipo de cambio")
	}

	t.mu.Lock()
	t.tasas, t.fecha = tasas, doc.Cubo.Dia.Fecha
	t.mu.Unlock()
	return nil
}

// mantenerTiposCambio descarga los tipos al arrancar y los renueva cada
// pocas horas. Si falla, reintenta antes.
func mantenerTiposCambio() {
	for {
		ctx, cancelar := context.WithTimeout(context.Background(), 30*time.Second)
		err := cambio.actualizar(ctx)
		cancelar()
		espera := refrescoTipos
		if err != nil {
			log.Printf("tipos de cambio: %v", err)
			espera = reintentoTipos
		} else {
			cambio.mu.RLock()
			log.Printf("tipos de cambio del BCE del %s cargados", cambio.fecha)
			cambio.mu.RUnlock()
		}
		time.Sleep(espera)
	}
}

// aEuros convierte un importe con los tipos del BCE.
func (t *tiposCambio) aEuros(valor float64, moneda string) (float64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	tasa, ok := t.tasas[moneda]
	if !ok {
		return 0, false
	}
	return valor / tasa, true
}

func formatoEuros(valor float64) string {
	return formatoDinero(strconv.FormatFloat(valor, 'f', 2, 64), monedaMostrada)
}

// Importes de la API, que se usan en las tarjetas y en la ficha.

type importeAPI struct {
	Valor        string `json:"value"`
	Moneda       string `json:"currency"`
	ValorOrigen  string `json:"convertedFromValue"`
	MonedaOrigen string `json:"convertedFromCurrency"`
}

// euros devuelve el importe en euros y si es exacto o una estimación. Es
// exacto si eBay ya lo da en euros, porque el marketplace o el vendedor usan
// esa moneda; si no, se convierte el original del vendedor con los tipos del
// BCE.
func (i *importeAPI) euros() (valor float64, exacto bool, ok bool) {
	if i == nil {
		return 0, false, false
	}
	if i.Moneda == monedaMostrada {
		v, err := strconv.ParseFloat(i.Valor, 64)
		return v, true, err == nil
	}
	if i.MonedaOrigen == monedaMostrada {
		v, err := strconv.ParseFloat(i.ValorOrigen, 64)
		return v, true, err == nil
	}
	valorOriginal, monedaOriginal := i.Valor, i.Moneda
	if i.ValorOrigen != "" {
		valorOriginal, monedaOriginal = i.ValorOrigen, i.MonedaOrigen
	}
	v, err := strconv.ParseFloat(valorOriginal, 64)
	if err != nil {
		return 0, false, false
	}
	convertido, ok := cambio.aEuros(v, monedaOriginal)
	return convertido, false, ok
}

// texto es el importe que se muestra en grande: en euros, con "≈" si es una
// estimación. Sin tipos de cambio disponibles, en la moneda del marketplace.
func (i *importeAPI) texto() string {
	if i == nil {
		return ""
	}
	valor, exacto, ok := i.euros()
	switch {
	case !ok:
		return formatoDinero(i.Valor, i.Moneda)
	case exacto:
		return formatoEuros(valor)
	default:
		return "≈ " + formatoEuros(valor)
	}
}

// textoOrigen acompaña en pequeño al importe en euros: lo que marca el
// marketplace si no es en euros, y lo que pidió el vendedor si usa otra
// moneda.
func (i *importeAPI) textoOrigen() string {
	if i == nil {
		return ""
	}
	_, _, ok := i.euros()
	var partes []string
	if ok && i.Moneda != monedaMostrada {
		partes = append(partes, formatoDinero(i.Valor, i.Moneda))
	}
	if i.ValorOrigen != "" && i.MonedaOrigen != monedaMostrada {
		partes = append(partes, formatoDinero(i.ValorOrigen, i.MonedaOrigen)+" del vendedor")
	}
	return unirNoVacios(partes, " · ")
}
