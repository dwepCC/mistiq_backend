package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// El bug real: una nota de crédito se guarda como una fila más de tenant_sales (mismo monto,
// mismo método de pago que la venta que anula, status "paid") sin cash_session_id — antes,
// listOrphanSalesForSession la traía igual que a una venta huérfana cualquiera, y el arqueo la
// sumaba como un ingreso nuevo. Este test prueba el fix exactamente en el punto donde se arman
// esas ventas huérfanas: una BOLETA huérfana normal debe seguir apareciendo (no romper lo que
// funciona), pero una NOTA_CREDITO nunca.
func TestListOrphanSalesForSession_excludesCreditNoteButKeepsNormalSales(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	session := newSession(t, db, 100)

	normal := database.TenantSale{
		BranchID: session.BranchID, UserID: 7, SeriesID: 1, DocType: "BOLETA", Series: "B001", Correlative: 1,
		Number: "B001-1", IssueDate: time.Now(), Total: 100, Currency: "PEN",
		PaymentMethod: "tarjeta", Status: "paid", CreatedAt: session.OpenedAt.Add(time.Hour),
	}
	if err := db.Create(&normal).Error; err != nil {
		t.Fatal(err)
	}
	creditNote := database.TenantSale{
		BranchID: session.BranchID, UserID: 7, SeriesID: 1, DocType: "NOTA_CREDITO", Series: "BC01", Correlative: 1,
		Number: "BC01-1", IssueDate: time.Now(), Total: 100, Currency: "PEN",
		PaymentMethod: "tarjeta", Status: "paid", CreatedAt: session.OpenedAt.Add(2 * time.Hour),
	}
	if err := db.Create(&creditNote).Error; err != nil {
		t.Fatal(err)
	}

	sales, err := svc.listOrphanSalesForSession(session)
	if err != nil {
		t.Fatalf("listOrphanSalesForSession: %v", err)
	}

	if len(sales) != 1 {
		t.Fatalf("esperaba 1 venta huérfana (la boleta, sin la nota de crédito), got %d: %+v", len(sales), sales)
	}
	if sales[0].ID != normal.ID {
		t.Errorf("la venta huérfana debía ser la boleta (id=%d), got id=%d doc_type=%s", normal.ID, sales[0].ID, sales[0].DocType)
	}
	for _, s := range sales {
		if s.DocType == "NOTA_CREDITO" || s.DocType == "NOTA_DEBITO" {
			t.Errorf("una nota (%s) no debe aparecer como venta huérfana del arqueo: %+v", s.DocType, s)
		}
	}
}
