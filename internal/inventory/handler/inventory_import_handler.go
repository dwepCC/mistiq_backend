package handler

import (
	"tukifac/internal/inventory/service"
	"tukifac/pkg/branch"

	"github.com/gofiber/fiber/v3"
)

// ImportAdjustmentPreviewAPI valida filas y devuelve vista previa con valorización.
func (h *InventoryHandler) ImportAdjustmentPreviewAPI(c fiber.Ctx) error {
	var body struct {
		BranchID uint `json:"branch_id"`
		Rows     []struct {
			RowNumber int     `json:"row_number"`
			Barcode   string  `json:"barcode"`
			NewStock  float64 `json:"new_stock"`
		} `json:"rows"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "payload inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	rows := make([]service.ImportAdjustmentRowInput, 0, len(body.Rows))
	for _, r := range body.Rows {
		rows = append(rows, service.ImportAdjustmentRowInput{
			RowNumber: r.RowNumber,
			Barcode:   r.Barcode,
			NewStock:  r.NewStock,
		})
	}
	result, err := documentService(c).PreviewImportAdjustment(service.ImportAdjustmentPreviewInput{
		BranchID: branchID,
		Rows:     rows,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": result})
}

// ImportAdjustmentConfirmAPI confirma el ajuste masivo (máx. 2 documentos).
func (h *InventoryHandler) ImportAdjustmentConfirmAPI(c fiber.Ctx) error {
	var body struct {
		BranchID       uint   `json:"branch_id"`
		MovementReason string `json:"movement_reason"`
		Notes          string `json:"notes"`
		Rows           []struct {
			RowNumber int     `json:"row_number"`
			Barcode   string  `json:"barcode"`
			NewStock  float64 `json:"new_stock"`
		} `json:"rows"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "payload inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	rows := make([]service.ImportAdjustmentRowInput, 0, len(body.Rows))
	for _, r := range body.Rows {
		rows = append(rows, service.ImportAdjustmentRowInput{
			RowNumber: r.RowNumber,
			Barcode:   r.Barcode,
			NewStock:  r.NewStock,
		})
	}
	result, err := documentService(c).ConfirmImportAdjustment(service.ImportAdjustmentConfirmInput{
		BranchID:       branchID,
		MovementReason: body.MovementReason,
		Notes:          body.Notes,
		Rows:           rows,
	}, userID(c))
	if err != nil {
		code, msg := mapDocumentError(err)
		return c.Status(code).JSON(fiber.Map{"error": msg})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": result})
}
