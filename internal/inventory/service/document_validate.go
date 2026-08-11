package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"
)

const (
	DocumentStatusDraft     = "draft"
	DocumentStatusConfirmed = "confirmed"
	DocumentStatusVoided    = "voided"
)

var (
	ErrDocumentNotFound         = errors.New("documento de inventario no encontrado")
	ErrDocumentNotDraft         = errors.New("solo se pueden modificar documentos en borrador")
	ErrDocumentAlreadyConfirmed = errors.New("el documento ya se encuentra confirmado")
	ErrDocumentAlreadyVoided    = errors.New("el documento ya se encuentra anulado")
	ErrDocumentNotConfirmed     = errors.New("solo se pueden anular documentos confirmados")
	ErrDocumentLinesRequired    = errors.New("el documento debe tener al menos una línea")
	ErrReferenceRequired        = errors.New("referencia de documento requerida para este tipo de operación")
	ErrInvalidDocumentDirection = errors.New("dirección de documento inválida; use IN u OUT")
)

// ValidateDocumentStatusForUpdate exige estado borrador para edición.
func ValidateDocumentStatusForUpdate(status string) error {
	if strings.TrimSpace(strings.ToLower(status)) != DocumentStatusDraft {
		if strings.TrimSpace(strings.ToLower(status)) == DocumentStatusConfirmed {
			return ErrDocumentAlreadyConfirmed
		}
		if strings.TrimSpace(strings.ToLower(status)) == DocumentStatusVoided {
			return ErrDocumentAlreadyVoided
		}
		return ErrDocumentNotDraft
	}
	return nil
}

// ValidateDocumentStatusForConfirm exige borrador e impide confirmación duplicada.
func ValidateDocumentStatusForConfirm(status string) error {
	s := strings.TrimSpace(strings.ToLower(status))
	switch s {
	case DocumentStatusDraft:
		return nil
	case DocumentStatusConfirmed:
		return ErrDocumentAlreadyConfirmed
	case DocumentStatusVoided:
		return ErrDocumentAlreadyVoided
	default:
		return errors.New("estado de documento inválido")
	}
}

// ValidateDocumentStatusForVoid exige confirmado e impide anulación duplicada.
func ValidateDocumentStatusForVoid(status string) error {
	s := strings.TrimSpace(strings.ToLower(status))
	switch s {
	case DocumentStatusConfirmed:
		return nil
	case DocumentStatusVoided:
		return ErrDocumentAlreadyVoided
	case DocumentStatusDraft:
		return ErrDocumentNotConfirmed
	default:
		return errors.New("estado de documento inválido")
	}
}

// ValidateDocumentDirection normaliza y valida IN|OUT.
func ValidateDocumentDirection(direction string) (string, error) {
	dir := strings.TrimSpace(strings.ToUpper(direction))
	if dir != "IN" && dir != "OUT" {
		return "", ErrInvalidDocumentDirection
	}
	return dir, nil
}

// ValidateDocumentHeader valida cabecera + tipo de operación (único punto para direction, allow_manual, requires_document).
func ValidateDocumentHeader(
	op database.TenantInventoryOperationType,
	documentDirection string,
	reference string,
	manual bool,
) error {
	dir, err := ValidateDocumentDirection(documentDirection)
	if err != nil {
		return err
	}
	if manual {
		if err := ValidateManualOperationType(op, dir); err != nil {
			return err
		}
	} else if err := ValidateDocumentOperationType(op, dir); err != nil {
		return err
	}
	if op.RequiresDocument && strings.TrimSpace(reference) == "" {
		return ErrReferenceRequired
	}
	return nil
}

// ValidateDocumentLines valida líneas de detalle antes de confirmar.
func ValidateDocumentLines(lines []DocumentLineInput, direction string) error {
	if len(lines) == 0 {
		return ErrDocumentLinesRequired
	}
	dir, err := ValidateDocumentDirection(direction)
	if err != nil {
		return err
	}
	for i, line := range lines {
		if line.ProductID == 0 {
			return errors.New("producto requerido en cada línea")
		}
		if line.Quantity <= 0 {
			return errors.New("cantidad debe ser mayor a cero en cada línea")
		}
		if dir == "IN" && line.UnitCost < 0 {
			return errors.New("costo unitario no puede ser negativo en ingresos")
		}
		_ = i
	}
	return nil
}
