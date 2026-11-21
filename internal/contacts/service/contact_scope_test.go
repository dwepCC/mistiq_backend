package service

import (
	"errors"
	"fmt"
	"testing"

	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Un contacto usado por documentos de OTRA sucursal no se puede eliminar aunque quien lo borra solo tenga acceso a la suya.
func TestContactDeleteCountsDocumentsOfAllBranches(t *testing.T) {
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
	contact := database.TenantContact{BusinessName: "Cliente compartido"}
	if err := raw.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	sys := branchscope.System(raw, "test: sembrado")
	if err := sys.Create(&database.TenantSale{BranchID: 2, ContactID: &contact.ID}).Error; err != nil {
		t.Fatal(err)
	}

	seller := branchscope.Attach(raw, &branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	n, err := NewContactService(seller).CountLinkedDocuments(contact.ID)
	if err != nil || n != 1 {
		t.Fatalf("documentos vinculados vistos por el vendedor de S1: n=%d err=%v, quiere 1", n, err)
	}
	var inUse *ContactInUseError
	if err := NewContactService(seller).Delete(contact.ID); !errors.As(err, &inUse) {
		t.Errorf("eliminar un contacto usado en otra sucursal: %v, quiere ContactInUseError", err)
	}
}
