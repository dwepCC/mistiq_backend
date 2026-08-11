package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/docseries"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InventoryDocumentService dominio de documentos de ingreso/egreso de inventario.
type InventoryDocumentService struct {
	db *gorm.DB
}

const (
	DocumentSourceManual     = "MANUAL"
	DocumentSourceAdjustment = "ADJUSTMENT"
	DocumentSourceImport     = "IMPORT"
)

func normalizeDocumentSource(source string) string {
	switch strings.TrimSpace(strings.ToUpper(source)) {
	case DocumentSourceManual, DocumentSourceAdjustment, DocumentSourceImport:
		return strings.TrimSpace(strings.ToUpper(source))
	default:
		return DocumentSourceManual
	}
}

func NewInventoryDocumentService(db *gorm.DB) *InventoryDocumentService {
	return &InventoryDocumentService{db: db}
}

// DocumentLineInput línea de detalle para crear/actualizar documentos.
type DocumentLineInput struct {
	ProductID uint
	// PresentationID: variante/presentación afectada (ej. color), cuando el producto vende por
	// presentación con stock propio (product.HasVariants).
	PresentationID *uint
	Quantity       float64
	UnitCost       float64
	Serials        []string
}

// CreateDocumentInput datos para crear un borrador.
type CreateDocumentInput struct {
	Direction       string
	OperationTypeID uint
	BranchID        uint
	DocumentDate    time.Time
	Reference       string
	MovementReason  string
	Notes           string
	Lines           []DocumentLineInput
	UserID          uint
	Manual          bool
	Source          string // MANUAL | ADJUSTMENT | IMPORT
}

// UpdateDocumentInput datos para editar un borrador.
type UpdateDocumentInput struct {
	OperationTypeID uint
	DocumentDate    time.Time
	Reference       string
	MovementReason  string
	Notes           string
	Lines           []DocumentLineInput
}

// DocumentListParams filtros de listado.
type DocumentListParams struct {
	Direction string
	Status    string
	BranchID  uint
	Limit     int
	Offset    int
}

// ListOperationTypes devuelve tipos de operación activos (solo lectura).
func (s *InventoryDocumentService) ListOperationTypes(direction string, manualOnly bool) ([]database.TenantInventoryOperationType, error) {
	q := s.db.Model(&database.TenantInventoryOperationType{}).Where("is_active = ?", true)
	if dir := strings.TrimSpace(strings.ToUpper(direction)); dir == "IN" || dir == "OUT" {
		q = q.Where("direction = ?", dir)
	}
	if manualOnly {
		q = q.Where("allow_manual = ?", true)
	}
	var rows []database.TenantInventoryOperationType
	err := q.Order("sort_order ASC, id ASC").Find(&rows).Error
	return rows, err
}

// GetInventoryDocument obtiene cabecera + líneas.
func (s *InventoryDocumentService) GetInventoryDocument(id uint) (database.TenantInventoryDocument, []database.TenantInventoryDocumentDetail, error) {
	var doc database.TenantInventoryDocument
	if err := s.db.First(&doc, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return doc, nil, ErrDocumentNotFound
		}
		return doc, nil, err
	}
	var lines []database.TenantInventoryDocumentDetail
	if err := s.db.Where("document_id = ?", id).Order("sort_order ASC, id ASC").Find(&lines).Error; err != nil {
		return doc, nil, err
	}
	return doc, lines, nil
}

// ListInventoryDocuments lista documentos con paginación opcional.
func (s *InventoryDocumentService) ListInventoryDocuments(params DocumentListParams) ([]database.TenantInventoryDocument, int64, error) {
	q := s.db.Model(&database.TenantInventoryDocument{})
	if dir := strings.TrimSpace(strings.ToUpper(params.Direction)); dir == "IN" || dir == "OUT" {
		q = q.Where("direction = ?", dir)
	}
	if st := strings.TrimSpace(strings.ToLower(params.Status)); st != "" {
		q = q.Where("status = ?", st)
	}
	if params.BranchID > 0 {
		q = q.Where("branch_id = ?", params.BranchID)
	}
	var total int64
	if params.Limit > 0 {
		if err := q.Count(&total).Error; err != nil {
			return nil, 0, err
		}
	}
	if params.Limit > 0 {
		q = q.Offset(params.Offset).Limit(params.Limit)
	}
	var docs []database.TenantInventoryDocument
	err := q.Order("created_at DESC").Find(&docs).Error
	if params.Limit == 0 {
		total = int64(len(docs))
	}
	return docs, total, err
}

