package service

import (
	"errors"
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func branchDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func mains(db *gorm.DB) []string {
	var names []string
	db.Model(&database.TenantBranch{}).Where("is_main = ?", true).Order("id").Pluck("name", &names)
	return names
}

// Regresión: CreateBranch con is_main intentaba quitar la marca con un UPDATE sin WHERE (GORM lo rechaza) y no revisaba el
// error: el tenant terminaba con DOS sucursales principales (visto en producción).
func TestCreateBranchMainLeavesExactlyOneMain(t *testing.T) {
	db := branchDB(t)
	svc := NewCompanyService(db)
	if _, err := svc.CreateBranch("Casa matriz", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateBranch("Local 2", "", "", "", false); err != nil {
		t.Fatal(err)
	}
	if got := mains(db); len(got) != 1 || got[0] != "Casa matriz" {
		t.Fatalf("principales: %v, quiere solo Casa matriz", got)
	}
	if _, err := svc.CreateBranch("Nueva principal", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	if got := mains(db); len(got) != 1 || got[0] != "Nueva principal" {
		t.Fatalf("tras crear otra principal: %v, quiere solo Nueva principal", got)
	}
}

func TestFirstBranchBecomesMainEvenIfNotRequested(t *testing.T) {
	db := branchDB(t)
	if _, err := NewCompanyService(db).CreateBranch("Única", "", "", "", false); err != nil {
		t.Fatal(err)
	}
	if got := mains(db); len(got) != 1 {
		t.Fatalf("la primera sucursal debe ser la principal: %v", got)
	}
}

func TestUpdateBranchMainMovesTheMark(t *testing.T) {
	db := branchDB(t)
	svc := NewCompanyService(db)
	a, _ := svc.CreateBranch("A", "", "", "", true)
	b, _ := svc.CreateBranch("B", "", "", "", false)
	if err := svc.UpdateBranch(b.ID, "B", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	if got := mains(db); len(got) != 1 || got[0] != "B" {
		t.Fatalf("tras marcar B: %v", got)
	}
	// Editar la principal conservando la marca funciona.
	if err := svc.UpdateBranch(b.ID, "B2", "dir", "", "", true); err != nil {
		t.Fatal(err)
	}
	// Quitarle la marca a la única principal sin designar otra se rechaza.
	if err := svc.UpdateBranch(b.ID, "B2", "", "", "", false); !errors.Is(err, ErrMainBranchRequired) {
		t.Fatalf("desmarcar la única principal: %v, quiere ErrMainBranchRequired", err)
	}
	if got := mains(db); len(got) != 1 {
		t.Fatalf("siempre debe haber una principal: %v", got)
	}
	// Una sucursal que no es principal puede editarse sin marca.
	if err := svc.UpdateBranch(a.ID, "A2", "", "", "", false); err != nil {
		t.Fatal(err)
	}
}
