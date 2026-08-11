package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V077GuiaSeriesSeed agrega series T001 (09) y V001 (31) en tenants que aún no las tienen.
// Solo provisioning/migración — nunca durante la emisión.
type V077GuiaSeriesSeed struct{}

func (V077GuiaSeriesSeed) Version() int { return 77 }
func (V077GuiaSeriesSeed) Name() string { return "guia_series_seed" }

func (V077GuiaSeriesSeed) Up(db *gorm.DB) error {
	return seedGuiaSeriesIfMissing(db)
}

func seedGuiaSeriesIfMissing(db *gorm.DB) error {
	var branch database.TenantBranch
	if err := db.Where("active = ?", true).Order("id ASC").First(&branch).Error; err != nil {
		return nil
	}
	if err := ensureGuiaSeries(db, branch.ID, "09", "GUIA_REMISION", "guia_remision", "T001"); err != nil {
		return err
	}
	return ensureGuiaSeries(db, branch.ID, "31", "GUIA_TRANSPORTISTA", "guia_transportista", "V001")
}

func ensureGuiaSeries(db *gorm.DB, branchID uint, sunatCode, docType, category, seriesCode string) error {
	var count int64
	if err := db.Model(&database.TenantDocumentSeries{}).
		Where("sunat_code = ?", sunatCode).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var dup int64
	if err := db.Model(&database.TenantDocumentSeries{}).
		Where("series = ?", seriesCode).
		Count(&dup).Error; err != nil {
		return err
	}
	if dup > 0 {
		return nil
	}
	row := database.TenantDocumentSeries{
		BranchID:    branchID,
		DocType:     docType,
		SunatCode:   sunatCode,
		Category:    category,
		Series:      seriesCode,
		Correlative: 1,
		Active:      true,
	}
	return db.Create(&row).Error
}
