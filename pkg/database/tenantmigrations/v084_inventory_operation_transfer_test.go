package tenantmigrations

import (
	"testing"

	"tukifac/pkg/database"
)

func TestV084InventoryOperationTransfer_SeedIdempotent(t *testing.T) {
	db := setupV083TestDB(t)
	if err := (V083InventoryIngressEgress{}).Up(db); err != nil {
		t.Fatal(err)
	}
	var before int64
	db.Model(&database.TenantInventoryOperationType{}).Count(&before)
	if before != 20 {
		t.Fatalf("pre V084 types = %d, want 20", before)
	}
	if err := (V084InventoryOperationTransfer{}).Up(db); err != nil {
		t.Fatal(err)
	}
	var after int64
	db.Model(&database.TenantInventoryOperationType{}).Count(&after)
	if after != 20 {
		t.Fatalf("post V084 types = %d, want 20", after)
	}
	var transfer int64
	db.Model(&database.TenantInventoryOperationType{}).Where("code = ?", "TRANSFER").Count(&transfer)
	if transfer != 1 {
		t.Fatal("expected TRANSFER operation type")
	}
}
