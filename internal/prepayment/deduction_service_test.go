package prepayment

import (
	"testing"
	"time"

	"tukifac/pkg/database"
	sunatpre "tukifac/pkg/sunat/prepayment"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestListOpenVouchers_listsByAffectationWithoutContactFilter(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:prepay_list?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantSale{},
		&database.TenantContact{},
		&database.TenantSalePrepaymentVoucher{},
	); err != nil {
		t.Fatal(err)
	}

	c1, c2 := uint(1), uint(2)
	now := time.Now()
	for _, tc := range []struct {
		saleID  uint
		contact uint
		doc     string
		balance float64
	}{
		{10, c1, "F001-1", 100},
		{20, c2, "F001-2", 200},
	} {
		contact := tc.contact
		sale := database.TenantSale{
			ID: tc.saleID, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "FACTURA",
			ContactID: &contact, Number: tc.doc, Total: tc.balance, BillingStatus: "accepted",
			IssueDate: now,
		}
		if err := db.Create(&sale).Error; err != nil {
			t.Fatal(err)
		}
		voucher := database.TenantSalePrepaymentVoucher{
			SaleID:            sale.ID,
			ContactID:         &contact,
			SunatDocCode:      "01",
			DocumentNumber:    tc.doc,
			OperationTypeCode: "0101",
			AffectationGroup:  sunatpre.AffectationGravado,
			RelatedDocType:    "02",
			OriginalAmount:    tc.balance,
			BalanceAmount:     tc.balance,
			Currency:          "PEN",
			Status:            sunatpre.StatusOpen,
			AvailableAt:       &now,
		}
		if err := db.Create(&voucher).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(db)
	rows, err := svc.ListOpenVouchers(0, sunatpre.AffectationGravado, 18)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 open vouchers (PHP no filtra cliente), got %d", len(rows))
	}
}

// Anular con NC la venta que dedujo un anticipo debe reponer el saldo del voucher origen y volver a
// mostrarlo en la lista de anticipos disponibles — antes esto no pasaba (bug reportado).
func TestReverseApplicationsForConsumerSale_restoresVoucherBalanceAndReappearsInList(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:prepay_reverse?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantSale{},
		&database.TenantContact{},
		&database.TenantSalePrepaymentVoucher{},
		&database.TenantSalePrepaymentApplication{},
	); err != nil {
		t.Fatal(err)
	}

	contact := uint(1)
	now := time.Now()

	// Factura A: emite el anticipo, 118 de saldo original.
	saleA := database.TenantSale{
		ID: 10, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "FACTURA",
		ContactID: &contact, Number: "F001-10", Total: 118, BillingStatus: "accepted", IssueDate: now,
	}
	if err := db.Create(&saleA).Error; err != nil {
		t.Fatal(err)
	}
	voucher := database.TenantSalePrepaymentVoucher{
		SaleID: saleA.ID, ContactID: &contact, SunatDocCode: "01", DocumentNumber: "F001-10",
		OperationTypeCode: "0101", AffectationGroup: sunatpre.AffectationGravado, RelatedDocType: "02",
		OriginalAmount: 118, BalanceAmount: 0, Currency: "PEN", Status: sunatpre.StatusOpen, AvailableAt: &now,
	}
	if err := db.Create(&voucher).Error; err != nil {
		t.Fatal(err)
	}

	// Factura B: dedujo el anticipo completo (balance quedó en 0, por eso arriba se creó así).
	saleB := database.TenantSale{
		ID: 20, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "FACTURA",
		ContactID: &contact, Number: "F001-20", Total: 118, BillingStatus: "accepted", IssueDate: now,
	}
	if err := db.Create(&saleB).Error; err != nil {
		t.Fatal(err)
	}
	app := database.TenantSalePrepaymentApplication{
		ConsumerSaleID: saleB.ID, SourceSaleID: saleA.ID, DocumentNumber: "F001-10",
		RelatedDocType: "02", AffectationGroup: sunatpre.AffectationGravado, Amount: 100, Total: 118,
	}
	if err := db.Create(&app).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewService(db)

	// Antes de anular: el voucher no aparece (sin saldo).
	before, err := svc.ListOpenVouchers(0, sunatpre.AffectationGravado, 18)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("antes de anular: esperaba 0 vouchers disponibles, got %d", len(before))
	}

	// Anular la Factura B por NC: reponer el voucher origen.
	if err := db.Transaction(func(tx *gorm.DB) error {
		return svc.ReverseApplicationsForConsumerSaleTx(tx, saleB.ID)
	}); err != nil {
		t.Fatalf("ReverseApplicationsForConsumerSaleTx: %v", err)
	}

	var reloadedVoucher database.TenantSalePrepaymentVoucher
	if err := db.First(&reloadedVoucher, "sale_id = ?", saleA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedVoucher.BalanceAmount != 118 {
		t.Fatalf("balance_amount tras revertir = %.2f, quería 118", reloadedVoucher.BalanceAmount)
	}

	var reloadedApp database.TenantSalePrepaymentApplication
	if err := db.First(&reloadedApp, app.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedApp.ReversedAt == nil {
		t.Fatal("reversed_at debería quedar seteado, no NULL")
	}

	// Después de anular: el voucher vuelve a aparecer en la lista de anticipos disponibles.
	after, err := svc.ListOpenVouchers(0, sunatpre.AffectationGravado, 18)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("después de anular: esperaba 1 voucher disponible, got %d", len(after))
	}

	// Idempotencia: revertir de nuevo (p. ej. webhook SUNAT duplicado) no debe volver a sumar el saldo.
	if err := db.Transaction(func(tx *gorm.DB) error {
		return svc.ReverseApplicationsForConsumerSaleTx(tx, saleB.ID)
	}); err != nil {
		t.Fatalf("segunda reversión: %v", err)
	}
	var reloadedVoucher2 database.TenantSalePrepaymentVoucher
	if err := db.First(&reloadedVoucher2, "sale_id = ?", saleA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedVoucher2.BalanceAmount != 118 {
		t.Fatalf("balance_amount tras segunda reversión = %.2f, quería 118 (no debe duplicar)", reloadedVoucher2.BalanceAmount)
	}
}