// CreateInventoryDocument crea un documento en borrador.
func (s *InventoryDocumentService) CreateInventoryDocument(input CreateDocumentInput) (uint, error) {
	input.Manual = true
	if strings.TrimSpace(input.Source) == "" {
		input.Source = DocumentSourceManual
	}
	input.Source = normalizeDocumentSource(input.Source)
	var docID uint
	err := s.db.Transaction(func(tx *gorm.DB) error {
		svc := &InventoryDocumentService{db: tx}
		id, err := svc.createInventoryDocumentTx(tx, input)
		if err != nil {
			return err
		}
		docID = id
		return nil
	})
	return docID, err
}

// UpdateInventoryDocument actualiza un borrador existente.
func (s *InventoryDocumentService) UpdateInventoryDocument(id uint, input UpdateDocumentInput) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		doc, err := loadDocumentForUpdate(tx, id)
		if err != nil {
			return err
		}
		op, err := LookupOperationTypeByID(tx, input.OperationTypeID)
		if err != nil {
			return err
		}
		if err := ValidateDocumentHeader(op, doc.Direction, input.Reference, true); err != nil {
			return err
		}
		if err := ValidateDocumentLines(input.Lines, doc.Direction); err != nil {
			return err
		}
		docDate := input.DocumentDate
		if docDate.IsZero() {
			docDate = doc.DocumentDate
		}
		updates := map[string]interface{}{
			"operation_type_id": op.ID,
			"document_date":     docDate,
			"reference":         strings.TrimSpace(input.Reference),
			"movement_reason":   strings.TrimSpace(input.MovementReason),
			"notes":             strings.TrimSpace(input.Notes),
			"updated_at":        time.Now(),
		}
		if err := tx.Model(&doc).Updates(updates).Error; err != nil {
			return err
		}
		return replaceDocumentLines(tx, doc.ID, input.Lines)
	})
}

// ConfirmInventoryDocument confirma el documento y genera kardex en una sola transacción.
func (s *InventoryDocumentService) ConfirmInventoryDocument(id, userID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		svc := &InventoryDocumentService{db: tx}
		return svc.confirmInventoryDocumentTx(tx, id, userID)
	})
}

// VoidInventoryDocument anula un documento confirmado revirtiendo stock/kardex.
func (s *InventoryDocumentService) VoidInventoryDocument(id, userID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		svc := &InventoryDocumentService{db: tx}
		return svc.voidInventoryDocumentTx(tx, id, userID)
	})
}

// RecordAdjustmentViaDocument wrapper compatible con POST /inventory/adjustment.
func (s *InventoryDocumentService) RecordAdjustmentViaDocument(input AdjustmentInput, userID uint) error {
	if input.ProductID == 0 || input.BranchID == 0 {
		return errors.New("producto y sucursal son requeridos")
	}
	if input.Type != "in" && input.Type != "out" {
		return errors.New("tipo debe ser 'in' u 'out'")
	}
	if input.Quantity <= 0 {
		return errors.New("la cantidad debe ser mayor a cero")
	}
	notes := strings.TrimSpace(input.Notes)
	if notes == "" {
		return errors.New("indica el motivo del ajuste")
	}

	var product database.TenantProduct
	if err := s.db.First(&product, input.ProductID).Error; err != nil {
		return err
	}
	if !product.ManageStock {
		return errors.New("el producto no controla stock")
	}

	direction := "IN"
	opCode := "INVENTORY_ADJUSTMENT_IN"
	if input.Type == "out" {
		direction = "OUT"
		opCode = "INVENTORY_ADJUSTMENT_OUT"
	}

	op, err := LookupOperationTypeByCode(s.db, opCode)
	if err != nil {
		return err
	}

	if product.ManageSeries {
		n := int(input.Quantity)
		if n <= 0 {
			return errors.New("para productos con series la cantidad debe ser un entero mayor a 0")
		}
		if input.Type == "in" {
			if len(input.Serials) != n {
				return errors.New("debe indicar exactamente la misma cantidad de números de serie")
			}
		} else if len(input.Serials) != n {
			return errors.New("debe seleccionar exactamente la misma cantidad de series a retirar")
		}
	}

	if input.PresentationID != nil && *input.PresentationID > 0 && !product.HasVariants {
		return errors.New("el producto no tiene presentaciones")
	}

	line := DocumentLineInput{
		ProductID:      input.ProductID,
		PresentationID: input.PresentationID,
		Quantity:       input.Quantity,
		Serials:        input.Serials,
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		svc := &InventoryDocumentService{db: tx}
		docID, err := svc.createInventoryDocumentTx(tx, CreateDocumentInput{
			Direction:       direction,
			OperationTypeID: op.ID,
			BranchID:        input.BranchID,
			DocumentDate:    time.Now(),
			MovementReason:  notes,
			Notes:           notes,
			Lines:           []DocumentLineInput{line},
			UserID:          userID,
			Manual:          false,
			Source:          DocumentSourceAdjustment,
		})
		if err != nil {
			return err
		}
		return svc.confirmInventoryDocumentTx(tx, docID, userID)
	})
}

