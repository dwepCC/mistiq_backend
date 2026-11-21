package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// La fila "venta" de una retención/percepción nunca queda sin sucursal: la pedida, la de su serie o la principal.
func TestDocBranchIDNeverZero(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantBranch{Name: "Principal", Active: true, IsMain: true})
	db.Create(&database.TenantBranch{Name: "Norte", Active: true})

	if got := docBranchID(db, 2, database.TenantDocumentSeries{BranchID: 1}); got != 2 {
		t.Errorf("sucursal pedida: %d, quiere 2", got)
	}
	if got := docBranchID(db, 0, database.TenantDocumentSeries{BranchID: 2}); got != 2 {
		t.Errorf("sucursal de la serie: %d, quiere 2", got)
	}
	if got := docBranchID(db, 0, database.TenantDocumentSeries{}); got != 1 {
		t.Errorf("sin pedida ni serie: %d, quiere 1 (principal)", got)
	}
}
