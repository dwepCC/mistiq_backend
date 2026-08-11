package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v111Sale struct {
	ReissuedAt       *time.Time `gorm:"column:reissued_at;index"`
	ReissuedFromDate *time.Time `gorm:"column:reissued_from_date"`
	ReissueReason    string     `gorm:"column:reissue_reason;type:text"`
	ReissuedByEmail  string     `gorm:"column:reissued_by_email;size:255"`
	ReissueCount     int        `gorm:"column:reissue_count;default:0"`
}

func (v111Sale) TableName() string { return "tenant_sales" }

// V111SaleReissueAudit rastro de reemisión fiscal de una venta.
//
// Cubre el caso de comprobantes emitidos contra SUNAT beta que luego hay que
// llevar a producción: se conserva la numeración y se reenvía con otra fecha de
// emisión. Como esa fecha sobrescribe issue_date, aquí queda la original junto
// con el motivo y el correo del soporte que ejecutó la corrección.
type V111SaleReissueAudit struct{}

func (V111SaleReissueAudit) Version() int { return 111 }
func (V111SaleReissueAudit) Name() string { return "sale_reissue_audit" }

func (V111SaleReissueAudit) Up(db *gorm.DB) error {
	mig := db.Migrator()
	sale := &v111Sale{}

	for _, col := range []string{"ReissuedAt", "ReissuedFromDate", "ReissueReason", "ReissuedByEmail", "ReissueCount"} {
		if mig.HasColumn(sale, col) {
			continue
		}
		if err := mig.AddColumn(sale, col); err != nil {
			return fmt.Errorf("add tenant_sales.%s: %w", col, err)
		}
	}

	return nil
}