func (s *InventoryDocumentService) createInventoryDocumentTx(tx *gorm.DB, input CreateDocumentInput) (uint, error) {
	dir, err := ValidateDocumentDirection(input.Direction)
	if err != nil {
		return 0, err
	}
	if input.BranchID == 0 || input.UserID == 0 || input.OperationTypeID == 0 {
		return 0, errors.New("sucursal, usuario y tipo de operación son requeridos")
	}
	op, err := LookupOperationTypeByID(tx, input.OperationTypeID)
	if err != nil {
		return 0, err
	}
	if err := ValidateDocumentHeader(op, dir, input.Reference, input.Manual); err != nil {
		return 0, err
	}
	if err := ValidateDocumentLines(input.Lines, dir); err != nil {
		return 0, err
	}
	docDate := input.DocumentDate
	if docDate.IsZero() {
		docDate = time.Now()
	}
	doc := database.TenantInventoryDocument{
		Direction:       dir,
		OperationTypeID: op.ID,
		BranchID:        input.BranchID,
		DocumentDate:    docDate,
		Status:          DocumentStatusDraft,
		Source:          normalizeDocumentSource(input.Source),
		Reference:       strings.TrimSpace(input.Reference),
		MovementReason:  strings.TrimSpace(input.MovementReason),
		Notes:           strings.TrimSpace(input.Notes),
		CreatedBy:       input.UserID,
	}
	if err := tx.Create(&doc).Error; err != nil {
		return 0, err
	}
	if err := tx.Model(&doc).Update("number", fmt.Sprintf("DRAFT-%d", doc.ID)).Error; err != nil {
		return 0, err
	}
	if err := replaceDocumentLines(tx, doc.ID, input.Lines); err != nil {
		return 0, err
	}
	return doc.ID, nil
}

func (s *InventoryDocumentService) confirmInventoryDocumentTx(tx *gorm.DB, id, userID uint) error {
	var doc database.TenantInventoryDocument
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&doc, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDocumentNotFound
		}
		return err
	}
	if err := ValidateDocumentStatusForConfirm(doc.Status); err != nil {
		return err
	}

	op, err := LookupOperationTypeByID(tx, doc.OperationTypeID)
	if err != nil {
		return err
	}
	if err := ValidateDocumentHeader(op, doc.Direction, doc.Reference, true); err != nil {
		return err
	}

	var lines []database.TenantInventoryDocumentDetail
	if err := tx.Where("document_id = ?", doc.ID).Order("sort_order ASC, id ASC").Find(&lines).Error; err != nil {
		return err
	}
	if err := ValidateDocumentLines(documentDetailsToLineInputs(lines), doc.Direction); err != nil {
		return err
	}

	seriesID, err := resolveInventorySeriesID(tx, doc.BranchID, doc.Direction)
	if err != nil {
		return err
	}
	correlative, seriesRow, err := docseries.ReserveNext(tx, seriesID)
	if err != nil {
		return err
	}
	number := fmt.Sprintf("%s-%08d", seriesRow.Series, correlative)
	now := time.Now()

	inv := NewInventoryService(tx)
	movType := "in"
	if doc.Direction == "OUT" {
		movType = "out"
	}
	opID := op.ID
	docID := doc.ID
	notes := buildMovementNotes(doc)

	for _, line := range lines {
		if err := applyDocumentLineMovement(tx, inv, doc, line, movType, opID, docID, number, notes, userID); err != nil {
			return err
		}
	}

	return tx.Model(&doc).Updates(map[string]interface{}{
		"number":       number,
		"series_id":    seriesRow.ID,
		"correlative":  correlative,
		"status":       DocumentStatusConfirmed,
		"confirmed_at": now,
		"confirmed_by": userID,
		"updated_at":   now,
	}).Error
}

