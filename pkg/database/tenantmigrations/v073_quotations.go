package tenantmigrations

import (
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V073Quotations tablas de cotizaciones, enlace venta←cotización y series por defecto.
type V073Quotations struct{}

func (V073Quotations) Version() int  { return 73 }
func (V073Quotations) Name() string { return "quotations" }

func (V073Quotations) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&database.TenantQuotation{},
		&database.TenantQuotationItem{},
		&database.TenantSale{},
	); err != nil {
		return err
	}
	return seedQuotationSeries(db)
}

func seedQuotationSeries(db *gorm.DB) error {
	var branches []database.TenantBranch
	if err := db.Where("active = ?", true).Find(&branches).Error; err != nil {
		return err
	}
	for _, b := range branches {
		var count int64
		db.Model(&database.TenantDocumentSeries{}).
			Where("branch_id = ? AND category = ?", b.ID, "cotizacion").
			Count(&count)
		if count > 0 {
			continue
		}
		seriesCode := "COT"
		var dup int64
		db.Model(&database.TenantDocumentSeries{}).
			Where("branch_id = ? AND series = ?", b.ID, seriesCode).
			Count(&dup)
		if dup > 0 {
			seriesCode = "COT1"
		}
		row := database.TenantDocumentSeries{
			BranchID:    b.ID,
			DocType:     "Cotización",
			SunatCode:   "QT",
			Category:    "cotizacion",
			Series:      strings.ToUpper(seriesCode),
			Correlative: 1,
			Active:      true,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
