package service

import (
	"errors"
	"strings"
	"time"

	"tukifac/pkg/database"
)

const productExpiryDateLayout = "2006-01-02"

// ParseProductExpiryDate interpreta YYYY-MM-DD en zona local; cadena vacía → nil.
func ParseProductExpiryDate(raw string) (*time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	t, err := time.ParseInLocation(productExpiryDateLayout, s, time.Local)
	if err != nil {
		return nil, errors.New("fecha de vencimiento inválida (use YYYY-MM-DD)")
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	return &day, nil
}

func normalizeProductExpiryFields(p *database.TenantProduct) {
	if p == nil {
		return
	}
	if !p.HasExpiryDate {
		p.ExpiryDate = nil
		return
	}
	if p.ExpiryDate != nil {
		t := *p.ExpiryDate
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
		p.ExpiryDate = &day
	}
}

func validateProductExpiry(hasExpiry bool, expiry *time.Time) error {
	if !hasExpiry {
		return nil
	}
	if expiry == nil {
		return errors.New("indique la fecha de vencimiento del producto")
	}
	return nil
}
