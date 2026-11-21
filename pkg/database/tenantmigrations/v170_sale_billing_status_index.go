package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

const idxSaleBillingStatus = "idx_tenant_sales_billing_status"

type v170Sale struct {
	BillingStatus string `gorm:"column:billing_status;size:30;index:idx_tenant_sales_billing_status"`
}

func (v170Sale) TableName() string { return "tenant_sales" }

// V170SaleBillingStatusIndex agrega un índice sobre tenant_sales.billing_status.
//
// Sin él, filtrar ventas por estado de envío (campanita de pendientes del header del tenant, el
// resumen fiscal del panel central, el worker de conciliación) recorre toda la tabla. Es solo un
// índice: no cambia datos ni columnas. Idempotente.
type V170SaleBillingStatusIndex struct{}

func (V170SaleBillingStatusIndex) Version() int { return 170 }
func (V170SaleBillingStatusIndex) Name() string { return "sale_billing_status_index" }

func (V170SaleBillingStatusIndex) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable("tenant_sales") {
		return nil
	}
	st := &v170Sale{}
	if mig.HasIndex(st, idxSaleBillingStatus) {
		return nil
	}
	if err := mig.CreateIndex(st, idxSaleBillingStatus); err != nil {
		return fmt.Errorf("crear %s: %w", idxSaleBillingStatus, err)
	}
	return nil
}
