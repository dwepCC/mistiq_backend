package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupOperationTypeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantInventoryOperationType{}); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestValidateDocumentOperationType_DirectionMatch(t *testing.T) {
	db := setupOperationTypeTestDB(t)

	donation, err := LookupOperationTypeByCode(db, "DONATION")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDocumentOperationType(donation, "OUT"); err != nil {
		t.Fatalf("DONATION on OUT: %v", err)
	}
	if err := ValidateDocumentOperationType(donation, "IN"); err != ErrOperationDirectionMismatch {
		t.Fatalf("DONATION on IN: got %v, want ErrOperationDirectionMismatch", err)
	}

	returnIn, err := LookupOperationTypeByCode(db, "RETURN_IN")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDocumentOperationType(returnIn, "IN"); err != nil {
		t.Fatalf("RETURN_IN on IN: %v", err)
	}
	if err := ValidateDocumentOperationType(returnIn, "OUT"); err != ErrOperationDirectionMismatch {
		t.Fatalf("RETURN_IN on OUT: got %v", err)
	}
}

func TestValidateManualOperationType_AllowManual(t *testing.T) {
	db := setupOperationTypeTestDB(t)

	purchase, err := LookupOperationTypeByCode(db, "PURCHASE")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateManualOperationType(purchase, "IN"); err != ErrOperationTypeNotManual {
		t.Fatalf("PURCHASE manual: got %v", err)
	}

	other, err := LookupOperationTypeByCode(db, "OTHER_OUT")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateManualOperationType(other, "OUT"); err != nil {
		t.Fatalf("OTHER_OUT manual: %v", err)
	}
}
