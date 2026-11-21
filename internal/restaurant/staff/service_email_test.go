package staff

import (
	"errors"
	"testing"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// createAndDeleteStaff crea un operativo de Tukichef, le da historial simulado y lo elimina
// (borrado lógico) como lo haría la pantalla de usuarios.
func createAndDeleteStaff(t *testing.T, db *gorm.DB, svc *Service, email, pin string) uint {
	t.Helper()
	in := staffInput(email)
	in.Pin = pin
	item, err := svc.CreateStaffUser(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&database.TenantUser{}, item.UserID).Error; err != nil {
		t.Fatal(err)
	}
	return item.UserID
}

// Bug reportado: crear personal con el correo de un usuario eliminado lo BORRABA definitivamente
// (Unscoped), dejando sus ventas/cajas con user_id huérfano. Ahora devuelve un error tipado y el
// eliminado sigue existiendo.
func TestCreateStaffUser_correoDeEliminadoNoLoPurga(t *testing.T) {
	db := setupStaffQuotaDB(t, 0)
	svc := New(db).WithTenant(staffQuotaTenantID)
	oldID := createAndDeleteStaff(t, db, svc, "viejo@t.com", "1111")

	in := staffInput("viejo@t.com")
	in.Pin = "2222"
	_, err := svc.CreateStaffUser(in)
	var de *database.DeletedUserEmailError
	if !errors.As(err, &de) || de.UserID != oldID || de.RoleName != operativeRestaurantRoleName {
		t.Fatalf("esperaba *DeletedUserEmailError del eliminado %d, llegó %v", oldID, err)
	}
	var still database.TenantUser
	if err := db.Unscoped().First(&still, oldID).Error; err != nil {
		t.Fatalf("el usuario eliminado NO debe borrarse definitivamente: %v", err)
	}
}

// El otro lado del mismo bug: un usuario ERP ACTIVO sin perfil de staff también era purgado.
func TestCreateStaffUser_correoDeUsuarioActivoNoLoPurga(t *testing.T) {
	db := setupStaffQuotaDB(t, 0)
	svc := New(db).WithTenant(staffQuotaTenantID)
	erp := database.TenantUser{Name: "Admin ERP", Email: "admin@t.com", RoleID: 1, Active: true}
	if err := db.Create(&erp).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateStaffUser(staffInput("admin@t.com")); err == nil {
		t.Fatal("el correo de un usuario activo debe rechazarse")
	}
	if err := db.First(&database.TenantUser{}, erp.ID).Error; err != nil {
		t.Fatalf("el usuario ERP activo NO debe borrarse: %v", err)
	}
}

func TestCreateStaffUser_ReplaceDeletedCreaNuevoYConservaAlViejo(t *testing.T) {
	db := setupStaffQuotaDB(t, 0)
	svc := New(db).WithTenant(staffQuotaTenantID)
	oldID := createAndDeleteStaff(t, db, svc, "viejo@t.com", "1111")

	in := staffInput("viejo@t.com")
	in.Pin = "2222"
	in.ReplaceDeleted = true
	item, err := svc.CreateStaffUser(in)
	if err != nil {
		t.Fatalf("crear reemplazando: %v", err)
	}
	if item.UserID == oldID {
		t.Fatal("debía crearse un usuario nuevo")
	}
	var old database.TenantUser
	if err := db.Unscoped().First(&old, oldID).Error; err != nil || !old.DeletedAt.Valid || old.Email == "viejo@t.com" {
		t.Fatalf("el viejo debía conservarse eliminado y con el correo liberado: %+v %v", old, err)
	}
}

func TestRestoreStaffUser_reactivaConPinNuevo(t *testing.T) {
	db := setupStaffQuotaDB(t, 0)
	svc := New(db).WithTenant(staffQuotaTenantID)
	oldID := createAndDeleteStaff(t, db, svc, "viejo@t.com", "1111")

	item, err := svc.RestoreStaffUser(oldID, CreateStaffUserInput{Name: "Mozo Renovado", EmployeeType: "cashier", Pin: "3333"})
	if err != nil {
		t.Fatalf("restaurar: %v", err)
	}
	if item.UserID != oldID || !item.Active || !item.StaffActive || item.EmployeeType != "cashier" || !item.HasPin {
		t.Errorf("restauración incorrecta: %+v", item)
	}
	var u database.TenantUser
	if err := db.First(&u, oldID).Error; err != nil || u.Name != "Mozo Renovado" {
		t.Errorf("el usuario debía quedar activo (no eliminado): %+v %v", u, err)
	}
	// El PIN viejo ya no sirve y el nuevo sí.
	if _, _, _, err := svc.AuthenticatePIN("1111", "pos"); err == nil {
		t.Error("el PIN anterior no debe funcionar tras restaurar")
	}
}

func TestRestoreStaffUser_rechazaUsuarioDelERPYNoEliminado(t *testing.T) {
	db := setupStaffQuotaDB(t, 0)
	svc := New(db).WithTenant(staffQuotaTenantID)

	erpRole := database.TenantRole{Name: "Cajero", IsSystem: true}
	db.Create(&erpRole)
	erp := database.TenantUser{Name: "Cajero ERP", Email: "erp@t.com", RoleID: erpRole.ID, Active: true}
	db.Create(&erp)
	db.Delete(&database.TenantUser{}, erp.ID)
	if _, err := svc.RestoreStaffUser(erp.ID, CreateStaffUserInput{Name: "X", EmployeeType: "cashier", Pin: "4444"}); err == nil {
		t.Error("un usuario del ERP eliminado no debe restaurarse desde Tukichef")
	}

	live := createAndDeleteStaff(t, db, svc, "otro@t.com", "5555")
	db.Unscoped().Model(&database.TenantUser{}).Where("id = ?", live).Update("deleted_at", nil)
	if _, err := svc.RestoreStaffUser(live, CreateStaffUserInput{Name: "X", EmployeeType: "cashier", Pin: "6666"}); err == nil {
		t.Error("restaurar a un usuario que no está eliminado debe fallar")
	}
}

func TestListStaffManagement_soloEliminadosConRestorable(t *testing.T) {
	db := setupStaffQuotaDB(t, 0)
	svc := New(db).WithTenant(staffQuotaTenantID)
	opID := createAndDeleteStaff(t, db, svc, "op@t.com", "1111")
	erpRole := database.TenantRole{Name: "Administrador", IsSystem: true}
	db.Create(&erpRole)
	erp := database.TenantUser{Name: "Admin ERP", Email: "erp@t.com", RoleID: erpRole.ID, Active: true}
	db.Create(&erp)
	db.Delete(&database.TenantUser{}, erp.ID)
	if _, err := svc.CreateStaffUser(staffInput("vivo@t.com")); err != nil {
		t.Fatal(err)
	}

	deleted, err := svc.ListStaffManagement(true)
	if err != nil || len(deleted) != 2 {
		t.Fatalf("debía listar solo los 2 eliminados: %d %v", len(deleted), err)
	}
	for _, it := range deleted {
		if it.DeletedAt == nil {
			t.Errorf("falta deleted_at: %+v", it)
		}
		if want := it.UserID == opID; it.Restorable != want {
			t.Errorf("restorable de %d = %v, esperado %v", it.UserID, it.Restorable, want)
		}
	}
	live, _ := svc.ListStaffManagement(false)
	for _, it := range live {
		if it.DeletedAt != nil {
			t.Errorf("el listado normal no debe traer eliminados: %+v", it)
		}
	}
}
