package service

import (
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/paymentmethod"
	salessvc "tukifac/internal/sales/service"
	sunatdet "tukifac/pkg/sunat/detraccion"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPrepareDetractionSalePaymentsAllowCredit_partial(t *testing.T) {
	eval := detractionEval1180(t)
	out, isCredit, err := salessvc.PrepareDetractionSalePaymentsAllowCredit(
		[]salessvc.PaymentInput{{Method: "cash", Amount: 500}},
		1180,
		eval,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !isCredit {
		t.Fatal("expected credit sale")
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(out))
	}
	if out[1].Method != paymentmethod.CodeDetraccionBN {
		t.Fatalf("expected spot line: %+v", out[1])
	}
}

func TestSaleBalance_detraccion1180(t *testing.T) {
	sale := database.TenantSale{Total: 1180, Status: "credit"}
	det := &database.TenantSaleDetraccion{
		NetPayablePen:       1132.80,
		DetractionAmountPen: 47.20,
		BnConfirmationStatus: BnStatusPending,
	}
	payments := []database.TenantSalePayment{
		{Method: "cash", Amount: 500},
		{Method: paymentmethod.CodeDetraccionBN, Amount: 47.20},
	}
	target, paid, due, spot, spotPending, bn := SaleBalance(sale, det, payments)
	if target != 1132.80 || paid != 500 || due != 632.80 {
		t.Fatalf("balance: target=%v paid=%v due=%v", target, paid, due)
	}
	if spot != 47.20 || spotPending != 47.20 || bn != BnStatusPending {
		t.Fatalf("spot: amt=%v pending=%v bn=%q", spot, spotPending, bn)
	}
	if !HasOpenReceivable(sale, det, payments) {
		t.Fatal("expected open receivable")
	}
}

func TestReceivableService_ConfirmBN(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:recv?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantSale{},
		&database.TenantSaleDetraccion{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	sale := database.TenantSale{Number: "F001-00000001", Total: 1180, Status: "credit"}
	if err := db.Create(&sale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSaleDetraccion{
		SaleID: sale.ID, NetPayablePen: 1132.80, DetractionAmountPen: 47.20,
		BnConfirmationStatus: BnStatusPending,
	}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewReceivableService(db)
	row, err := svc.ConfirmBN(sale.ID, ConfirmBNInput{Status: BnStatusConfirmed, Reference: "OP-123"})
	if err != nil {
		t.Fatal(err)
	}
	if row.BnConfirmationStatus != BnStatusConfirmed || row.BnConfirmationReference != "OP-123" {
		t.Fatalf("unexpected row: %+v", row)
	}
}

// Bug reportado: una nota de crédito ("FC01-00000001") aparecía como "Cobro FC01-00000001" en
// Cuentas Bancarias, sumando al saldo como si fuera un ingreso real. Causa raíz: una nota de
// crédito nace con Status="paid" y Total>0 (ver billing/service CreateCreditNoteAndVoidSale) pero
// sin ningún TenantSalePayment — antes de este fix, HasOpenReceivable la veía como "cobrable" por
// el total completo, exactamente igual que una venta real.
func TestHasOpenReceivable_ExcludesCreditAndDebitNotes(t *testing.T) {
	creditNote := database.TenantSale{DocType: "NOTA_CREDITO", Status: "paid", Total: 65}
	if HasOpenReceivable(creditNote, nil, nil) {
		t.Fatal("una nota de crédito nunca debe ser una cuenta por cobrar, sin importar su Status/Total")
	}
	debitNote := database.TenantSale{DocType: "NOTA_DEBITO", Status: "paid", Total: 30}
	if HasOpenReceivable(debitNote, nil, nil) {
		t.Fatal("una nota de débito nunca debe ser una cuenta por cobrar")
	}
	// Control: una FACTURA real con las mismas condiciones (paid, sin pagos registrados) sigue
	// evaluándose igual que antes — este fix no debe tocar el caso normal.
	realSale := database.TenantSale{DocType: "FACTURA", Status: "credit", Total: 1090}
	if !HasOpenReceivable(realSale, nil, nil) {
		t.Fatal("una factura real con saldo pendiente debe seguir siendo una cuenta por cobrar")
	}
}

func TestReceivableService_List_ExcludesCreditNotes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:recv_notes?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantSale{}, &database.TenantSaleDetraccion{},
		&database.TenantSalePayment{}, &database.TenantSaleCreditInstallment{}, &database.TenantContact{},
	); err != nil {
		t.Fatal(err)
	}
	realSale := database.TenantSale{Number: "F001-00000001", DocType: "FACTURA", Status: "credit", Total: 1090}
	if err := db.Create(&realSale).Error; err != nil {
		t.Fatal(err)
	}
	creditNote := database.TenantSale{Number: "FC01-00000001", DocType: "NOTA_CREDITO", Status: "paid", Total: 65}
	if err := db.Create(&creditNote).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewReceivableService(db)
	rows, total, err := svc.List(ListFilter{Status: "all", Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1 (solo la factura real; la nota de crédito no es cobrable)", total)
	}
	for _, r := range rows {
		if r.SaleID == creditNote.ID {
			t.Fatalf("la nota de crédito no debió aparecer en la lista de cuentas por cobrar: %+v", r)
		}
	}
}

func TestReceivableService_Collect_RejectsCreditNote(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:recv_collect_note?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleDetraccion{}, &database.TenantSalePayment{}); err != nil {
		t.Fatal(err)
	}
	creditNote := database.TenantSale{Number: "FC01-00000001", DocType: "NOTA_CREDITO", Status: "paid", Total: 65}
	if err := db.Create(&creditNote).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewReceivableService(db)
	err = svc.Collect(creditNote.ID, CollectPaymentInput{
		Payments: []salessvc.PaymentInput{{Method: "transferencia", Amount: 65}},
	})
	if err == nil {
		t.Fatal("cobrar una nota de crédito debería rechazarse")
	}
	var count int64
	db.Model(&database.TenantSalePayment{}).Where("sale_id = ?", creditNote.ID).Count(&count)
	if count != 0 {
		t.Fatal("no debió crearse ningún pago para la nota de crédito")
	}
}

func detractionEval1180(t *testing.T) sunatdet.CalcResult {
	t.Helper()
	cat, err := sunatdet.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	res, err := sunatdet.Evaluate(cat, sunatdet.CalcInput{
		OperationTypeCode: "1001",
		SunatDocCode:      "01",
		Currency:          "PEN",
		GravadoTotalPEN:   1180,
		SaleTotalPEN:      1180,
		GoodCode:          "014",
		BankAccount:       "0004-1234567890",
		PaymentMethodCode: "001",
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}
