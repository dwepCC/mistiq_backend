package handler

import (
	"errors"
	"strconv"
	"time"

	"tukifac/internal/inventory/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

func documentService(c fiber.Ctx) *service.InventoryDocumentService {
	return service.NewInventoryDocumentService(db(c))
}

func mapDocumentError(err error) (int, string) {
	switch {
	case errors.Is(err, service.ErrDocumentNotFound):
		return fiber.StatusNotFound, err.Error()
	case errors.Is(err, service.ErrDocumentAlreadyConfirmed),
		errors.Is(err, service.ErrDocumentAlreadyVoided),
		errors.Is(err, service.ErrDocumentNotConfirmed),
		errors.Is(err, service.ErrDocumentNotDraft):
		return fiber.StatusConflict, err.Error()
	case errors.Is(err, service.ErrOperationTypeNotFound),
		errors.Is(err, service.ErrOperationDirectionMismatch),
		errors.Is(err, service.ErrOperationTypeNotManual),
		errors.Is(err, service.ErrReferenceRequired),
		errors.Is(err, service.ErrDocumentLinesRequired):
		return fiber.StatusBadRequest, err.Error()
	default:
		return fiber.StatusBadRequest, err.Error()
	}
}

// OperationTypesAPI lista tipos de operación (solo lectura).
func (h *InventoryHandler) OperationTypesAPI(c fiber.Ctx) error {
	manualOnly := c.Query("manual") == "1" || c.Query("manual") == "true"
	rows, err := documentService(c).ListOperationTypes(c.Query("direction"), manualOnly)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// DocumentsListAPI lista documentos de inventario.
func (h *InventoryHandler) DocumentsListAPI(c fiber.Ctx) error {
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	reqB, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqB))
	params := service.DocumentListParams{
		Direction: c.Query("direction"),
		Status:    c.Query("status"),
		BranchID:  uint(branchID),
	}
	if perPage > 0 {
		params.Limit = perPage
		params.Offset = (page - 1) * perPage
	}
	docs, total, err := documentService(c).ListInventoryDocuments(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": docs, "total": total})
}

// DocumentGetAPI detalle de documento.
func (h *InventoryHandler) DocumentGetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	doc, lines, err := documentService(c).GetInventoryDocument(uint(id))
	if err != nil {
		code, msg := mapDocumentError(err)
		return c.Status(code).JSON(fiber.Map{"error": msg})
	}
	return c.JSON(fiber.Map{"data": doc, "lines": lines})
}

// DocumentCreateAPI crea borrador.
func (h *InventoryHandler) DocumentCreateAPI(c fiber.Ctx) error {
	var body struct {
		Direction       string `json:"direction"`
		OperationTypeID uint   `json:"operation_type_id"`
		BranchID        uint   `json:"branch_id"`
		DocumentDate    string `json:"document_date"`
		Reference       string `json:"reference"`
		MovementReason  string `json:"movement_reason"`
		Notes           string `json:"notes"`
		Lines           []struct {
			ProductID uint     `json:"product_id"`
			Quantity  float64  `json:"quantity"`
			UnitCost  float64  `json:"unit_cost"`
			Serials   []string `json:"serials"`
		} `json:"lines"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "payload inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	docDate := time.Now()
	if body.DocumentDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", body.DocumentDate, time.Local); err == nil {
			docDate = t
		}
	}
	lines := make([]service.DocumentLineInput, 0, len(body.Lines))
	for _, l := range body.Lines {
		lines = append(lines, service.DocumentLineInput{
			ProductID: l.ProductID,
			Quantity:  l.Quantity,
			UnitCost:  l.UnitCost,
			Serials:   l.Serials,
		})
	}
	id, err := documentService(c).CreateInventoryDocument(service.CreateDocumentInput{
		Direction:       body.Direction,
		OperationTypeID: body.OperationTypeID,
		BranchID:        branchID,
		DocumentDate:    docDate,
		Reference:       body.Reference,
		MovementReason:  body.MovementReason,
		Notes:           body.Notes,
		Lines:           lines,
		UserID:          userID(c),
	})
	if err != nil {
		code, msg := mapDocumentError(err)
		return c.Status(code).JSON(fiber.Map{"error": msg})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"ok": true, "id": id})
}

// DocumentUpdateAPI edita borrador.
func (h *InventoryHandler) DocumentUpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		OperationTypeID uint   `json:"operation_type_id"`
		DocumentDate    string `json:"document_date"`
		Reference       string `json:"reference"`
		MovementReason  string `json:"movement_reason"`
		Notes           string `json:"notes"`
		Lines           []struct {
			ProductID uint     `json:"product_id"`
			Quantity  float64  `json:"quantity"`
			UnitCost  float64  `json:"unit_cost"`
			Serials   []string `json:"serials"`
		} `json:"lines"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "payload inválido"})
	}
	docDate := time.Time{}
	if body.DocumentDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", body.DocumentDate, time.Local); err == nil {
			docDate = t
		}
	}
	lines := make([]service.DocumentLineInput, 0, len(body.Lines))
	for _, l := range body.Lines {
		lines = append(lines, service.DocumentLineInput{
			ProductID: l.ProductID,
			Quantity:  l.Quantity,
			UnitCost:  l.UnitCost,
			Serials:   l.Serials,
		})
	}
	err = documentService(c).UpdateInventoryDocument(uint(id), service.UpdateDocumentInput{
		OperationTypeID: body.OperationTypeID,
		DocumentDate:    docDate,
		Reference:       body.Reference,
		MovementReason:  body.MovementReason,
		Notes:           body.Notes,
		Lines:           lines,
	})
	if err != nil {
		code, msg := mapDocumentError(err)
		return c.Status(code).JSON(fiber.Map{"error": msg})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// DocumentConfirmAPI confirma documento.
func (h *InventoryHandler) DocumentConfirmAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	err = documentService(c).ConfirmInventoryDocument(uint(id), userID(c))
	if err != nil {
		code, msg := mapDocumentError(err)
		return c.Status(code).JSON(fiber.Map{"error": msg})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// DocumentVoidAPI anula documento confirmado.
func (h *InventoryHandler) DocumentVoidAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	err = documentService(c).VoidInventoryDocument(uint(id), userID(c))
	if err != nil {
		code, msg := mapDocumentError(err)
		return c.Status(code).JSON(fiber.Map{"error": msg})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// enrichMovementsWithOperationTypes agrega datos del catálogo al kardex.
func enrichMovementsWithOperationTypes(tdb *gorm.DB, movements []database.TenantStockMovement) map[uint]database.TenantInventoryOperationType {
	opIDs := make(map[uint]struct{})
	for _, m := range movements {
		if m.OperationTypeID != nil {
			opIDs[*m.OperationTypeID] = struct{}{}
		}
	}
	out := make(map[uint]database.TenantInventoryOperationType)
	if len(opIDs) == 0 {
		return out
	}
	ids := make([]uint, 0, len(opIDs))
	for id := range opIDs {
		ids = append(ids, id)
	}
	var ops []database.TenantInventoryOperationType
	if tdb.Where("id IN ?", ids).Find(&ops).Error == nil {
		for _, o := range ops {
			out[o.ID] = o
		}
	}
	return out
}
