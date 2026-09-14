package tenantmigrations

import (
	"fmt"
	"log"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V062CreditNoteBCSeries agrega serie BCxx por sucursal si falta (NC para anular boletas).
type V062CreditNoteBCSeries struct{}

func (V062CreditNoteBCSeries) Version() int { return 62 }
func (V062CreditNoteBCSeries) Name() string {
	return "credit_note_bc_series"
}

// Up: un INSERT...SELECT masivo original asignaba el literal 'BC01' a TODAS las sucursales que
// lo necesitaran en una sola pasada — para un tenant con 2+ sucursales sin serie BC simultánea,
// eso intentaba insertar 'BC01' dos veces y violaba el índice único de V052SeriesGlobalUnique
// (serie única por TENANT, no por sucursal), abortando la migración para ese tenant a mitad de
// camino. Ahora recorre las sucursales una por una y genera un código BCxx distinto para cada
// una que lo necesite (ver nextAvailableDocumentSeries). Encontrado en producción auditando
// fallos de migración enmascarados como éxito — doriconta quedó así, con esta migración
// realmente fallida.
func (V062CreditNoteBCSeries) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_document_series") || !db.Migrator().HasTable("tenant_branches") {
		return nil
	}

	var branches []database.TenantBranch
	if err := db.Where("active = ?", true).Find(&branches).Error; err != nil {
		return fmt.Errorf("listar sucursales: %w", err)
	}

	created := 0
	for _, b := range branches {
		var count int64
		if err := db.Model(&database.TenantDocumentSeries{}).
			Where("branch_id = ? AND category = ? AND UPPER(series) LIKE ?", b.ID, "nota_credito", "BC%").
			Count(&count).Error; err != nil {
			return fmt.Errorf("comprobar serie BC de sucursal %d: %w", b.ID, err)
		}
		if count > 0 {
			continue
		}

		seriesCode, err := nextAvailableDocumentSeries(db, "BC")
		if err != nil {
			return fmt.Errorf("generar serie BC para sucursal %d: %w", b.ID, err)
		}

		row := database.TenantDocumentSeries{
			BranchID:    b.ID,
			DocType:     "NOTA_CREDITO",
			SunatCode:   "07",
			Category:    "nota_credito",
			Series:      seriesCode,
			Correlative: 1,
			Active:      true,
		}
		if err := db.Create(&row).Error; err != nil {
			return fmt.Errorf("insertar serie %s para sucursal %d: %w", seriesCode, b.ID, err)
		}
		created++
	}
	if created > 0 {
		log.Printf("[v062] tenant: %d sucursal(es) con nueva serie BC (nota de crédito boletas)", created)
	}
	return nil
}
