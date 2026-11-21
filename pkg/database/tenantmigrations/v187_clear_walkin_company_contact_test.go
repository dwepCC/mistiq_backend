package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// El "Clientes Varios" con el correo/teléfono de la empresa queda limpio; uno con datos propios o un cliente
// real con el mismo correo que la empresa no se tocan. Idempotente.
func TestV172ClearWalkInCompanyContact(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantContact{}, &database.TenantCompanyConfig{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantCompanyConfig{ID: 1, Email: "Empresa@Mail.com", Phone: "999111222"})
	walkIn := database.TenantContact{DocType: "0", DocNumber: "99999999", BusinessName: "Clientes Varios", IsDefaultWalkIn: true, Email: "empresa@mail.com", Phone: "999111222"}
	custom := database.TenantContact{DocType: "0", DocNumber: "99999999", BusinessName: "Clientes Varios 2", IsDefaultWalkIn: true, Email: "otro@mail.com", Phone: "111"}
	real := database.TenantContact{DocType: "1", DocNumber: "47316691", BusinessName: "Cliente Real", Email: "empresa@mail.com", Phone: "999111222"}
	db.Create(&walkIn)
	db.Create(&custom)
	db.Create(&real)

	for i := 0; i < 2; i++ {
		if err := (V187ClearWalkInCompanyContact{}).Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}
	get := func(id uint) database.TenantContact {
		var c database.TenantContact
		db.First(&c, id)
		return c
	}
	if c := get(walkIn.ID); c.Email != "" || c.Phone != "" {
		t.Errorf("Clientes Varios con datos de la empresa debía limpiarse: %+v", c)
	}
	if c := get(custom.ID); c.Email != "otro@mail.com" || c.Phone != "111" {
		t.Errorf("datos propios del walk-in deben respetarse: %+v", c)
	}
	if c := get(real.ID); c.Email != "empresa@mail.com" || c.Phone != "999111222" {
		t.Errorf("un cliente real no debe tocarse: %+v", c)
	}
}

func TestV172ClearWalkInCompanyContact_EmptyIsNoop(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (V187ClearWalkInCompanyContact{}).Up(db); err != nil {
		t.Fatalf("sin tablas debe ser no-op: %v", err)
	}
}
