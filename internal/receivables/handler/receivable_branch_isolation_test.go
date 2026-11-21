package handler

import (
	"strconv"
	"testing"
	"time"

	"tukifac/internal/sales/branchguard/branchtest"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Aislamiento entre sucursales en cobranzas (Fase 3A).

type recvFixture struct {
	db           *gorm.DB
	sale1, sale2 uint // ventas al crédito de S1 / S2
	sess1, sess2 uint
}

func u(n uint) string { return strconv.FormatUint(uint64(n), 10) }

func newRecvFixture(t *testing.T) *recvFixture {
	t.Helper()
	db := branchtest.OpenDB(t,
		&database.TenantBranch{}, &database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantSalePayment{},
		&database.TenantSaleDetraccion{}, &database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantPaymentMethod{}, &database.TenantSaleCreditInstallment{}, &database.TenantBankMovement{},
		&database.TenantBankAccount{}, &database.TenantCompanyConfig{}, &database.TenantContact{},
	)
	f := &recvFixture{db: db}
	for _, b := range []database.TenantBranch{{ID: 1, Name: "S1", Active: true, IsMain: true}, {ID: 2, Name: "S2", Active: true}} {
		b := b
		db.Create(&b)
	}
	db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"})
	mk := func(branchID uint, num string) uint {
		s := database.TenantSale{BranchID: branchID, UserID: 1, DocType: "FACTURA", Series: "F001", Number: num,
			IssueDate: time.Now(), Total: 100, Status: "pending", BillingStatus: "accepted"}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		// Detracción BN pendiente de confirmar.
		db.Create(&database.TenantSaleDetraccion{SaleID: s.ID, BnConfirmationStatus: "pending"})
		return s.ID
	}
	f.sale1, f.sale2 = mk(1, "F001-1"), mk(2, "F001-2")
	mkSess := func(branchID, userID uint) uint {
		s := database.TenantCashSession{BranchID: branchID, UserID: userID, OpenedBy: userID, Status: "open", OpenedAt: time.Now()}
		db.Create(&s)
		return s.ID
	}
	f.sess1, f.sess2 = mkSess(1, branchtest.Vendedor1.UserID), mkSess(2, branchtest.Cajero2.UserID)
	return f
}

func (f *recvFixture) app(a branchtest.Actor) *fiber.App {
	h := NewReceivableHandler()
	app := branchtest.NewApp(f.db, a)
	app.Post("/receivables/:saleId/collect", h.CollectAPI)
	app.Post("/receivables/:saleId/confirm-bn", h.ConfirmBNAPI)
	return app
}

func (f *recvFixture) bnStatus(t *testing.T, sale uint) string {
	t.Helper()
	var d database.TenantSaleDetraccion
	if err := f.db.Where("sale_id = ?", sale).First(&d).Error; err != nil {
		t.Fatal(err)
	}
	return d.BnConfirmationStatus
}

func TestConfirmBNIsolation(t *testing.T) {
	f := newRecvFixture(t)
	body := `{"status":"confirmed","reference":"X"}`
	for _, a := range []branchtest.Actor{branchtest.Vendedor1, branchtest.Admin} { // admin con S1 activa: no escribe en S2
		if code, resp := branchtest.Do(t, f.app(a), "POST", "/receivables/"+u(f.sale2)+"/confirm-bn", body); code != 404 {
			t.Errorf("%s confirm-bn de S2: got %d want 404 (%s)", a.Name, code, resp)
		}
	}
	if code, resp := branchtest.Do(t, f.app(branchtest.Cajero2), "POST", "/receivables/"+u(f.sale1)+"/confirm-bn", body); code != 404 {
		t.Errorf("cajero S2 confirm-bn de S1: got %d want 404 (%s)", code, resp)
	}
	if st := f.bnStatus(t, f.sale2); st != "pending" {
		t.Fatalf("la detracción de S2 no debía cambiar: %s", st)
	}
	// Caso propio: se confirma.
	if code, resp := branchtest.Do(t, f.app(branchtest.Vendedor1), "POST", "/receivables/"+u(f.sale1)+"/confirm-bn", body); code != 200 {
		t.Fatalf("confirm-bn propio: got %d want 200 (%s)", code, resp)
	}
	if st := f.bnStatus(t, f.sale1); st != "confirmed" {
		t.Errorf("la detracción de S1 debía quedar confirmada: %s", st)
	}
}

func TestCollectIsolation(t *testing.T) {
	f := newRecvFixture(t)
	f.db.Where("sale_id IN ?", []uint{f.sale1, f.sale2}).Delete(&database.TenantSaleDetraccion{})
	pay := func(session string) string {
		return `{"payments":[{"method":"cash","amount":40}]` + session + `}`
	}
	// Venta de otra sucursal
	for _, a := range []branchtest.Actor{branchtest.Vendedor1, branchtest.Admin} {
		if code, resp := branchtest.Do(t, f.app(a), "POST", "/receivables/"+u(f.sale2)+"/collect", pay("")); code != 404 {
			t.Errorf("%s cobrar venta de S2: got %d want 404 (%s)", a.Name, code, resp)
		}
	}
	// Venta propia pero caja de otra sucursal
	if code, resp := branchtest.Do(t, f.app(branchtest.Vendedor1), "POST", "/receivables/"+u(f.sale1)+"/collect",
		pay(`,"cash_session_id":`+u(f.sess2))); code != 404 {
		t.Errorf("cobrar con caja de S2: got %d want 404 (%s)", code, resp)
	}
	var n int64
	f.db.Model(&database.TenantSalePayment{}).Count(&n)
	var m int64
	f.db.Model(&database.TenantCashMovement{}).Count(&m)
	if n != 0 || m != 0 {
		t.Fatalf("no debía registrarse ningún cobro (pagos=%d, movimientos=%d)", n, m)
	}
	// Caso propio: venta y caja de S1 -> pasa la guarda y cobra.
	code, resp := branchtest.Do(t, f.app(branchtest.Vendedor1), "POST", "/receivables/"+u(f.sale1)+"/collect",
		pay(`,"cash_session_id":`+u(f.sess1)))
	if code == 404 {
		t.Fatalf("cobro propio bloqueado por la guarda: %d %s", code, resp)
	}
	if code != 200 {
		t.Fatalf("el cobro propio debe seguir funcionando: %d %s", code, resp)
	}
	f.db.Model(&database.TenantSalePayment{}).Where("sale_id = ?", f.sale1).Count(&n)
	if n == 0 {
		t.Errorf("el cobro propio debía registrar el pago")
	}
}
