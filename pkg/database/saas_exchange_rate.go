package database

import "time"

const (
	ExchangeRateStatusConfirmed   = "confirmed"
	ExchangeRateStatusFallback    = "fallback"
	ExchangeRateStatusPending     = "pending"
	ExchangeRateStatusUnavailable = "unavailable"

	ExchangeRateSourceApiPeru          = "apiperu"
	ExchangeRateSourceFallbackPrevious = "fallback_previous"
	ExchangeRateSourceManual           = "manual"
)

// SaasExchangeRate tipo de cambio SUNAT cacheado en BD central (una fila por fecha calendario Lima).
type SaasExchangeRate struct {
	RateDate      string     `gorm:"primaryKey;size:10" json:"rate_date"`
	BuyRate       float64    `gorm:"type:decimal(10,4);not null" json:"buy_rate"`
	SaleRate      float64    `gorm:"type:decimal(10,4);not null" json:"sale_rate"`
	Status        string     `gorm:"size:20;not null;index" json:"status"`
	Source        string     `gorm:"size:30;not null" json:"source"`
	EffectiveDate string     `gorm:"size:10;not null;index" json:"effective_date"`
	FetchedAt     *time.Time `json:"fetched_at,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	NextRetryAt   *time.Time `json:"next_retry_at,omitempty"`
	AttemptCount  int        `gorm:"default:0" json:"attempt_count"`
	ErrorMessage  string     `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (SaasExchangeRate) TableName() string { return "saas_exchange_rates" }
