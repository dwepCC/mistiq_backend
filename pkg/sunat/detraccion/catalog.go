package detraccion

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// GoodEntry catálogo SUNAT 54 — bien o servicio sujeto a detracción.
type GoodEntry struct {
	Code           string  `json:"code"`
	Description    string  `json:"description"`
	RatePercent    float64 `json:"rate_percent"`
	MinAmountPEN   float64 `json:"min_amount_pen"`
	TransportCargo bool    `json:"transport_cargo"`
	Active         bool    `json:"active"`
}

// PaymentMethodEntry catálogo SUNAT 59 — medio de pago detracción.
type PaymentMethodEntry struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	Active      bool   `json:"active"`
}

// CatalogProvider carga catálogos parametrizables (JSON embebido o inyectado en tests).
type CatalogProvider struct {
	goods          []GoodEntry
	paymentMethods []PaymentMethodEntry
}

var (
	defaultCatalog     *CatalogProvider
	defaultCatalogOnce sync.Once
	defaultCatalogErr  error
)

// DefaultCatalog devuelve el catálogo embebido (lazy, thread-safe).
func DefaultCatalog() (*CatalogProvider, error) {
	defaultCatalogOnce.Do(func() {
		defaultCatalog, defaultCatalogErr = LoadCatalog(goodsJSON, paymentMethodsJSON)
	})
	return defaultCatalog, defaultCatalogErr
}

// LoadCatalog parsea JSON de catálogos (permite reemplazar fuente sin recompilar lógica).
func LoadCatalog(goodsRaw, paymentRaw []byte) (*CatalogProvider, error) {
	var goods []GoodEntry
	if err := json.Unmarshal(goodsRaw, &goods); err != nil {
		return nil, fmt.Errorf("catálogo detracción bienes: %w", err)
	}
	var methods []PaymentMethodEntry
	if err := json.Unmarshal(paymentRaw, &methods); err != nil {
		return nil, fmt.Errorf("catálogo detracción medios de pago: %w", err)
	}
	return &CatalogProvider{goods: goods, paymentMethods: methods}, nil
}

// ListGoods devuelve bienes activos; excludeTransport excluye código 027 (reservado 1004).
func (c *CatalogProvider) ListGoods(excludeTransport bool) []GoodEntry {
	if c == nil {
		return nil
	}
	out := make([]GoodEntry, 0, len(c.goods))
	for _, g := range c.goods {
		if !g.Active {
			continue
		}
		if excludeTransport && g.TransportCargo {
			continue
		}
		out = append(out, g)
	}
	return out
}

// ListPaymentMethods devuelve medios de pago activos (cat. 59).
func (c *CatalogProvider) ListPaymentMethods() []PaymentMethodEntry {
	if c == nil {
		return nil
	}
	out := make([]PaymentMethodEntry, 0, len(c.paymentMethods))
	for _, m := range c.paymentMethods {
		if m.Active {
			out = append(out, m)
		}
	}
	return out
}

// GoodByCode busca un bien por código cat. 54.
func (c *CatalogProvider) GoodByCode(code string) (*GoodEntry, bool) {
	if c == nil {
		return nil, false
	}
	code = strings.TrimSpace(code)
	for i := range c.goods {
		if c.goods[i].Code == code && c.goods[i].Active {
			return &c.goods[i], true
		}
	}
	return nil, false
}

// PaymentMethodByCode busca medio de pago cat. 59.
func (c *CatalogProvider) PaymentMethodByCode(code string) (*PaymentMethodEntry, bool) {
	if c == nil {
		return nil, false
	}
	code = strings.TrimSpace(code)
	for i := range c.paymentMethods {
		if c.paymentMethods[i].Code == code && c.paymentMethods[i].Active {
			return &c.paymentMethods[i], true
		}
	}
	return nil, false
}
