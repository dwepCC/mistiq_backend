package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

func TestBanksCatalog_CreateUpdateAndLinkAccount(t *testing.T) {
	svc, _, ids := destSetup(t)
	b, err := svc.CreateBank("  banco nuevo ")
	if err != nil || b.Name != "BANCO NUEVO" {
		t.Fatalf("crear banco: %v %+v", err, b)
	}
	if _, err := svc.CreateBank("Banco Nuevo"); err == nil {
		t.Error("nombre duplicado debe rechazarse")
	}
	if err := svc.SetBankAccountBank(ids["BCP"], &b.ID); err != nil {
		t.Fatal(err)
	}
	var acc database.TenantBankAccount
	svc.db.First(&acc, ids["BCP"])
	if acc.BankID == nil || *acc.BankID != b.ID || acc.BankName != "BANCO NUEVO" {
		t.Fatalf("la cuenta debe quedar enlazada con el banco: %+v", acc)
	}
	if err := svc.UpdateBank(b.ID, "Banco Renombrado", true); err != nil {
		t.Fatal(err)
	}
	svc.db.First(&acc, ids["BCP"])
	if acc.BankName != "BANCO RENOMBRADO" {
		t.Errorf("renombrar el banco actualiza sus cuentas: %q", acc.BankName)
	}
	bad := uint(9999)
	if err := svc.SetBankAccountBank(ids["BCP"], &bad); err == nil {
		t.Error("banco inexistente debe rechazarse")
	}
}

func TestPaymentMethod_AppliesToIsEnforced(t *testing.T) {
	svc, ses, _ := destSetup(t)
	sid := ses.ID
	var yape database.TenantPaymentMethod
	svc.db.Where("code = ?", "yape").First(&yape)
	if err := svc.SetPaymentMethodOptions(yape.ID, "expense", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordPayment(nil, "yape", 10, &sid, "NV-1", "Venta", nil, 1); err == nil || !strings.Contains(err.Error(), "solo sirve para pagos") {
		t.Errorf("cobrar con un método solo de egresos debe rechazarse, err=%v", err)
	}
	if err := svc.RecordExpensePayment(nil, "yape", 10, &sid, "C-1", "Compra", nil, 1); err != nil {
		t.Errorf("pagar con un método de egresos debe pasar: %v", err)
	}
	svc.SetPaymentMethodOptions(yape.ID, "income", false)
	if err := svc.RecordExpensePayment(nil, "yape", 10, &sid, "C-2", "Compra", nil, 1); err == nil || !strings.Contains(err.Error(), "solo sirve para cobros") {
		t.Errorf("pagar con un método solo de ingresos debe rechazarse, err=%v", err)
	}
	if err := svc.SetPaymentMethodOptions(yape.ID, "otro", false); err == nil {
		t.Error("applies_to inválido debe rechazarse")
	}
	var cash database.TenantPaymentMethod
	svc.db.Where("code = ?", "cash").First(&cash)
	if err := svc.SetPaymentMethodOptions(cash.ID, "income", false); err == nil {
		t.Error("el método de sistema debe seguir siendo both")
	}
}

func TestPaymentMethod_RequiresReference(t *testing.T) {
	svc, _, _ := destSetup(t)
	var yape database.TenantPaymentMethod
	svc.db.Where("code = ?", "yape").First(&yape)
	svc.SetPaymentMethodOptions(yape.ID, "both", true)
	if err := svc.ValidatePaymentReferences([]PaymentRefLine{{Method: "yape", Reference: "  "}}); err == nil || !strings.Contains(err.Error(), "exige") {
		t.Errorf("sin referencia debe rechazarse, err=%v", err)
	}
	if err := svc.ValidatePaymentReferences([]PaymentRefLine{{Method: "yape", Reference: "OP123"}, {Method: "cash"}}); err != nil {
		t.Errorf("con referencia (y efectivo sin ella) debe pasar: %v", err)
	}
}
