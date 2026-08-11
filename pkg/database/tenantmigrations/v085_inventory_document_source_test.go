package tenantmigrations

import (
	"testing"

	"tukifac/pkg/database"
)

func TestV085InventoryDocumentSource(t *testing.T) {
	db := setupV083TestDB(t)
	if err := (V083InventoryIngressEgress{}).Up(db); err != nil {
		t.Fatal(err)
	}
	if err := (V084InventoryOperationTransfer{}).Up(db); err != nil {
		t.Fatal(err)
	}
	mig := V085InventoryDocumentSource{}
	if err := mig.Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	doc := &database.TenantInventoryDocument{}
	if !db.Migrator().HasColumn(doc, "Source") {
		t.Fatal("expected source column on tenant_inventory_documents")
	}

	for _, code := range []string{"INVENTORY_ADJUSTMENT_IN", "INVENTORY_ADJUSTMENT_OUT"} {
		var n int64
		db.Model(&database.TenantInventoryOperationType{}).Where("code = ?", code).Count(&n)
		if n != 1 {
			t.Fatalf("missing operation type %s", code)
		}
	}

	var opCount int64
	db.Model(&database.TenantInventoryOperationType{}).Count(&opCount)
	if opCount != 20 {
		t.Fatalf("operation types = %d, want 20", opCount)
	}

	if err := mig.Up(db); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	var opCount2 int64
	db.Model(&database.TenantInventoryOperationType{}).Count(&opCount2)
	if opCount2 != 20 {
		t.Fatalf("after re-run operation types = %d, want 20", opCount2)
	}
}
