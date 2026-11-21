package service

import (
	"errors"
	"fmt"
	"testing"

	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// El código de una serie SUNAT es único en TODO el tenant: no puede repetirse en otra sucursal aunque quien lo crea no vea esa sucursal.
func TestSeriesCodeUniqueAcrossBranchesUnderScope(t *testing.T) {
	raw, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(raw); err != nil {
		t.Fatal(err)
	}
	if err := branchscope.Install(raw, database.BranchScopedModels()...); err != nil {
		t.Fatal(err)
	}
	sys := branchscope.System(raw, "test: sembrado")
	if err := sys.Create(&database.TenantDocumentSeries{BranchID: 2, Series: "F777", DocType: "FACTURA", Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	seller := branchscope.Attach(raw, &branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	err = NewCompanyService(seller).assertSeriesCodeUnique(1, "venta", "F777", 0)
	if !errors.Is(err, docseries.ErrSeriesDuplicate) {
		t.Errorf("serie F777 existente en S2 creada desde S1: %v, quiere ErrSeriesDuplicate", err)
	}
	if err := NewCompanyService(seller).assertSeriesCodeUnique(1, "venta", "F778", 0); err != nil {
		t.Errorf("serie nueva: %v", err)
	}
}
