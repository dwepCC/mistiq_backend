package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Flujo real bajo el scope de sucursal: el usuario del ORIGEN crea la transferencia y el usuario del DESTINO (que solo
// tiene su sucursal) la confirma. La confirmación debe mover las series y el stock a destino; sin acceso a las filas de origen
// los UPDATE terminaban en 0 filas sin error (series en tránsito para siempre).
func TestTransferWithSerialsConfirmedByDestinationUnderScope(t *testing.T) {
	raw, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(raw); err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{ Up(*gorm.DB) error }{tenantmigrations.V083InventoryIngressEgress{}, tenantmigrations.V084InventoryOperationTransfer{}, tenantmigrations.V085InventoryDocumentSource{}} {
		if err := m.Up(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := branchscope.Install(raw, database.BranchScopedModels()...); err != nil {
		t.Fatal(err)
	}
	sys := branchscope.System(raw, "test: sembrado")
	for _, n := range []string{"Origen", "Destino"} {
		if err := sys.Create(&database.TenantBranch{Name: n, Active: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	prod := database.TenantProduct{Name: "Laptop", ManageStock: true, ManageSeries: true, Active: true}
	if err := sys.Create(&prod).Error; err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"S-1", "S-2"} {
		if err := sys.Create(&database.TenantProductSerial{ProductID: prod.ID, BranchID: 1, Serial: s, Status: "available"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := sys.Create(&database.TenantProductStock{ProductID: prod.ID, BranchID: 1, Quantity: 2}).Error; err != nil {
		t.Fatal(err)
	}

	origin := branchscope.Attach(raw, &branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	dest := branchscope.Attach(raw, &branchscope.Scope{ActiveBranchID: 2, AllowedBranchIDs: []uint{2}})

	trID, err := NewInventoryService(origin).CreateTransferWithLines(1, 2, 1, "t", []TransferLineInput{{ProductID: prod.ID, Quantity: 2}})
	if err != nil {
		t.Fatalf("crear desde el origen: %v", err)
	}
	if err := NewInventoryService(dest).ConfirmTransfer(trID, 2); err != nil {
		t.Fatalf("confirmar desde el destino: %v", err)
	}

	var serials []database.TenantProductSerial
	sys.Order("id").Find(&serials)
	for _, s := range serials {
		if s.BranchID != 2 || s.Status != "available" {
			t.Errorf("serie %s quedó en sucursal %d estado %q, quiere sucursal 2 available", s.Serial, s.BranchID, s.Status)
		}
	}
	var st database.TenantProductStock
	if err := sys.Where("product_id = ? AND branch_id = ?", prod.ID, 2).First(&st).Error; err != nil || st.Quantity != 2 {
		t.Errorf("stock en destino: %+v err=%v, quiere 2", st, err)
	}
	var origSt database.TenantProductStock
	sys.Where("product_id = ? AND branch_id = ?", prod.ID, 1).First(&origSt)
	if origSt.Quantity != 0 {
		t.Errorf("stock en origen: %v, quiere 0", origSt.Quantity)
	}
}
