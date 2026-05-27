package gormutil

import "gorm.io/gorm"

// PersistBoolWithDefault corrige un comportamiento de GORM: si un bool tiene `default:true`
// en el tag y el valor es false, el INSERT puede omitir la columna y MySQL aplica el default (true).
// Llamar justo después de Create cuando el valor debe quedar en false.
func PersistBoolWithDefault(tx *gorm.DB, model interface{}, column string, value bool) error {
	if value {
		return nil
	}
	return tx.Model(model).UpdateColumn(column, false).Error
}
