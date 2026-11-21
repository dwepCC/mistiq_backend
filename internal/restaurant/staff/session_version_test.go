package staff

import (
	"testing"

	"tukifac/pkg/database"
)

// Cambiar o quitar el PIN revoca las sesiones del usuario (branch_session_version + 1); editar otros datos no.
func TestUpsertStaffForUser_PinChangeBumpsSessionVersion(t *testing.T) {
	db := setupStaffQuotaDB(t, 0)
	svc := New(db).WithTenant(staffQuotaTenantID)
	item, err := svc.CreateStaffUser(staffInput("mozo@t.com"))
	if err != nil {
		t.Fatal(err)
	}
	ver := func() uint {
		var u database.TenantUser
		db.First(&u, item.UserID)
		return u.BranchSessionVersion
	}
	db.Model(&database.TenantUser{}).Where("id = ?", item.UserID).UpdateColumn("branch_session_version", 1)

	if err := svc.UpsertStaffForUser(item.UserID, "waiter", "", UpsertFlags{}); err != nil {
		t.Fatal(err)
	}
	if ver() != 1 {
		t.Fatalf("editar sin tocar el PIN no debe revocar: versión %d", ver())
	}
	if err := svc.UpsertStaffForUser(item.UserID, "waiter", "7391", UpsertFlags{}); err != nil {
		t.Fatal(err)
	}
	if ver() != 2 {
		t.Fatalf("cambiar el PIN debe incrementar la versión: %d", ver())
	}
	if err := svc.UpsertStaffForUser(item.UserID, "waiter", "", UpsertFlags{ClearPin: true}); err != nil {
		t.Fatal(err)
	}
	if ver() != 3 {
		t.Fatalf("quitar el PIN debe incrementar la versión: %d", ver())
	}
}
