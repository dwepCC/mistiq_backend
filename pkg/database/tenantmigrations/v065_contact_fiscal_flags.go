package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v065TenantContact struct {
	EsAgenteDeRetencion             bool `gorm:"column:es_agente_de_retencion;default:false"`
	EsAgenteDePercepcion            bool `gorm:"column:es_agente_de_percepcion;default:false"`
	EsAgenteDePercepcionCombustible bool `gorm:"column:es_agente_de_percepcion_combustible;default:false"`
	EsBuenContribuyente             bool `gorm:"column:es_buen_contribuyente;default:false"`
}

func (v065TenantContact) TableName() string { return "tenant_contacts" }

// V065ContactFiscalFlags flags fiscales del contacto (agente retención/percepción, buen contribuyente).
type V065ContactFiscalFlags struct{}

func (V065ContactFiscalFlags) Version() int { return 65 }
func (V065ContactFiscalFlags) Name() string { return "contact_fiscal_flags" }

func (V065ContactFiscalFlags) Up(db *gorm.DB) error {
	mig := db.Migrator()
	cfg := &v065TenantContact{}
	if !mig.HasTable(cfg) {
		return nil
	}
	cols := []struct {
		name string
		col  string
	}{
		{"EsAgenteDeRetencion", "es_agente_de_retencion"},
		{"EsAgenteDePercepcion", "es_agente_de_percepcion"},
		{"EsAgenteDePercepcionCombustible", "es_agente_de_percepcion_combustible"},
		{"EsBuenContribuyente", "es_buen_contribuyente"},
	}
	for _, c := range cols {
		if !mig.HasColumn(cfg, c.name) {
			if err := mig.AddColumn(cfg, c.name); err != nil {
				return fmt.Errorf("add tenant_contacts.%s: %w", c.col, err)
			}
		}
	}
	return nil
}
