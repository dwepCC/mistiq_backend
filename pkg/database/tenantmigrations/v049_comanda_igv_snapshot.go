package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V049ComandaIgvSnapshot guarda afectación IGV e «incluye IGV» por línea (ítems manuales y snapshot de catálogo).
type V049ComandaIgvSnapshot struct{}

func (V049ComandaIgvSnapshot) Version() int { return 49 }
func (V049ComandaIgvSnapshot) Name() string { return "comanda_igv_snapshot" }

func (V049ComandaIgvSnapshot) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_comandas") {
		return nil
	}
	if !db.Migrator().HasColumn("tenant_comandas", "igv_affectation_type") {
		if err := db.Exec(`ALTER TABLE tenant_comandas ADD COLUMN igv_affectation_type VARCHAR(10) NOT NULL DEFAULT '10'`).Error; err != nil {
			return fmt.Errorf("tenant_comandas.igv_affectation_type: %w", err)
		}
	}
	if !db.Migrator().HasColumn("tenant_comandas", "price_includes_igv") {
		if err := db.Exec(`ALTER TABLE tenant_comandas ADD COLUMN price_includes_igv TINYINT(1) NOT NULL DEFAULT 1`).Error; err != nil {
			return fmt.Errorf("tenant_comandas.price_includes_igv: %w", err)
		}
	}
	return nil
}