func (s *InventoryDocumentService) voidInventoryDocumentTx(tx *gorm.DB, id, userID uint) error {
	var doc database.TenantInventoryDocument
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&doc, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDocumentNotFound
		}
		return err
	}
	if err := ValidateDocumentStatusForVoid(doc.Status); err != nil {
		return err
	}

	var lines []database.TenantInventoryDocumentDetail
	if err := tx.Where("document_id = ?", doc.ID).Order("sort_order ASC, id ASC").Find(&lines).Error; err != nil {
		return err
	}

	inv := NewInventoryService(tx)
	reverseType := "out"
	if doc.Direction == "OUT" {
		reverseType = "in"
	}
	opID := doc.OperationTypeID
	docID := doc.ID
	voidRef := "ANULACION " + doc.Number
	notes := buildMovementNotes(doc)
	now := time.Now()

	for _, line := range lines {
		if err := applyDocumentLineVoid(tx, inv, doc, line, reverseType, opID, docID, voidRef, notes, userID); err != nil {
			return err
		}
	}

	return tx.Model(&doc).Updates(map[string]interface{}{
		"status":     DocumentStatusVoided,
		"voided_at":  now,
		"voided_by":  userID,
		"updated_at": now,
	}).Error
}

func loadDocumentForUpdate(tx *gorm.DB, id uint) (database.TenantInventoryDocument, error) {
	var doc database.TenantInventoryDocument
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&doc, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return doc, ErrDocumentNotFound
		}
		return doc, err
	}
	if err := ValidateDocumentStatusForUpdate(doc.Status); err != nil {
		return doc, err
	}
	return doc, nil
}

