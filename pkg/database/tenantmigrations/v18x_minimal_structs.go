package tenantmigrations

// Estructuras MÍNIMAS de V195–V199: cada migración agrega solo sus columnas. Con el struct completo, GORM compara TODAS las columnas
// existentes de la tabla y puede emitir ALTER ... MODIFY (copia de tabla) sobre tablas con millones de filas. Mismo patrón que V129.
// Las etiquetas de cada columna son idénticas a las del struct completo en pkg/database/migrations.go.

type v197CashMovement struct {
	ID           uint     `gorm:"primaryKey"`
	Currency     string   `gorm:"size:10;default:'PEN'"`
	ExchangeRate *float64 `gorm:"type:decimal(10,4)"`
	AmountPEN    *float64 `gorm:"column:amount_pen;type:decimal(15,2)"`
}

func (v197CashMovement) TableName() string { return "tenant_cash_movements" }

type v197BankMovement struct {
	ID           uint     `gorm:"primaryKey"`
	Currency     string   `gorm:"size:10;default:'PEN'"`
	ExchangeRate *float64 `gorm:"type:decimal(10,4)"`
	AmountPEN    *float64 `gorm:"column:amount_pen;type:decimal(15,2)"`
}

func (v197BankMovement) TableName() string { return "tenant_bank_movements" }

type v198CashMovement struct {
	ID         uint  `gorm:"primaryKey"`
	TransferID *uint `gorm:"column:transfer_id;index"`
}

func (v198CashMovement) TableName() string { return "tenant_cash_movements" }

type v198BankMovement struct {
	ID            uint     `gorm:"primaryKey"`
	AccountAmount *float64 `gorm:"column:account_amount;type:decimal(15,2)"`
	TransferID    *uint    `gorm:"column:transfer_id;index"`
}

func (v198BankMovement) TableName() string { return "tenant_bank_movements" }

type v199BankAccount struct {
	ID     uint  `gorm:"primaryKey"`
	BankID *uint `gorm:"column:bank_id;index"`
}

func (v199BankAccount) TableName() string { return "tenant_bank_accounts" }

type v199PaymentMethod struct {
	ID                uint   `gorm:"primaryKey"`
	AppliesTo         string `gorm:"column:applies_to;size:10;default:'both'"`
	RequiresReference bool   `gorm:"column:requires_reference;default:false"`
}

func (v199PaymentMethod) TableName() string { return "tenant_payment_methods" }

type v195Purchase struct {
	ID              uint     `gorm:"primaryKey"`
	ExchangeRate    *float64 `gorm:"column:exchange_rate;type:decimal(10,4)"`
	PurchaseOrderID *uint    `gorm:"column:purchase_order_id;index"`
}

func (v195Purchase) TableName() string { return "tenant_purchases" }

// V202: país opcional del cliente (solo esa columna).
type v202Contact struct {
	ID      uint   `gorm:"primaryKey"`
	Country string `gorm:"column:country;size:2"`
}

func (v202Contact) TableName() string { return "tenant_contacts" }

// V201: interruptor de exportaciones en la configuración de la empresa (solo esa columna, sin MODIFY de las demás).
type v201CompanyConfig struct {
	ID             uint `gorm:"primaryKey"`
	ExportsEnabled bool `gorm:"column:exports_enabled;default:false"`
}

func (v201CompanyConfig) TableName() string { return "tenant_company_configs" }
