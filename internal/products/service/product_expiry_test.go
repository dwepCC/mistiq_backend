package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

func TestParseProductExpiryDate(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := ParseProductExpiryDate("")
		if err != nil || got != nil {
			t.Fatalf("got %v err %v", got, err)
		}
	})
	t.Run("valid", func(t *testing.T) {
		got, err := ParseProductExpiryDate("2026-12-31")
		if err != nil || got == nil {
			t.Fatalf("got %v err %v", got, err)
		}
		if got.Format("2006-01-02") != "2026-12-31" {
			t.Fatalf("date: %s", got.Format("2006-01-02"))
		}
	})
	t.Run("invalid", func(t *testing.T) {
		_, err := ParseProductExpiryDate("31/12/2026")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestBulkImport_ExpiryDate(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	res, err := svc.BulkImportCatalog([]BulkImportItem{
		{
			RowNumber: 2,
			Name:      "Con vencimiento",
			SalePrice: 10,
			ExpiryDate: strPtr("2026-06-30"),
		},
		{
			RowNumber: 3,
			Name:      "Sin vencimiento",
			SalePrice: 12,
			ExpiryDate: strPtr(""),
		},
	}, 0, 1)
	if err != nil {
		t.Fatalf("BulkImportCatalog: %v", err)
	}
	if res.Created != 2 {
		t.Fatalf("created: got %d want 2", res.Created)
	}

	var withExpiry database.TenantProduct
	if err := db.Where("name = ?", "Con vencimiento").First(&withExpiry).Error; err != nil {
		t.Fatal(err)
	}
	if !withExpiry.HasExpiryDate || withExpiry.ExpiryDate == nil {
		t.Fatal("expected expiry on product")
	}
	want := time.Date(2026, 6, 30, 0, 0, 0, 0, time.Local)
	if !withExpiry.ExpiryDate.Equal(want) {
		t.Fatalf("expiry: got %v want %v", withExpiry.ExpiryDate, want)
	}

	var withoutExpiry database.TenantProduct
	if err := db.Where("name = ?", "Sin vencimiento").First(&withoutExpiry).Error; err != nil {
		t.Fatal(err)
	}
	if withoutExpiry.HasExpiryDate || withoutExpiry.ExpiryDate != nil {
		t.Fatal("expected no expiry")
	}
}

func strPtr(s string) *string { return &s }
