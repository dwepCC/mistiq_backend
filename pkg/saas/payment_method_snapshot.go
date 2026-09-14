package saas

import "encoding/json"

// paymentMethodSnapshot arma lo que se le mostró al tenant al momento de pagar (label, kind, y
// el QR o las cuentas bancarias vigentes en ese instante), para guardarlo junto al pago. Sin esto,
// SaasPayment.PaymentMethod es solo un string libre ("yape", "transfer", ...) sin ningún detalle
// de qué QR/cuenta vio realmente el tenant — imposible de auditar/conciliar después si la config
// central cambia (se reemplaza el QR, se borra una cuenta, etc.).
func paymentMethodSnapshot(cfg PlatformSettings, methodKey string) (label, kind, detailsJSON string) {
	m := PaymentMethodByKey(cfg.PaymentMethods, methodKey)
	if m == nil {
		return "", "", ""
	}
	label = m.Label
	kind = m.Kind

	switch kind {
	case PaymentMethodKindQR:
		b, _ := json.Marshal(map[string]string{"qr_url": m.QRURL})
		detailsJSON = string(b)
	case PaymentMethodKindBankAccount:
		enabled := make([]BankAccountConfig, 0, len(cfg.BankAccounts))
		for _, b := range cfg.BankAccounts {
			if b.Enabled {
				enabled = append(enabled, b)
			}
		}
		b, _ := json.Marshal(enabled)
		detailsJSON = string(b)
	}
	return label, kind, detailsJSON
}
