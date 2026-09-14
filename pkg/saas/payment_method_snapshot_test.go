package saas

import (
	"strings"
	"testing"
)

func TestPaymentMethodSnapshot_qrMethod(t *testing.T) {
	cfg := PlatformSettings{
		PaymentMethods: []PaymentMethodConfig{
			{Key: "yape", Label: "Yape", Enabled: true, Kind: PaymentMethodKindQR, QRURL: "/storage/saas/qr_yape_1.png"},
		},
	}
	label, kind, details := paymentMethodSnapshot(cfg, "yape")
	if label != "Yape" || kind != PaymentMethodKindQR {
		t.Fatalf("label/kind = %q/%q, want Yape/qr", label, kind)
	}
	if !strings.Contains(details, "/storage/saas/qr_yape_1.png") {
		t.Errorf("details = %q, esperaba que incluyera la url del QR", details)
	}
}

func TestPaymentMethodSnapshot_bankAccountMethod_onlyEnabledAccounts(t *testing.T) {
	cfg := PlatformSettings{
		PaymentMethods: []PaymentMethodConfig{
			{Key: "transfer", Label: "Transferencia", Enabled: true, Kind: PaymentMethodKindBankAccount},
		},
		BankAccounts: []BankAccountConfig{
			{Bank: "BCP", AccountNumber: "191-1", Enabled: true},
			{Bank: "Interbank", AccountNumber: "999-9", Enabled: false}, // no debe aparecer en el snapshot
		},
	}
	label, kind, details := paymentMethodSnapshot(cfg, "transfer")
	if label != "Transferencia" || kind != PaymentMethodKindBankAccount {
		t.Fatalf("label/kind = %q/%q, want Transferencia/bank_account", label, kind)
	}
	if !strings.Contains(details, "191-1") {
		t.Errorf("details = %q, esperaba la cuenta BCP habilitada", details)
	}
	if strings.Contains(details, "999-9") {
		t.Errorf("details = %q, no debía incluir la cuenta deshabilitada", details)
	}
}

func TestPaymentMethodSnapshot_unknownMethod_returnsEmpty(t *testing.T) {
	cfg := PlatformSettings{PaymentMethods: []PaymentMethodConfig{{Key: "yape", Label: "Yape", Kind: PaymentMethodKindQR}}}
	label, kind, details := paymentMethodSnapshot(cfg, "efectivo")
	if label != "" || kind != "" || details != "" {
		t.Errorf("método desconocido debería devolver todo vacío, got %q/%q/%q", label, kind, details)
	}
}

// El bug real que esto previene: configs guardadas antes de que existiera Kind/QRURL traían esos
// campos vacíos tras el json.Unmarshal — sin backfill, un tenant con configuración vieja se
// quedaba sin ver NINGÚN QR ni cuenta bancaria hasta que un admin reabriera y re-guardara el
// formulario en el panel central.
func TestBackfillPaymentMethodDefaults_infersKindForLegacyConfig(t *testing.T) {
	methods := []PaymentMethodConfig{
		{Key: "yape", Label: "Yape", Enabled: true},               // Kind y QRURL vacíos (config vieja)
		{Key: "plin", Label: "Plin", Enabled: true},
		{Key: "transfer", Label: "Transferencia", Enabled: true},
		{Key: "deposit", Label: "Depósito", Enabled: true},
	}
	backfillPaymentMethodDefaults(methods, "/storage/saas/qr_yape_old.png", "/storage/saas/qr_plin_old.png")

	byKey := map[string]PaymentMethodConfig{}
	for _, m := range methods {
		byKey[m.Key] = m
	}
	if byKey["yape"].Kind != PaymentMethodKindQR || byKey["yape"].QRURL != "/storage/saas/qr_yape_old.png" {
		t.Errorf("yape = %+v, want kind=qr con la url legacy migrada", byKey["yape"])
	}
	if byKey["plin"].Kind != PaymentMethodKindQR || byKey["plin"].QRURL != "/storage/saas/qr_plin_old.png" {
		t.Errorf("plin = %+v, want kind=qr con la url legacy migrada", byKey["plin"])
	}
	if byKey["transfer"].Kind != PaymentMethodKindBankAccount {
		t.Errorf("transfer.Kind = %q, want bank_account", byKey["transfer"].Kind)
	}
	if byKey["deposit"].Kind != PaymentMethodKindBankAccount {
		t.Errorf("deposit.Kind = %q, want bank_account", byKey["deposit"].Kind)
	}
}

// Si el método ya trae Kind/QRURL explícitos (config nueva, guardada tras este cambio), el
// backfill no debe pisarlos con nada.
func TestBackfillPaymentMethodDefaults_doesNotOverrideExplicitValues(t *testing.T) {
	methods := []PaymentMethodConfig{
		{Key: "yape", Label: "Yape", Kind: PaymentMethodKindQR, QRURL: "/storage/saas/qr_yape_new.png"},
	}
	backfillPaymentMethodDefaults(methods, "/storage/saas/qr_yape_old.png", "")
	if methods[0].QRURL != "/storage/saas/qr_yape_new.png" {
		t.Errorf("QRURL = %q, no debía pisarse con el valor legacy habiendo uno explícito", methods[0].QRURL)
	}
}

func TestPaymentMethodByKey_caseInsensitiveAndTrims(t *testing.T) {
	methods := []PaymentMethodConfig{{Key: "yape", Label: "Yape"}}
	if m := PaymentMethodByKey(methods, "  YAPE  "); m == nil || m.Label != "Yape" {
		t.Errorf("esperaba encontrar yape sin importar mayúsculas/espacios, got %+v", m)
	}
	if m := PaymentMethodByKey(methods, "plin"); m != nil {
		t.Errorf("no debería encontrar un método inexistente, got %+v", m)
	}
	if m := PaymentMethodByKey(methods, ""); m != nil {
		t.Errorf("key vacía no debería matchear nada, got %+v", m)
	}
}
