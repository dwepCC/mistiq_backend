package database

import "time"

// TenantFiscalDaily: volumen de comprobantes electrónicos de cada tenant por día de emisión,
// tipo (01/03/07/08/09/31) y estado de envío (pending/sent/accepted/rejected/error).
//
// Es un RESUMEN en la BD central, alimentado por el job periódico de internal/fiscal/summary que
// lee —solo lectura— la BD de cada tenant. Permite filtrar por fecha/tipo/RUC desde el panel
// central sin recorrer las bases de los tenants. Ver docs/PLAN-RESUMEN-FISCAL-POR-TENANT.md.
type TenantFiscalDaily struct {
	ID       uint `gorm:"primaryKey"`
	TenantID uint `gorm:"not null;uniqueIndex:uk_tenant_fiscal_daily,priority:1;index:idx_tenant_fiscal_daily_tenant_day,priority:1"`
	// Day es el día de issue_date (YYYY-MM-DD) tal como lo guarda el tenant.
	Day           string    `gorm:"type:date;not null;uniqueIndex:uk_tenant_fiscal_daily,priority:2;index:idx_tenant_fiscal_daily_tenant_day,priority:2;index:idx_tenant_fiscal_daily_day"`
	DocCode       string    `gorm:"size:4;not null;uniqueIndex:uk_tenant_fiscal_daily,priority:3"`
	BillingStatus string    `gorm:"size:20;not null;uniqueIndex:uk_tenant_fiscal_daily,priority:4"`
	Cnt           int       `gorm:"not null;default:0"`
	Amount        float64   `gorm:"type:decimal(15,2);not null;default:0"`
	RefreshedAt   time.Time `gorm:"not null"`
}

func (TenantFiscalDaily) TableName() string { return "tenant_fiscal_daily" }

// TenantFiscalHealth: una fila por tenant con el estado actual de su facturación.
//
// open_* = comprobantes que aún no terminaron (de CUALQUIER antigüedad): se calculan sumando lo
// abierto dentro de la ventana de 60 días (se recalcula cada ciclo) más lo abierto más antiguo
// (older_*, que solo recalcula la pasada profunda nocturna: leer tenant_sales por billing_status
// no tiene índice).
type TenantFiscalHealth struct {
	TenantID  uint      `gorm:"primaryKey;autoIncrement:false"`
	ScannedAt time.Time `gorm:"not null"`
	ScanMs    int       `gorm:"not null;default:0"`
	// ScanError no vacío = el último escaneo de este tenant falló (esquema viejo, timeout, etc.).
	ScanError string `gorm:"type:text"`

	OpenPending  int `gorm:"not null;default:0"`
	OpenSent     int `gorm:"not null;default:0"`
	OpenError    int `gorm:"not null;default:0"`
	OpenRejected int `gorm:"not null;default:0"`
	// OldestOpenAt: fecha de emisión del comprobante pending/error más antiguo.
	OldestOpenAt *time.Time
	LastIssueAt  *time.Time

	OlderOpenPending  int `gorm:"not null;default:0"`
	OlderOpenSent     int `gorm:"not null;default:0"`
	OlderOpenError    int `gorm:"not null;default:0"`
	OlderOpenRejected int `gorm:"not null;default:0"`
	OlderOldestAt     *time.Time
	LastDeepAt        *time.Time
}

func (TenantFiscalHealth) TableName() string { return "tenant_fiscal_health" }
