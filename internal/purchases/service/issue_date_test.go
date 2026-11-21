package service

import (
	"testing"
	"time"
)

func TestValidatePurchaseIssueDate(t *testing.T) {
	now := time.Date(2026, 11, 16, 15, 0, 0, 0, time.UTC)
	if err := validatePurchaseIssueDate(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), now); err != nil {
		t.Fatalf("hoy debe ser válido: %v", err)
	}
	if err := validatePurchaseIssueDate(time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), now); err != nil {
		t.Fatalf("fecha pasada válida: %v", err)
	}
	if err := validatePurchaseIssueDate(time.Date(2026, 11, 29, 12, 0, 0, 0, time.UTC), now); err == nil {
		t.Fatal("29-nov debe rechazarse")
	}
}
