package exchangerate

import "time"

const (
	StatusConfirmed   = "confirmed"
	StatusFallback    = "fallback"
	StatusPending     = "pending"
	StatusUnavailable = "unavailable"

	SourceApiPeru          = "apiperu"
	SourceFallbackPrevious = "fallback_previous"
	SourceManual           = "manual"
	SourceCacheCentral     = "cache_central"

	cooldownDuration = time.Hour
	lockTTL          = 30 * time.Second
	waitForPeerMax   = 5 * time.Second
	waitForPeerStep  = 200 * time.Millisecond
)

// ProviderResult respuesta cruda del proveedor externo.
type ProviderResult struct {
	Fecha  string
	Moneda string
	Venta  float64
	Compra float64
	Error  string
	OK     bool
}

// QueryResult respuesta API compatible con frontend existente + metadatos de cache.
type QueryResult struct {
	Success         bool    `json:"success"`
	Fecha           string  `json:"fecha"`
	FechaEfectiva   string  `json:"fecha_efectiva,omitempty"`
	Moneda          string  `json:"moneda,omitempty"`
	Venta           float64 `json:"venta"`
	Compra          float64 `json:"compra"`
	Fuente          string  `json:"fuente,omitempty"`
	Status          string  `json:"status,omitempty"`
	EsFallback      bool    `json:"es_fallback,omitempty"`
	ProximoReintento *string `json:"proximo_reintento,omitempty"`
	Mensaje         string  `json:"mensaje,omitempty"`
	ErrorMessage    string  `json:"error_message,omitempty"`
}
