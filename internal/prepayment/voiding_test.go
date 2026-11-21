package prepayment

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	sunatpre "tukifac/pkg/sunat/prepayment"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newVoidingDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantContact{},
		&database.TenantSalePrepaymentVoucher{}, &database.TenantSalePrepaymentApplication{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedAnticipo(t *testing.T, db *gorm.DB, saleID, contactID uint, doc string, balance float64) {
	t.Helper()
	now := time.Now()
	c := contactID
	if err := db.Create(&database.TenantSale{ID: saleID, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "FACTURA",
		ContactID: &c, Number: doc, Total: balance, BillingStatus: "accepted", IssueDate: now, Status: "paid"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePrepaymentVoucher{SaleID: saleID, ContactID: &c, SunatDocCode: "01",
		DocumentNumber: doc, OperationTypeCode: "0101", AffectationGroup: sunatpre.AffectationGravado, RelatedDocType: "02",
		OriginalAmount: balance, BalanceAmount: balance, Currency: "PEN", Status: sunatpre.StatusOpen, AvailableAt: &now}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestListOpenVouchers_soloDelClienteYSinAnulados(t *testing.T) {
	db := newVoidingDB(t, "prepay_list_client")
	seedAnticipo(t, db, 10, 1, "F001-1", 100)
	seedAnticipo(t, db, 20, 2, "F001-2", 200)
	seedAnticipo(t, db, 30, 1, "F001-3", 300)
	svc := NewService(db)

	rows, err := svc.ListOpenVouchers(1, sunatpre.AffectationGravado, 18)
	if err != nil || len(rows) != 2 {
		t.Fatalf("cliente 1: want 2 anticipos, got %d (err=%v)", len(rows), err)
	}
	for _, r := range rows {
		if r.ContactID == nil || *r.ContactID != 1 {
			t.Fatalf("apareció un anticipo de otro cliente: %+v", r)
		}
	}
	if rows, _ := svc.ListOpenVouchers(0, sunatpre.AffectationGravado, 18); len(rows) != 0 {
		t.Fatalf("sin cliente la lista debe ser vacía, got %d", len(rows))
	}

	// Anular la venta del anticipo 30 (aunque su voucher siguiera "open") lo saca de la lista.
	if err := db.Model(&database.TenantSale{}).Where("id = ?", 30).Update("status", "cancelled").Error; err != nil {
		t.Fatal(err)
	}
	rows, _ = svc.ListOpenVouchers(1, sunatpre.AffectationGravado, 18)
	if len(rows) != 1 || rows[0].SourceSaleID != 10 {
		t.Fatalf("el anticipo anulado no debe aparecer: %+v", rows)
	}
}

func TestVoidSourceVoucherTx_saleDeAnticipoAnuladoSeMarcaVoided(t *testing.T) {
	db := newVoidingDB(t, "prepay_void_source")
	seedAnticipo(t, db, 10, 1, "F001-1", 100)
	svc := NewService(db)
	for i := 0; i < 2; i++ { // idempotente
		if err := db.Transaction(func(tx *gorm.DB) error { return svc.VoidSourceVoucherTx(tx, 10) }); err != nil {
			t.Fatal(err)
		}
	}
	var v database.TenantSalePrepaymentVoucher
	if err := db.First(&v, "sale_id = ?", 10).Error; err != nil {
		t.Fatal(err)
	}
	if v.Status != sunatpre.StatusVoided || v.BalanceAmount != 0 {
		t.Fatalf("voucher tras anular: status=%s balance=%.2f", v.Status, v.BalanceAmount)
	}
	// Una venta que no es anticipo no se ve afectada ni falla.
	if err := db.Transaction(func(tx *gorm.DB) error { return svc.VoidSourceVoucherTx(tx, 999) }); err != nil {
		t.Fatal(err)
	}
}

func TestPlanDeductions_rechazaAnticipoAnulado(t *testing.T) {
	db := newVoidingDB(t, "prepay_plan_voided")
	seedAnticipo(t, db, 10, 1, "F001-1", 118)
	if err := db.Model(&database.TenantSale{}).Where("id = ?", 10).Update("status", "cancelled").Error; err != nil {
		t.Fatal(err)
	}
	contact := uint(1)
	items := []database.TenantSaleItem{{IgvAffectationType: "10", Quantity: 1, UnitPrice: 200, Total: 236}}
	_, err := NewService(db).PlanDeductions(&contact, sunatpre.AffectationGravado, items,
		[]DeductionInput{{SourceSaleID: 10, Amount: 100}}, 18)
	if err == nil || !strings.Contains(err.Error(), "anulado") {
		t.Fatalf("debía rechazar el anticipo anulado, err=%v", err)
	}
}

func TestEnsureVoucherVoidable_bloqueaSiYaFueDeducido(t *testing.T) {
	db := newVoidingDB(t, "prepay_voidable")
	seedAnticipo(t, db, 10, 1, "F001-1", 118)
	c := uint(1)
	consumer := database.TenantSale{ID: 20, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "FACTURA", ContactID: &c,
		Number: "F001-20", Total: 50, IssueDate: time.Now(), Status: "paid"}
	if err := db.Create(&consumer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePrepaymentApplication{ConsumerSaleID: 20, SourceSaleID: 10,
		DocumentNumber: "F001-1", RelatedDocType: "02", AffectationGroup: sunatpre.AffectationGravado, Amount: 40, Total: 50}).Error; err != nil {
		t.Fatal(err)
	}
	err := EnsureVoucherVoidable(db, 10)
	if err == nil || !strings.Contains(err.Error(), "F001-20") {
		t.Fatalf("debía bloquear indicando F001-20, err=%v", err)
	}
	// Si el comprobante que lo usó ya está anulado, ya no bloquea.
	if err := db.Model(&database.TenantSale{}).Where("id = ?", 20).Update("status", "cancelled").Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureVoucherVoidable(db, 10); err != nil {
		t.Fatalf("con el consumidor anulado no debe bloquear: %v", err)
	}
	// Una venta que no es anticipo nunca bloquea.
	if err := EnsureVoucherVoidable(db, 999); err != nil {
		t.Fatal(err)
	}
}
