package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func v194DB(t *testing.T) *gorm.DB {
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

func mainIDs(db *gorm.DB) []uint {
	var ids []uint
	db.Model(&database.TenantBranch{}).Where("is_main = ?", true).Order("id").Pluck("id", &ids)
	return ids
}

// Caso real de producción (doriconta): dos sucursales marcadas principales. Se conserva la primera.
func TestV179_KeepsFirstMainBranchOnly(t *testing.T) {
	db := v194DB(t)
	db.Create(&database.TenantBranch{Name: "Principal", Active: true, IsMain: true})
	db.Create(&database.TenantBranch{Name: "Restaurante", Active: true})
	db.Create(&database.TenantBranch{Name: "sucursal 01", Active: true, IsMain: true})
	for i := 0; i < 2; i++ { // idempotente
		if err := (V194SingleMainBranchAndOrphans{}).Up(db); err != nil {
			t.Fatal(err)
		}
	}
	if got := mainIDs(db); len(got) != 1 || got[0] != 1 {
		t.Errorf("principales tras V194: %v, quiere solo [1]", got)
	}
}

func TestV179_PrefersActiveMainAndCreatesOneWhenNone(t *testing.T) {
	db := v194DB(t)
	db.Create(&database.TenantBranch{Name: "Cerrada", Active: true, IsMain: true})
	db.Model(&database.TenantBranch{}).Where("id = 1").Update("active", false)
	db.Create(&database.TenantBranch{Name: "Abierta", Active: true, IsMain: true})
	if err := (V194SingleMainBranchAndOrphans{}).Up(db); err != nil {
		t.Fatal(err)
	}
	if got := mainIDs(db); len(got) != 1 || got[0] != 2 {
		t.Errorf("con una principal inactiva debe quedar la activa: %v", got)
	}

	none := v194DB(t) // otro tenant sin ninguna principal
	_ = none
	db2, _ := gorm.Open(sqlite.Open("file:v194_none?mode=memory&cache=shared"), &gorm.Config{})
	_ = database.ApplyBaselineSchema(db2)
	db2.Create(&database.TenantBranch{Name: "A", Active: true})
	db2.Create(&database.TenantBranch{Name: "B", Active: true})
	if err := (V194SingleMainBranchAndOrphans{}).Up(db2); err != nil {
		t.Fatal(err)
	}
	if got := mainIDs(db2); len(got) != 1 || got[0] != 1 {
		t.Errorf("sin principal: la primera sucursal activa debe serlo: %v", got)
	}
}

// Los huérfanos (branch_id = 0) se vinculan a la principal; las filas con sucursal no se tocan.
func TestV179_LinksOrphansToMainBranch(t *testing.T) {
	db := v194DB(t)
	db.Create(&database.TenantBranch{Name: "Principal", Active: true, IsMain: true})
	db.Create(&database.TenantBranch{Name: "Norte", Active: true})
	orphan := database.TenantSale{DocType: "RETENCION", Series: "R001", BranchID: 0}
	own := database.TenantSale{DocType: "NOTA DE VENTA", Series: "NV01", BranchID: 2}
	db.Create(&orphan)
	db.Create(&own)
	db.Create(&database.TenantPurchase{BranchID: 0})

	if err := (V194SingleMainBranchAndOrphans{}).Up(db); err != nil {
		t.Fatal(err)
	}
	var o, w database.TenantSale
	db.First(&o, orphan.ID)
	db.First(&w, own.ID)
	if o.BranchID != 1 {
		t.Errorf("retención huérfana: sucursal %d, quiere 1 (principal)", o.BranchID)
	}
	if w.BranchID != 2 {
		t.Errorf("venta con sucursal: %d, no debe cambiar (2)", w.BranchID)
	}
	var orphans int64
	db.Model(&database.TenantPurchase{}).Where("branch_id = 0").Count(&orphans)
	if orphans != 0 {
		t.Errorf("compras huérfanas restantes: %d", orphans)
	}
	// Segunda corrida: sin cambios.
	if err := (V194SingleMainBranchAndOrphans{}).Up(db); err != nil {
		t.Fatal(err)
	}
}

// Un huérfano que choca con un índice único en la principal se omite sin hacer fallar la migración.
func TestV179_UniqueConflictIsSkippedNotFatal(t *testing.T) {
	db := v194DB(t)
	db.Create(&database.TenantBranch{Name: "Principal", Active: true, IsMain: true})
	if err := db.AutoMigrate(&database.TenantBranchDailyComandaCounter{}); err != nil {
		t.Fatal(err)
	}
	day := "20261110"
	db.Create(&database.TenantBranchDailyComandaCounter{BranchID: 1, BusinessDate: day, LastNumber: 5})
	if err := db.Create(&database.TenantBranchDailyComandaCounter{BranchID: 0, BusinessDate: day, LastNumber: 3}).Error; err != nil {
		t.Skipf("el esquema de prueba no permite el huérfano: %v", err)
	}
	if err := (V194SingleMainBranchAndOrphans{}).Up(db); err != nil {
		t.Fatalf("un conflicto único no debe hacer fallar la migración: %v", err)
	}
	var left, main int64
	db.Model(&database.TenantBranchDailyComandaCounter{}).Where("branch_id = 0").Count(&left)
	db.Model(&database.TenantBranchDailyComandaCounter{}).Where("branch_id = 1").Count(&main)
	if left != 1 || main != 1 {
		t.Errorf("la fila en conflicto debe quedar sin tocar (huérfanos=%d, principal=%d)", left, main)
	}
}

func TestV179_NoBranchesIsNoop(t *testing.T) {
	db := v194DB(t)
	if err := (V194SingleMainBranchAndOrphans{}).Up(db); err != nil {
		t.Fatal(err)
	}
}
