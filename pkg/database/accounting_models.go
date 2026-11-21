package database

import "time"

// ── Módulo Contabilidad (accounting) ─────────────────────────────────────────────────────────────
//
// Tablas de tenant del módulo «accounting» (V174). Ninguna otra parte del sistema las usa: el módulo
// solo LEE ventas, compras, caja, CxC y CxP y escribe únicamente aquí.

// TenantAccountingSettings cuentas contables de los formatos de exportación (una sola fila por tenant).
// Son TEXTO (no enteros): una cuenta como «0121» no puede perder el cero inicial. Vacío = se usa el
// valor por defecto del PCGE (ver internal/accounting/service.DefaultAccounts).
type TenantAccountingSettings struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// Ventas, por moneda.
	ReceivablePEN string `gorm:"size:20" json:"receivable_pen"` // cuenta por cobrar (total)
	ReceivableUSD string `gorm:"size:20" json:"receivable_usd"`
	IgvSalesPEN   string `gorm:"size:20" json:"igv_sales_pen"`
	IgvSalesUSD   string `gorm:"size:20" json:"igv_sales_usd"`
	TaxedPEN      string `gorm:"size:20" json:"taxed_pen"` // ingreso operaciones gravadas
	TaxedUSD      string `gorm:"size:20" json:"taxed_usd"`
	Exonerated    string `gorm:"size:20" json:"exonerated"`
	Unaffected    string `gorm:"size:20" json:"unaffected"`
	Exportation   string `gorm:"size:20" json:"exportation"`
	Icbper        string `gorm:"size:20" json:"icbper"`
	// Compras.
	PayablePEN   string `gorm:"size:20" json:"payable_pen"` // cuenta por pagar (total)
	PayableUSD   string `gorm:"size:20" json:"payable_usd"`
	IgvPurchases string `gorm:"size:20" json:"igv_purchases"`
	PurchaseCost string `gorm:"size:20" json:"purchase_cost"` // compras / gasto
	// SubdiarioSales: código de subdiario de ventas para Concar (por defecto «05»).
	SubdiarioSales string    `gorm:"size:5" json:"subdiario_sales"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (TenantAccountingSettings) TableName() string { return "tenant_accounting_settings" }

// TenantAccountingAccount plan de cuentas (4 niveles) del Libro Mayor. Se siembra con un plan propio
// (equivalente al del sistema anterior) y es de solo lectura en la primera entrega.
type TenantAccountingAccount struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Code string `gorm:"size:30;not null;uniqueIndex" json:"code"`
	Name string `gorm:"size:255;not null" json:"name"`
	// Kind: asset | liability | equity | income | expense — define el signo del saldo.
	Kind   string `gorm:"size:20;not null;default:'asset'" json:"kind"`
	Active bool   `gorm:"default:true" json:"active"`
}

func (TenantAccountingAccount) TableName() string { return "tenant_accounting_accounts" }

// TenantSireSettings credenciales SIRE (una fila). Los secretos se guardan CIFRADOS (pkg/secrets,
// AES-256-GCM) y nunca se devuelven al navegador: la API solo informa si están configurados.
type TenantSireSettings struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	ClientID        string    `gorm:"size:100" json:"client_id"`
	ClientSecretEnc string    `gorm:"type:text" json:"-"`
	SolUsername     string    `gorm:"size:50" json:"sol_username"`
	SolPasswordEnc  string    `gorm:"type:text" json:"-"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (TenantSireSettings) TableName() string { return "tenant_sire_settings" }

// TenantSireTicket historial de tickets SIRE (antes solo vivía en el localStorage del navegador).
type TenantSireTicket struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	Book       string `gorm:"size:10;not null;index:idx_sire_ticket_book_period" json:"book"`      // sale | purchase
	Period     string `gorm:"size:6;not null;index:idx_sire_ticket_book_period" json:"period"`     // YYYYMM
	Ticket     string `gorm:"size:60;not null" json:"ticket"`
	StatusCode string `gorm:"size:5" json:"status_code"` // 01..06 (ver internal/accounting/sire)
	StatusText string `gorm:"size:100" json:"status_text"`
	FileName   string `gorm:"size:200" json:"file_name"`
	UserID     uint   `gorm:"index" json:"user_id"`
	// AcceptedAt: cuándo se aceptó la propuesta (solo ventas).
	AcceptedAt *time.Time `json:"accepted_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (TenantSireTicket) TableName() string { return "tenant_sire_tickets" }
