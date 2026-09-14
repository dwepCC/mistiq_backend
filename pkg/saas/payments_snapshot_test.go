package saas

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

// El bug real que esto previene: SaasPayment.PaymentMethod era un string libre ("yape") sin
// ningún detalle de qué QR/cuenta vio el tenant al pagar — imposible de auditar/reportar por
// método, y si la config central cambiaba después (se reemplaza el QR, se borra una cuenta) no
// quedaba ningún rastro de lo que realmente se le mostró en el momento del pago.
func TestSubmitPayment_snapshotsPaymentMethodDetails_qr(t *testing.T) {
	db := setupApprovePaymentDB(t)

	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	for i := range cfg.PaymentMethods {
		if cfg.PaymentMethods[i].Key == "yape" {
			cfg.PaymentMethods[i].QRURL = "/storage/saas/qr_yape_123.png"
		}
	}
	if err := SaveSettings(cfg); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	payment, err := SubmitPayment(SubmitPaymentInput{
		TenantID: tenant.ID, Amount: 99, PaymentMethod: "yape", FromAdmin: true,
	})
	if err != nil {
		t.Fatalf("SubmitPayment: %v", err)
	}
	if payment.PaymentMethodLabel != "Yape" {
		t.Errorf("PaymentMethodLabel = %q, want Yape", payment.PaymentMethodLabel)
	}
	if payment.PaymentMethodKind != PaymentMethodKindQR {
		t.Errorf("PaymentMethodKind = %q, want qr", payment.PaymentMethodKind)
	}
	if !strings.Contains(payment.PaymentDetailsJSON, "qr_yape_123.png") {
		t.Errorf("PaymentDetailsJSON = %q, esperaba que incluyera la url del QR vigente", payment.PaymentDetailsJSON)
	}
}

// SubmitRenewalRequest delega en SubmitPayment (mismo camino de escritura) — confirma que el
// snapshot también queda guardado en el flujo de renovación, no solo en el de deuda pendiente.
func TestSubmitRenewalRequest_alsoSnapshotsPaymentMethod(t *testing.T) {
	db := setupApprovePaymentDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	cfg, _ := LoadSettings()
	for i := range cfg.PaymentMethods {
		if cfg.PaymentMethods[i].Key == "transfer" {
			cfg.PaymentMethods[i].Enabled = true
		}
	}
	cfg.BankAccounts = []BankAccountConfig{{Bank: "BCP", AccountNumber: "191-1", Enabled: true}}
	if err := SaveSettings(cfg); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	payment, err := SubmitRenewalRequest(SubmitRenewalRequestInput{
		TenantID: tenant.ID, PlanID: plan.ID, PaymentMethod: "transfer",
	})
	if err != nil {
		t.Fatalf("SubmitRenewalRequest: %v", err)
	}
	if payment.PaymentMethodKind != PaymentMethodKindBankAccount {
		t.Errorf("PaymentMethodKind = %q, want bank_account", payment.PaymentMethodKind)
	}
	if !strings.Contains(payment.PaymentDetailsJSON, "191-1") {
		t.Errorf("PaymentDetailsJSON = %q, esperaba la cuenta bancaria vigente", payment.PaymentDetailsJSON)
	}
}
