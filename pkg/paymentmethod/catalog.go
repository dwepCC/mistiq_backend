package paymentmethod

// OperationalEntry medio de cobro canónico del tenant.
type OperationalEntry struct {
	Code            string
	Name            string
	IsSystem        bool
	SortOrder       int
	BankAccountKey  string
}

// OperationalCatalog medios de cobro obligatorios.
var OperationalCatalog = []OperationalEntry{
	{Code: "cash", Name: "Efectivo", IsSystem: true, SortOrder: 0},
	{Code: "yape", Name: "Yape", SortOrder: 1, BankAccountKey: "yape"},
	{Code: "plin", Name: "Plin", SortOrder: 2, BankAccountKey: "plin"},
	{Code: "transferencia", Name: "Transferencia", SortOrder: 3, BankAccountKey: "transferencia"},
	{Code: "tarjeta", Name: "Tarjeta", SortOrder: 4, BankAccountKey: "tarjeta"},
}

// BankAccountTemplate plantilla de cuenta/billetera.
type BankAccountTemplate struct {
	Name          string
	Type          string
	PaymentMethod string
	Currency      string
}

// BankAccountCatalog cuentas por defecto.
var BankAccountCatalog = map[string]BankAccountTemplate{
	"yape":          {Name: "Billetera Yape", Type: "wallet", PaymentMethod: "yape", Currency: "PEN"},
	"plin":          {Name: "Billetera Plin", Type: "wallet", PaymentMethod: "plin", Currency: "PEN"},
	"transferencia": {Name: "Cuenta bancaria", Type: "bank", PaymentMethod: "transferencia", Currency: "PEN"},
	"tarjeta":       {Name: "Terminal tarjetas", Type: "bank", PaymentMethod: "tarjeta", Currency: "PEN"},
}

// DestinationForCode infiere destino tesorería del medio operativo.
func DestinationForCode(code string) string {
	if code == "cash" {
		return "cash"
	}
	return "bank_account"
}

// IsOperationalCode true si es medio de cobro válido en POS/caja.
func IsOperationalCode(code string) bool {
	for _, e := range OperationalCatalog {
		if e.Code == code {
			return true
		}
	}
	return false
}