func replaceDocumentLines(tx *gorm.DB, documentID uint, lines []DocumentLineInput) error {
	if err := tx.Where("document_id = ?", documentID).Delete(&database.TenantInventoryDocumentDetail{}).Error; err != nil {
		return err
	}
	for i, line := range lines {
		var product database.TenantProduct
		if err := tx.First(&product, line.ProductID).Error; err != nil {
			return errors.New("producto no encontrado")
		}
		if !product.ManageStock {
			return errors.New("el producto no controla stock: " + product.Name)
		}
		serialsJSON := ""
		if len(line.Serials) > 0 {
			b, _ := json.Marshal(line.Serials)
			serialsJSON = string(b)
		}
		if product.ManageSeries {
			if err := validateLineSerials(product, line); err != nil {
				return err
			}
		}
		row := database.TenantInventoryDocumentDetail{
			DocumentID:     documentID,
			ProductID:      line.ProductID,
			PresentationID: line.PresentationID,
			Quantity:       line.Quantity,
			UnitCost:       line.UnitCost,
			SerialsJSON:    serialsJSON,
			SortOrder:      i,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func validateLineSerials(product database.TenantProduct, line DocumentLineInput) error {
	n := int(line.Quantity)
	if n <= 0 {
		return errors.New("cantidad debe ser entera para productos con series")
	}
	if len(line.Serials) != n {
		return errors.New("debe indicar exactamente la misma cantidad de números de serie")
	}
	return nil
}

func documentDetailsToLineInputs(lines []database.TenantInventoryDocumentDetail) []DocumentLineInput {
	out := make([]DocumentLineInput, 0, len(lines))
	for _, l := range lines {
		var serials []string
		if l.SerialsJSON != "" {
			_ = json.Unmarshal([]byte(l.SerialsJSON), &serials)
		}
		out = append(out, DocumentLineInput{
			ProductID: l.ProductID,
			Quantity:  l.Quantity,
			UnitCost:  l.UnitCost,
			Serials:   serials,
		})
	}
	return out
}

func resolveInventorySeriesID(tx *gorm.DB, branchID uint, direction string) (uint, error) {
	docType := inventoryDirectionDocType(direction)
	id, err := lookupInventorySeriesByDocType(tx, branchID, docType)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, errInventorySeriesNotFound) {
		return 0, err
	}
	if err := database.SeedInventoryDocumentSeriesForBranch(tx, branchID); err != nil {
		return 0, err
	}
	id, err = lookupInventorySeriesByDocType(tx, branchID, docType)
	if err == nil {
		return id, nil
	}
	if errors.Is(err, errInventorySeriesNotFound) {
		return 0, fmt.Errorf(
			"no hay serie activa de %s de inventario para esta sucursal; créela en Ajustes → Series",
			inventoryDirectionLabel(direction),
		)
	}
	return 0, err
}

var errInventorySeriesNotFound = errors.New("inventory series not found")

func lookupInventorySeriesByDocType(tx *gorm.DB, branchID uint, docType string) (uint, error) {
	var rows []database.TenantDocumentSeries
	if err := tx.Where(
		"branch_id = ? AND category = ? AND doc_type = ? AND active = ?",
		branchID, "almacen", docType, true,
	).Order("id ASC").Find(&rows).Error; err != nil {
		return 0, err
	}
	switch len(rows) {
	case 0:
		return 0, errInventorySeriesNotFound
	case 1:
		return rows[0].ID, nil
	default:
		return 0, fmt.Errorf(
			"hay más de una serie activa de %s para esta sucursal; deje solo una en Ajustes → Series",
			inventoryDocTypeLabel(docType),
		)
	}
}

func inventoryDirectionDocType(direction string) string {
	if strings.ToUpper(direction) == "OUT" {
		return "EGRESO_INVENTARIO"
	}
	return "INGRESO_INVENTARIO"
}

func inventoryDirectionLabel(direction string) string {
	if strings.ToUpper(direction) == "OUT" {
		return "egreso"
	}
	return "ingreso"
}

func inventoryDocTypeLabel(docType string) string {
	switch strings.ToUpper(strings.TrimSpace(docType)) {
	case "EGRESO_INVENTARIO":
		return "egreso"
	default:
		return "ingreso"
	}
}

func buildMovementNotes(doc database.TenantInventoryDocument) string {
	parts := make([]string, 0, 3)
	if doc.MovementReason != "" {
		parts = append(parts, doc.MovementReason)
	}
	if doc.Notes != "" && doc.Notes != doc.MovementReason {
		parts = append(parts, doc.Notes)
	}
	if doc.Reference != "" {
		parts = append(parts, "Ref: "+doc.Reference)
	}
	return strings.Join(parts, " | ")
}

func applyDocumentLineMovement(
	tx *gorm.DB,
	inv *InventoryService,
	doc database.TenantInventoryDocument,
	line database.TenantInventoryDocumentDetail,
	movType string,
	opID, docID uint,
	reference, notes string,
	userID uint,
) error {
	var product database.TenantProduct
	if err := tx.First(&product, line.ProductID).Error; err != nil {
		return err
	}

	var serials []string
	if line.SerialsJSON != "" {
		_ = json.Unmarshal([]byte(line.SerialsJSON), &serials)
	}

	var presentationID *uint
	if product.HasVariants && line.PresentationID != nil && *line.PresentationID > 0 {
		presentationID = line.PresentationID
	}

	opTypeID := opID
	docRefID := docID
	movInput := MovementInput{
		ProductID:           line.ProductID,
		PresentationID:      presentationID,
		BranchID:            doc.BranchID,
		Type:                movType,
		Quantity:            line.Quantity,
		UnitCost:            line.UnitCost,
		Reference:           reference,
		Notes:               notes,
		UserID:              userID,
		OperationTypeID:     &opTypeID,
		InventoryDocumentID: &docRefID,
	}

	if product.ManageSeries {
		if movType == "in" {
			return applySerialsInTx(tx, inv, movInput, serials)
		}
		return applySerialsOutTx(tx, inv, movInput, serials)
	}

	if movType == "out" {
		if presentationID != nil {
			var pstock database.TenantProductPresentationStock
			tx.Where("presentation_id = ? AND branch_id = ?", *presentationID, doc.BranchID).First(&pstock)
			if pstock.Quantity < line.Quantity {
				return errors.New("stock insuficiente")
			}
		} else {
			var stock database.TenantProductStock
			tx.Where("product_id = ? AND branch_id = ?", line.ProductID, doc.BranchID).First(&stock)
			if stock.Quantity < line.Quantity {
				return errors.New("stock insuficiente")
			}
		}
	}
	return inv.RecordMovementTx(tx, movInput)
}

func applyDocumentLineVoid(
	tx *gorm.DB,
	inv *InventoryService,
	doc database.TenantInventoryDocument,
	line database.TenantInventoryDocumentDetail,
	reverseType string,
	opID, docID uint,
	reference, notes string,
	userID uint,
) error {
	var product database.TenantProduct
	if err := tx.First(&product, line.ProductID).Error; err != nil {
		return err
	}
	var serials []string
	if line.SerialsJSON != "" {
		_ = json.Unmarshal([]byte(line.SerialsJSON), &serials)
	}
	var presentationID *uint
	if product.HasVariants && line.PresentationID != nil && *line.PresentationID > 0 {
		presentationID = line.PresentationID
	}

	opTypeID := opID
	docRefID := docID
	movInput := MovementInput{
		ProductID:           line.ProductID,
		PresentationID:      presentationID,
		BranchID:            doc.BranchID,
		Type:                reverseType,
		Quantity:            line.Quantity,
		UnitCost:            line.UnitCost,
		Reference:           reference,
		Notes:               notes,
		UserID:              userID,
		OperationTypeID:     &opTypeID,
		InventoryDocumentID: &docRefID,
	}

	if product.ManageSeries {
		if reverseType == "out" {
			return applySerialsOutTx(tx, inv, movInput, serials)
		}
		if doc.Direction == "OUT" {
			return applySerialsRestoreOnVoidTx(tx, inv, movInput, serials)
		}
		return applySerialsInTx(tx, inv, movInput, serials)
	}
	if reverseType == "out" {
		if presentationID != nil {
			var pstock database.TenantProductPresentationStock
			tx.Where("presentation_id = ? AND branch_id = ?", *presentationID, doc.BranchID).First(&pstock)
			if pstock.Quantity < line.Quantity {
				return errors.New("stock insuficiente para anular")
			}
		} else {
			var stock database.TenantProductStock
			tx.Where("product_id = ? AND branch_id = ?", line.ProductID, doc.BranchID).First(&stock)
			if stock.Quantity < line.Quantity {
				return errors.New("stock insuficiente para anular")
			}
		}
	}
	return inv.RecordMovementTx(tx, movInput)
}

func applySerialsInTx(tx *gorm.DB, inv *InventoryService, input MovementInput, serials []string) error {
	for _, serial := range serials {
		serial = strings.TrimSpace(serial)
		if serial == "" {
			return errors.New("no se permiten seriales vacíos")
		}
		var exists int64
		tx.Model(&database.TenantProductSerial{}).Where("product_id = ? AND serial = ?", input.ProductID, serial).Count(&exists)
		if exists > 0 {
			return errors.New("el serial '" + serial + "' ya existe para este producto")
		}
	}
	if err := inv.RecordMovementTx(tx, input); err != nil {
		return err
	}
	for _, serial := range serials {
		if err := tx.Create(&database.TenantProductSerial{
			ProductID: input.ProductID, BranchID: input.BranchID,
			Serial: strings.TrimSpace(serial), Status: "available",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func applySerialsOutTx(tx *gorm.DB, inv *InventoryService, input MovementInput, serials []string) error {
	for _, serial := range serials {
		var ps database.TenantProductSerial
		if err := tx.Where("product_id = ? AND branch_id = ? AND serial = ? AND status = ?",
			input.ProductID, input.BranchID, serial, "available").First(&ps).Error; err != nil {
			return errors.New("el serial '" + serial + "' no está disponible en esta sucursal")
		}
	}
	if err := inv.RecordMovementTx(tx, input); err != nil {
		return err
	}
	for _, serial := range serials {
		if err := tx.Model(&database.TenantProductSerial{}).
			Where("product_id = ? AND branch_id = ? AND serial = ?", input.ProductID, input.BranchID, serial).
			Updates(map[string]interface{}{"status": "removed", "updated_at": time.Now()}).Error; err != nil {
			return err
		}
	}
	return nil
}

func applySerialsRestoreOnVoidTx(tx *gorm.DB, inv *InventoryService, input MovementInput, serials []string) error {
	for _, serial := range serials {
		var ps database.TenantProductSerial
		if err := tx.Where("product_id = ? AND branch_id = ? AND serial = ? AND status = ?",
			input.ProductID, input.BranchID, serial, "removed").First(&ps).Error; err != nil {
			return errors.New("el serial '" + serial + "' no puede restaurarse en anulación")
		}
	}
	if err := inv.RecordMovementTx(tx, input); err != nil {
		return err
	}
	for _, serial := range serials {
		if err := tx.Model(&database.TenantProductSerial{}).
			Where("product_id = ? AND branch_id = ? AND serial = ?", input.ProductID, input.BranchID, serial).
			Updates(map[string]interface{}{"status": "available", "updated_at": time.Now()}).Error; err != nil {
			return err
		}
	}
	return nil
}
