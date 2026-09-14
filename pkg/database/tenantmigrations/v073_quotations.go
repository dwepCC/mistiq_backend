package tenantmigrations

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V073Quotations tablas de cotizaciones, enlace venta←cotización y series por defecto.
type V073Quotations struct{}

func (V073Quotations) Version() int { return 73 }
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

// seedQuotationSeries: el chequeo de disponibilidad de código original filtraba por
// "branch_id = ? AND series = ?" — pero V052SeriesGlobalUnique hace `series` único por TENANT
// completo, no por sucursal. Para un tenant con 2+ sucursales sin serie de cotización a la vez,
// la primera sucursal se quedaba con 'COT' y la segunda, al comprobar solo su propio branch_id,
// también creía tener 'COT' libre — violaba el índice único y abortaba la migración a mitad de
// camino (el resto de sucursales del bucle se quedaban sin serie). Encontrado en producción
// auditando fallos de migración enmascarados como éxito — industriassanma quedó así. Ahora usa
// nextAvailableDocumentSeries (chequeo tenant-wide, sin filtrar por branch_id).
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

		seriesCode, err := nextAvailableQuotationSeries(db)
		if err != nil {
			return fmt.Errorf("generar serie de cotización para sucursal %d: %w", b.ID, err)
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
			return fmt.Errorf("insertar serie %s para sucursal %d: %w", seriesCode, b.ID, err)
		}
	}
	return nil
}

// nextAvailableQuotationSeries: a diferencia de nextAvailableDocumentSeries (prefijo + 2 dígitos,
// usado por facturación electrónica), la serie de cotización histórica es "COT" a secas y su
// único fallback previo era "COT1" — se mantiene ese mismo formato (no BC## / FC##, que
// pertenece solo a notas de crédito) mientras se corrige el chequeo de disponibilidad para que
// sea tenant-wide y no se quede corto si hay más de 2 sucursales sin serie.
func nextAvailableQuotationSeries(db *gorm.DB) (string, error) {
	candidates := []string{"COT", "COT1", "COT2", "COT3", "COT4", "COT5", "COT6", "COT7", "COT8", "COT9"}
	for _, code := range candidates {
		var dup int64
		if err := db.Model(&database.TenantDocumentSeries{}).
			Where("series = ?", code).
			Count(&dup).Error; err != nil {
			return "", err
		}
		if dup == 0 {
			return code, nil
		}
	}
	return "", fmt.Errorf("sin código de cotización disponible (COT..COT9 agotado)")
}
