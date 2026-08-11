package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

var (
	ErrOperationTypeNotFound    = errors.New("tipo de operación no encontrado")
	ErrOperationTypeInactive    = errors.New("tipo de operación inactivo")
	ErrOperationTypeNotManual   = errors.New("tipo de operación no permitido para registro manual")
	ErrOperationDirectionMismatch = errors.New("el tipo de operación no corresponde a la dirección del documento")
)

// LookupOperationTypeByCode obtiene un tipo de operación activo por código interno.
func LookupOperationTypeByCode(db *gorm.DB, code string) (database.TenantInventoryOperationType, error) {
	code = strings.TrimSpace(strings.ToUpper(code))
	var row database.TenantInventoryOperationType
	err := db.Where("code = ? AND is_active = ?", code, true).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return row, ErrOperationTypeNotFound
		}
		return row, err
	}
	return row, nil
}

// LookupOperationTypeByID obtiene un tipo de operación activo por ID.
func LookupOperationTypeByID(db *gorm.DB, id uint) (database.TenantInventoryOperationType, error) {
	var row database.TenantInventoryOperationType
	err := db.Where("id = ? AND is_active = ?", id, true).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return row, ErrOperationTypeNotFound
		}
		return row, err
	}
	return row, nil
}

// ValidateDocumentOperationType comprueba que el tipo exista y coincida con la dirección del documento.
func ValidateDocumentOperationType(op database.TenantInventoryOperationType, documentDirection string) error {
	dir := strings.TrimSpace(strings.ToUpper(documentDirection))
	opDir := strings.TrimSpace(strings.ToUpper(op.Direction))
	if dir != "IN" && dir != "OUT" {
		return errors.New("dirección de documento inválida")
	}
	if opDir != dir {
		return ErrOperationDirectionMismatch
	}
	return nil
}

// ValidateManualOperationType valida tipo para documentos manuales (allow_manual + dirección).
func ValidateManualOperationType(op database.TenantInventoryOperationType, documentDirection string) error {
	if err := ValidateDocumentOperationType(op, documentDirection); err != nil {
		return err
	}
	if !op.AllowManual {
		return ErrOperationTypeNotManual
	}
	return nil
}
