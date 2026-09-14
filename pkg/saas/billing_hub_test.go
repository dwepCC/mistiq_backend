package saas

import (
	"testing"

	"tukifac/pkg/database"
)

// El comprobante que el tenant subió (receipt_url) debe verse en el ciclo mientras está en
// revisión — es lo que alimenta la columna "Comprobante" del panel del tenant y el listado de
// Cobros del panel central.
func TestListInvoicesView_showsReceiptURLWhilePendingReview(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: NowLima(), PeriodEnd: NowLima().AddDate(0, 1, 0), DueDate: NowLima(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePendingReview,
	}
	db.Create(&cycle)
	pago := database.SaasPayment{
		TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 99,
		Status: database.SaasPayPendingReview, ReceiptURL: "/uploads/tenants/acme/receipts/x.jpg",
	}
	db.Create(&pago)

	invoices := ListInvoicesView(tenant.ID)
	if len(invoices) != 1 {
		t.Fatalf("se esperaba 1 factura, hay %d", len(invoices))
	}
	if invoices[0].ReceiptURL != "/uploads/tenants/acme/receipts/x.jpg" {
		t.Errorf("receipt_url = %q, se esperaba el comprobante subido", invoices[0].ReceiptURL)
	}
}

// Si hubo varios intentos (uno rechazado y luego otro que sí se subió), debe mostrarse el
// comprobante del intento MÁS RECIENTE, no el primero.
func TestListInvoicesView_showsMostRecentReceiptOnResubmission(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: NowLima(), PeriodEnd: NowLima().AddDate(0, 1, 0), DueDate: NowLima(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePendingReview,
	}
	db.Create(&cycle)

	viejo := database.SaasPayment{
		TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 99,
		Status: database.SaasPayRejected, ReceiptURL: "/old.jpg",
	}
	db.Create(&viejo)
	nuevo := database.SaasPayment{
		TenantID: tenant.ID, BillingCycleID: &cycle.ID, Amount: 99,
		Status: database.SaasPayPendingReview, ReceiptURL: "/new.jpg",
	}
	db.Create(&nuevo)

	invoices := ListInvoicesView(tenant.ID)
	if len(invoices) != 1 || invoices[0].ReceiptURL != "/new.jpg" {
		t.Fatalf("receipt_url = %q, se esperaba /new.jpg (el intento más reciente)", invoices[0].ReceiptURL)
	}
}

// Un ciclo pagado sigue exponiendo el comprobante del tenant (receipt_url) además de la
// boleta/factura del admin (fiscal_doc_url) — son cosas distintas y ninguna reemplaza a la otra.
func TestListInvoicesView_paidCycleShowsBothReceiptAndFiscalDoc(t *testing.T) {
	db := setupApprovePaymentDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	pago := database.SaasPayment{
		TenantID: tenant.ID, Amount: 99, Status: database.SaasPayApproved,
		ReceiptURL: "/receipt.jpg", FiscalDocURL: "/fiscal.pdf",
	}
	db.Create(&pago)
	cycle := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: 1, PlanID: 1,
		PeriodStart: NowLima(), PeriodEnd: NowLima().AddDate(0, 1, 0), DueDate: NowLima(),
		Amount: 99, Currency: "PEN", Status: database.SaasInvoicePaid, PaymentID: &pago.ID,
	}
	db.Create(&cycle)
	// En el flujo real, payment.billing_cycle_id ya viene seteado desde SubmitPayment (el
	// tenant paga CONTRA un ciclo existente) — se replica acá a mano para no depender de ese
	// flujo completo en este test de solo lectura.
	db.Model(&pago).Update("billing_cycle_id", cycle.ID)

	invoices := ListInvoicesView(tenant.ID)
	if len(invoices) != 1 {
		t.Fatalf("se esperaba 1 factura, hay %d", len(invoices))
	}
	if invoices[0].ReceiptURL != "/receipt.jpg" {
		t.Errorf("receipt_url = %q, se esperaba /receipt.jpg", invoices[0].ReceiptURL)
	}
	if invoices[0].FiscalDocURL != "/fiscal.pdf" {
		t.Errorf("fiscal_doc_url = %q, se esperaba /fiscal.pdf", invoices[0].FiscalDocURL)
	}
}
