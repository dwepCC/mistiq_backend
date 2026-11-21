package branchscope_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var seq atomic.Int64

// setup crea un tenant en SQLite con ventas en las sucursales 1 y 2 (y una sin sucursal, "legacy").
func setup(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), seq.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := branchscope.Install(db, database.BranchScopedModels()...); err != nil {
		t.Fatal(err)
	}
	// Sembrado interno (sin scope): ver auditoría; se declara como System.
	sys := branchscope.System(db, "test: sembrado")
	for _, b := range []uint{1, 1, 2, 2, 2, 0} {
		if err := sys.Create(&database.TenantSale{BranchID: b}).Error; err != nil {
			t.Fatal(err)
		}
	}
	db.Create(&database.TenantContact{BusinessName: "Global"})
	return db
}

func scoped(db *gorm.DB, s branchscope.Scope) *gorm.DB {
	return branchscope.Attach(db, &s)
}

func count(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&database.TenantSale{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReadsAreScopedToAllowedBranches(t *testing.T) {
	db := setup(t)
	seller := scoped(db, branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	// ventas de S1 (2) + sin sucursal (1)
	if n := count(t, seller); n != 3 {
		t.Errorf("vendedor S1: %d ventas, quiere 3 (2 de S1 + 1 sin sucursal)", n)
	}
	var s2 database.TenantSale
	if err := seller.Where("branch_id = ?", 2).First(&s2).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("vendedor S1 leyendo S2: %v, quiere not found", err)
	}
	// Por ID: la venta 3 es de S2.
	if err := seller.First(&database.TenantSale{}, 3).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("First por ID de otra sucursal: %v", err)
	}
	// Multi-sucursal asignada: S1 y S2.
	multi := scoped(db, branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1, 2}})
	if n := count(t, multi); n != 6 {
		t.Errorf("multi S1+S2: %d, quiere 6", n)
	}
	// Quien ve todo.
	admin := scoped(db, branchscope.Scope{ActiveBranchID: 1, ViewAll: true})
	if n := count(t, admin); n != 6 {
		t.Errorf("view_all: %d, quiere 6", n)
	}
}

func TestFailClosedWithoutAnyBranch(t *testing.T) {
	db := setup(t)
	none := scoped(db, branchscope.Scope{})
	if n := count(t, none); n != 1 { // solo la venta sin sucursal
		t.Errorf("scope sin sucursales: %d, quiere 1 (solo la legacy sin sucursal)", n)
	}
}

