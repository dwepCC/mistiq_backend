package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type v114DocumentSeries struct {
	IsDefault bool `gorm:"column:is_default"`
}

func (v114DocumentSeries) TableName() string { return "tenant_document_series" }

// V114DocumentSeriesIsDefault agrega tenant_document_series.is_default (comprobante preferido
// por sucursal al iniciar una venta en POS/registro de ventas).
//
// Antes no existía ninguna preferencia configurable: el frontend elegía el comprobante inicial
// buscando a mano la serie con sunat_code = '00' (Nota de Venta) en varios lugares distintos de
// cada app. Este backfill marca esa misma serie como is_default en cada sucursal para que el
// comportamiento actual quede exactamente igual tras migrar — nadie tiene que reconfigurar nada.
type V114DocumentSeriesIsDefault struct{}

func (V114DocumentSeriesIsDefault) Version() int { return 114 }
func (V114DocumentSeriesIsDefault) Name() string { return "document_series_is_default" }

func (V114DocumentSeriesIsDefault) Up(db *gorm.DB) error {
	mig := db.Migrator()
	row := &v114DocumentSeries{}
	if !mig.HasColumn(row, "IsDefault") {
		if err := mig.AddColumn(row, "IsDefault"); err != nil {
			return fmt.Errorf("add tenant_document_series.is_default: %w", err)
		}
	}

	// Backfill: por cada sucursal, la serie de Nota de Venta activa más antigua (la que ya se
	// usaba como default de facto) queda marcada is_default. Si una sucursal no tiene ninguna
	// serie '00' activa, queda sin default — el frontend sigue con su fallback (primera serie
	// de la lista) hasta que alguien elija una explícitamente en Ajustes → Series.
	var rows []database.TenantDocumentSeries
	if err := db.Where("sunat_code = ? AND active = ?", "00", true).
		Order("branch_id ASC, id ASC").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("v114 leer series nota de venta: %w", err)
	}
	seenBranch := make(map[uint]bool, len(rows))
	for _, r := range rows {
		if seenBranch[r.BranchID] {
			continue
		}
		seenBranch[r.BranchID] = true
		if err := db.Model(&database.TenantDocumentSeries{}).
			Where("id = ?", r.ID).
			Update("is_default", true).Error; err != nil {
			return fmt.Errorf("v114 backfill is_default sucursal %d: %w", r.BranchID, err)
		}
	}
	return nil
}
