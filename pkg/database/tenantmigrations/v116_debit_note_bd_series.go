package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V116DebitNoteBDSeries agrega la serie BD01 (nota de débito que aumenta el valor de una
// boleta) a las sucursales que ya tienen FD01 pero nunca recibieron su contraparte —
// seedDocumentSeries solo sembraba FD01, así que en producción no existía ninguna serie
// para emitir una ND sobre una boleta. Ver docseries.DebitNoteSeriesPrefixForAffected.
type V116DebitNoteBDSeries struct{}

func (V116DebitNoteBDSeries) Version() int { return 116 }
func (V116DebitNoteBDSeries) Name() string { return "debit_note_bd_series" }

func (V116DebitNoteBDSeries) Up(db *gorm.DB) error {
	var branches []database.TenantBranch
	if err := db.Where("active = ?", true).Find(&branches).Error; err != nil {
		return err
	}
	for _, b := range branches {
		var hasFD, hasBD int64
		db.Model(&database.TenantDocumentSeries{}).
			Where("branch_id = ? AND category = ? AND series = ?", b.ID, "nota_debito", "FD01").
			Count(&hasFD)
		if hasFD == 0 {
			// Sucursal sin FD01 (creada antes de que existiera nota_debito, o sembrada
			// distinto): no asumir el esquema de nomenclatura, no crear nada acá.
			continue
		}
		db.Model(&database.TenantDocumentSeries{}).
			Where("branch_id = ? AND category = ? AND series = ?", b.ID, "nota_debito", "BD01").
			Count(&hasBD)
		if hasBD > 0 {
			continue
		}
		row := database.TenantDocumentSeries{
			BranchID:    b.ID,
			DocType:     "NOTA_DEBITO",
			SunatCode:   "08",
			Category:    "nota_debito",
			Series:      "BD01",
			Correlative: 1,
			Active:      true,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
