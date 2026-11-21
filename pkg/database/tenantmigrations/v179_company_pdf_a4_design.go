package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v179CompanyConfig struct {
	PdfA4Design string `gorm:"column:pdf_a4_design;size:30;not null;default:'modern'"`
}

func (v179CompanyConfig) TableName() string { return "tenant_company_configs" }

// V179CompanyPdfA4Design agrega el diseño del PDF A4 de comprobantes que usa el tenant ('modern' |
// 'classic' | los que se agreguen). Solo aditiva: las filas existentes quedan en 'modern', el diseño
// que ya veían. Idempotente.
type V179CompanyPdfA4Design struct{}

func (V179CompanyPdfA4Design) Version() int { return 179 }
func (V179CompanyPdfA4Design) Name() string { return "company_pdf_a4_design" }

func (V179CompanyPdfA4Design) Up(db *gorm.DB) error {
	mig := db.Migrator()
	st := &v179CompanyConfig{}
	if !mig.HasTable(st) {
		return nil
	}
	if mig.HasColumn(st, "PdfA4Design") {
		return nil
	}
	if err := mig.AddColumn(st, "PdfA4Design"); err != nil {
		return fmt.Errorf("add tenant_company_configs.pdf_a4_design: %w", err)
	}
	return nil
}
