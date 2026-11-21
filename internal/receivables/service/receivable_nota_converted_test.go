package service

import (
	"strings"
	"testing"

	salessvc "tukifac/internal/sales/service"

	"tukifac/pkg/database"
)

// Una NV a crédito convertida en factura/boleta (opción A) deja de ser cuenta por cobrar: la deuda
// vive solo en el comprobante. Si el comprobante se anula, la NV vuelve a cobrarse.
func TestReceivables_notaConvertidaNoDuplicaDeuda(t *testing.T) {
	db := setupReceivableCollectionSessionTestDB(t)
	if err := db.AutoMigrate(&database.TenantContact{}); err != nil {
		t.Fatal(err)
	}
	c := database.TenantContact{BusinessName: "Cliente", Active: true}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	contact := c.ID
	nv := &database.TenantSale{BranchID: 1, UserID: 1, ContactID: &contact, Number: "NV001-00000001", DocType: "NOTA DE VENTA", Total: 118, Status: "credit"}
	if err := db.Create(nv).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewReceivableService(db)

	rows, _, err := svc.List(ListFilter{Status: "open", Page: 1, PageSize: 50})
	if err != nil || len(rows) != 1 {
		t.Fatalf("antes de convertir la NV es cobrable: rows=%d err=%v", len(rows), err)
	}

	child := &database.TenantSale{BranchID: 1, UserID: 1, ContactID: &contact, Number: "B001-00000001", DocType: "BOLETA", Total: 118, Status: "credit", IssuedFromNotaSaleID: &nv.ID}
	if err := db.Create(child).Error; err != nil {
		t.Fatal(err)
	}

	rows, _, _ = svc.List(ListFilter{Status: "open", Page: 1, PageSize: 50})
	if len(rows) != 1 || rows[0].SaleID != child.ID {
		t.Fatalf("solo el comprobante debe figurar como deuda: %+v", rows)
	}
	sum, _ := svc.Summary(0)
	if sum.TotalDirectDue != 118 || sum.CountOpen != 1 {
		t.Fatalf("saldo duplicado en el resumen: %+v", sum)
	}
	st, err := svc.Statement(contact, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.TotalDue != 118 {
		t.Fatalf("estado de cuenta duplica la deuda: %v", st.TotalDue)
	}
	if err := svc.Collect(nv.ID, CollectPaymentInput{}); err == nil {
		t.Fatal("se esperaba error sin pagos")
	}
	if err := svc.Collect(nv.ID, CollectPaymentInput{Payments: []salessvc.PaymentInput{{Method: "cash", Amount: 10}}}); err == nil || !strings.Contains(err.Error(), "convertida") {
		t.Fatal("cobro bloqueado")
	}

	// Anulado el comprobante, la NV vuelve a ser la deuda.
	db.Model(&database.TenantSale{}).Where("id = ?", child.ID).Update("status", "cancelled")
	rows, _, _ = svc.List(ListFilter{Status: "open", Page: 1, PageSize: 50})
	if len(rows) != 1 || rows[0].SaleID != nv.ID {
		t.Fatalf("con el comprobante anulado la NV vuelve a CxC: %+v", rows)
	}
}
