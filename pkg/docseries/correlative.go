package docseries

import (
	"errors"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func mapSeriesLookupErr(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrSeriesNotFound
	}
	return err
}

// ValidateForBranch comprueba que la serie exista, esté activa y pertenezca a la sucursal indicada.
func ValidateForBranch(db *gorm.DB, seriesID, branchID uint) (database.TenantDocumentSeries, error) {
	var series database.TenantDocumentSeries
	if err := db.First(&series, seriesID).Error; err != nil {
		return series, mapSeriesLookupErr(err)
	}
	if !series.Active {
		return series, ErrSeriesInactive
	}
	if branchID > 0 && series.BranchID != branchID {
		return series, ErrSeriesWrongBranch
	}
	return series, nil
}

// ReserveNext asigna el correlativo actual y lo incrementa de forma atómica (SELECT … FOR UPDATE).
// Debe llamarse dentro de una transacción abierta.
func ReserveNext(tx *gorm.DB, seriesID uint) (correlative uint, series database.TenantDocumentSeries, err error) {
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&series, seriesID).Error; err != nil {
		return 0, series, mapSeriesLookupErr(err)
	}
	if !series.Active {
		return 0, series, ErrSeriesInactive
	}
	correlative = series.Correlative
	if err := tx.Model(&series).Update("correlative", series.Correlative+1).Error; err != nil {
		return 0, series, err
	}
	return correlative, series, nil
}

// ReserveNextStandalone reserva correlativo en su propia transacción (p. ej. notas de crédito administrativas).
func ReserveNextStandalone(db *gorm.DB, seriesID uint) (uint, error) {
	var next uint
	err := db.Transaction(func(tx *gorm.DB) error {
		n, _, err := ReserveNext(tx, seriesID)
		next = n
		return err
	})
	return next, err
}