func TestWritesAreScoped(t *testing.T) {
	db := setup(t)
	seller := scoped(db, branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	res := seller.Model(&database.TenantSale{}).Where("id = ?", 3).Update("notes", "x") // venta de S2
	if res.Error != nil || res.RowsAffected != 0 {
		t.Errorf("update cruzado: err=%v filas=%d, quiere 0 filas", res.Error, res.RowsAffected)
	}
	res = seller.Where("id = ?", 3).Delete(&database.TenantSale{})
	if res.RowsAffected != 0 {
		t.Errorf("delete cruzado afectó %d filas", res.RowsAffected)
	}
	res = seller.Model(&database.TenantSale{}).Where("id = ?", 1).Update("notes", "x")
	if res.Error != nil || res.RowsAffected != 1 {
		t.Errorf("update propio: err=%v filas=%d", res.Error, res.RowsAffected)
	}
	// La venta ajena sigue intacta.
	var s database.TenantSale
	db.Table("tenant_sales").Where("id = 3").Scan(&s)
	if s.ID != 3 {
		t.Error("la venta de S2 desapareció")
	}
}

func TestCreateUsesContextBranch(t *testing.T) {
	db := setup(t)
	seller := scoped(db, branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	sale := database.TenantSale{} // sin branch_id: lo fija el contexto
	if err := seller.Create(&sale).Error; err != nil {
		t.Fatal(err)
	}
	if sale.BranchID != 1 {
		t.Errorf("branch_id asignado: %d, quiere 1", sale.BranchID)
	}
	foreign := database.TenantSale{BranchID: 2}
	if err := seller.Create(&foreign).Error; !errors.Is(err, branchscope.ErrForbidden) {
		t.Errorf("alta en sucursal ajena: %v, quiere ErrForbidden", err)
	}
	admin := scoped(db, branchscope.Scope{ActiveBranchID: 1, ViewAll: true})
	if err := admin.Create(&database.TenantSale{BranchID: 2}).Error; err != nil {
		t.Errorf("quien ve todo puede crear en otra sucursal: %v", err)
	}
	// Lotes (slice).
	batch := []database.TenantSale{{}, {BranchID: 1}}
	if err := seller.Create(&batch).Error; err != nil || batch[0].BranchID != 1 {
		t.Errorf("lote: err=%v branch=%d", err, batch[0].BranchID)
	}
	bad := []database.TenantSale{{BranchID: 1}, {BranchID: 2}}
	if err := seller.Create(&bad).Error; !errors.Is(err, branchscope.ErrForbidden) {
		t.Errorf("lote con una ajena: %v", err)
	}
}

func TestAllBranchesModeIsReadOnly(t *testing.T) {
	db := setup(t)
	ro := scoped(db, branchscope.Scope{ActiveBranchID: 1, ViewAll: true, ReadOnly: true})
	if n := count(t, ro); n != 6 {
		t.Errorf("lectura consolidada: %d, quiere 6", n)
	}
	if err := ro.Create(&database.TenantSale{BranchID: 1}).Error; !errors.Is(err, branchscope.ErrReadOnly) {
		t.Errorf("create en modo todas: %v", err)
	}
	if err := ro.Model(&database.TenantSale{}).Where("id = 1").Update("notes", "x").Error; !errors.Is(err, branchscope.ErrReadOnly) {
		t.Errorf("update en modo todas: %v", err)
	}
	if err := ro.Where("id = 1").Delete(&database.TenantSale{}).Error; !errors.Is(err, branchscope.ErrReadOnly) {
		t.Errorf("delete en modo todas: %v", err)
	}
}

func TestScopeSurvivesTransactionsAndTables(t *testing.T) {
	db := setup(t)
	seller := scoped(db, branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	err := seller.Transaction(func(tx *gorm.DB) error {
		if n := count(t, tx); n != 3 {
			return fmt.Errorf("dentro de la transacción: %d, quiere 3", n)
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	// Entidades globales del tenant no se filtran.
	var n int64
	seller.Model(&database.TenantContact{}).Count(&n)
	if n != 1 {
		t.Errorf("contactos (global): %d, quiere 1", n)
	}
	// db.Table() sobre una tabla registrada también se acota.
	var cnt int64
	seller.Table("tenant_sales").Count(&cnt)
	if cnt != 3 {
		t.Errorf("Table(): %d, quiere 3", cnt)
	}
}

func TestUnscopedAccessAuditAndStrict(t *testing.T) {
	db := setup(t)
	// Modo auditoría (por defecto): sin contexto no se acota (acceso interno), pero queda registrado.
	if n := count(t, db); n != 6 {
		t.Errorf("sin scope en modo auditoría: %d, quiere 6", n)
	}
	// Modo estricto: una consulta sin contexto sobre una entidad con sucursal FALLA (nunca muestra todo).
	t.Setenv("BRANCH_SCOPE_STRICT", "1")
	var n int64
	if err := db.Model(&database.TenantSale{}).Count(&n).Error; !errors.Is(err, branchscope.ErrScopeMissing) {
		t.Errorf("estricto sin contexto: %v, quiere ErrScopeMissing", err)
	}
	// El acceso interno declarado sigue funcionando.
	if err := branchscope.System(db, "test: cron").Model(&database.TenantSale{}).Count(&n).Error; err != nil || n != 6 {
		t.Errorf("System en estricto: err=%v n=%d", err, n)
	}
	// Una solicitud con scope no se ve afectada.
	if got := count(t, scoped(db, branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})); got != 3 {
		t.Errorf("scope en estricto: %d", got)
	}
	// Tablas sin scope no fallan en estricto.
	if err := db.Model(&database.TenantContact{}).Count(&n).Error; err != nil {
		t.Errorf("tabla global en estricto: %v", err)
	}
}

func TestDisabledSwitch(t *testing.T) {
	db := setup(t)
	t.Setenv("BRANCH_SCOPE_DISABLED", "1")
	seller := scoped(db, branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	if n := count(t, seller); n != 6 {
		t.Errorf("con BRANCH_SCOPE_DISABLED=1: %d, quiere 6", n)
	}
}

// Muchos servicios hacen db.WithContext(ctxConTimeout) (reemplaza el contexto) o Session{NewDB:true}: el scope debe sobrevivir.
func TestScopeSurvivesContextReplacementAndNewSession(t *testing.T) {
	db := setup(t)
	seller := scoped(db, branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	ctx, cancel := context.WithTimeout(context.Background(), 5e9)
	defer cancel()
	if n := count(t, seller.WithContext(ctx)); n != 3 {
		t.Errorf("tras WithContext(timeout): %d, quiere 3", n)
	}
	if n := count(t, seller.Session(&gorm.Session{NewDB: true})); n != 3 {
		t.Errorf("tras Session{NewDB:true}: %d, quiere 3", n)
	}
	if n := count(t, seller.Session(&gorm.Session{})); n != 3 {
		t.Errorf("tras Session{}: %d, quiere 3", n)
	}
	// Y el modo estricto no lo trata como "sin contexto".
	t.Setenv("BRANCH_SCOPE_STRICT", "1")
	if n := count(t, seller.WithContext(ctx)); n != 3 {
		t.Errorf("estricto tras WithContext: %d", n)
	}
}

// db.Table("tenant_sales ts") deja Statement.Table = alias: el scope debe aplicarse igual (antes se saltaba en silencio).
func TestScopeAppliesToAliasedTable(t *testing.T) {
	db := setup(t)
	seller := scoped(db, branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	for _, tbl := range []string{"tenant_sales ts", "tenant_sales AS ts", "`tenant_sales` s2"} {
		var n int64
		if err := seller.Table(tbl).Count(&n).Error; err != nil || n != 3 {
			t.Errorf("Table(%q): n=%d err=%v, quiere 3", tbl, n, err)
		}
	}
	// Con JOIN y alias de la tabla principal.
	var n int64
	err := seller.Table("tenant_sales AS v").Joins("JOIN tenant_contacts c ON c.id = v.contact_id OR v.contact_id IS NULL").Count(&n).Error
	if err != nil {
		t.Fatal(err)
	}
	// Una subconsulta como tabla no debe romperse.
	if err := seller.Table("(SELECT 1 AS x) q").Count(&n).Error; err != nil {
		t.Errorf("subconsulta como tabla: %v", err)
	}
	// El modo estricto tampoco la deja pasar sin contexto.
	t.Setenv("BRANCH_SCOPE_STRICT", "1")
	if err := db.Table("tenant_sales ts").Count(&n).Error; err == nil {
		t.Error("estricto sin contexto sobre Table con alias debía fallar")
	}
}
