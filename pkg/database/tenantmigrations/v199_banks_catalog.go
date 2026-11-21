package tenantmigrations

import (
	"strings"

	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V199BanksCatalog: catálogo de bancos (tenant_banks) con las entidades más usadas en Perú, vínculo cuenta→banco (bank_id, rellenado
// por coincidencia exacta del nombre ya escrito en la cuenta) y los campos de métodos de pago applies_to / requires_reference.
// Idempotente: siembra solo si la tabla está vacía y solo enlaza cuentas sin bank_id.
type V199BanksCatalog struct{}

func (V199BanksCatalog) Version() int { return 199 }
func (V199BanksCatalog) Name() string { return "banks_catalog" }

// DefaultBanks bancos, cajas y billeteras de uso común (sembrados en tenants existentes y nuevos).
var DefaultBanks = []string{
	"BANCO DE CREDITO DEL PERU", "BBVA CONTINENTAL", "INTERBANK", "BANCO SCOTIABANK", "BANCO DE LA NACION",
	"BANCO PICHINCHA", "BANCO DE COMERCIO", "MIBANCO", "BANBIF", "BANCO GNB", "BANCO FALABELLA", "BANCO RIPLEY",
	"CAJA AREQUIPA", "CAJA HUANCAYO", "CAJA PIURA", "CAJA CUSCO", "CAJA TRUJILLO", "CAJA SULLANA", "CAJA MAYNAS",
	"YAPE", "PLIN",
}

// bankAliases nombres cortos habituales con los que los clientes escribieron el banco en la cuenta.
var bankAliases = map[string]string{
	"BCP": "BANCO DE CREDITO DEL PERU", "BANCO DE CREDITO": "BANCO DE CREDITO DEL PERU", "CREDITO": "BANCO DE CREDITO DEL PERU",
	"BBVA": "BBVA CONTINENTAL", "SCOTIABANK": "BANCO SCOTIABANK", "PICHINCHA": "BANCO PICHINCHA", "NACION": "BANCO DE LA NACION",
	"COMERCIO": "BANCO DE COMERCIO", "FALABELLA": "BANCO FALABELLA", "RIPLEY": "BANCO RIPLEY", "GNB": "BANCO GNB",
}

func (V199BanksCatalog) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&database.TenantBank{}, &v199BankAccount{}, &v199PaymentMethod{}); err != nil {
		return err
	}
	sys := branchscope.System(db, "V199 catálogo de bancos")
	var n int64
	if err := sys.Model(&database.TenantBank{}).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		for _, name := range DefaultBanks {
			if err := sys.Create(&database.TenantBank{Name: name, Active: true}).Error; err != nil {
				return err
			}
		}
	}
	// Enlazar cuentas existentes con el banco cuyo nombre coincide exactamente (sin distinguir mayúsculas ni espacios sobrantes).
	var banks []database.TenantBank
	if err := sys.Find(&banks).Error; err != nil {
		return err
	}
	byName := map[string]uint{}
	for _, b := range banks {
		byName[strings.ToUpper(strings.TrimSpace(b.Name))] = b.ID
	}
	var accs []database.TenantBankAccount
	if err := sys.Where("bank_id IS NULL AND bank_name <> ''").Find(&accs).Error; err != nil {
		return err
	}
	for _, a := range accs {
		key := strings.ToUpper(strings.TrimSpace(a.BankName))
		if alias, ok := bankAliases[key]; ok {
			key = alias
		}
		if id, ok := byName[key]; ok {
			if err := sys.Model(&database.TenantBankAccount{}).Where("id = ?", a.ID).Update("bank_id", id).Error; err != nil {
				return err
			}
		}
	}
	// Métodos de pago antiguos: aplican a ingresos y egresos, como hasta ahora.
	return sys.Exec(`UPDATE tenant_payment_methods SET applies_to = 'both' WHERE applies_to IS NULL OR applies_to = ''`).Error
}
