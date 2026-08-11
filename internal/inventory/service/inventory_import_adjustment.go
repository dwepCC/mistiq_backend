package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

const (
	importStatusUpdate = "Actualizar"
	importStatusSkip   = "Sin cambios"
	importStatusError  = "Error"
)

// ImportAdjustmentRowInput fila del Excel normalizada.
type ImportAdjustmentRowInput struct {
	RowNumber int     `json:"row_number"`
	Barcode   string  `json:"barcode"`
	NewStock  float64 `json:"new_stock"`
}

// ImportAdjustmentPreviewInput solicitud de vista previa.
type ImportAdjustmentPreviewInput struct {
	BranchID uint
	Rows     []ImportAdjustmentRowInput
}

// ImportAdjustmentPreviewRow resultado por fila.
type ImportAdjustmentPreviewRow struct {
	RowNumber    int     `json:"row_number"`
	Barcode      string  `json:"barcode"`
	ProductID    uint    `json:"product_id,omitempty"`
	ProductName  string  `json:"product_name,omitempty"`
	ProductCode  string  `json:"product_code,omitempty"`
	CurrentStock float64 `json:"current_stock"`
	NewStock     float64 `json:"new_stock"`
	Delta        float64 `json:"delta"`
	Direction    string  `json:"direction,omitempty"`
	UnitCost     float64 `json:"unit_cost"`
	LineTotal    float64 `json:"line_total"`
	Status       string  `json:"status"`
	Error        string  `json:"error,omitempty"`
}

// ImportAdjustmentPreviewSummary totales de la vista previa.
type ImportAdjustmentPreviewSummary struct {
	TotalRows     int     `json:"total_rows"`
	ValidInRows   int     `json:"valid_in_rows"`
	ValidOutRows  int     `json:"valid_out_rows"`
	SkippedRows   int     `json:"skipped_rows"`
	ErrorRows     int     `json:"error_rows"`
	TotalInQty    float64 `json:"total_in_qty"`
	TotalOutQty   float64 `json:"total_out_qty"`
	TotalInValue  float64 `json:"total_in_value"`
	TotalOutValue float64 `json:"total_out_value"`
}

// ImportAdjustmentPreviewResult respuesta de preview.
type ImportAdjustmentPreviewResult struct {
	Summary    ImportAdjustmentPreviewSummary `json:"summary"`
	Rows       []ImportAdjustmentPreviewRow   `json:"rows"`
	CanConfirm bool                           `json:"can_confirm"`
}

// ImportAdjustmentConfirmInput confirma el ajuste masivo.
type ImportAdjustmentConfirmInput struct {
	BranchID       uint
	MovementReason string
	Notes          string
	Rows           []ImportAdjustmentRowInput
}

// ImportAdjustmentConfirmResult documentos generados.
type ImportAdjustmentConfirmResult struct {
	ImportReference string                         `json:"import_reference"`
	InDocumentID    *uint                          `json:"in_document_id,omitempty"`
	OutDocumentID   *uint                          `json:"out_document_id,omitempty"`
	InDocumentNo    string                         `json:"in_document_number,omitempty"`
	OutDocumentNo   string                         `json:"out_document_number,omitempty"`
	Summary         ImportAdjustmentPreviewSummary `json:"summary"`
}

func normalizeBarcode(raw string) string {
	return strings.TrimSpace(raw)
}

// PreviewImportAdjustment valida filas y calcula deltas con valorización (costo promedio).
func (s *InventoryDocumentService) PreviewImportAdjustment(input ImportAdjustmentPreviewInput) (*ImportAdjustmentPreviewResult, error) {
	if input.BranchID == 0 {
		return nil, errors.New("sucursal requerida")
	}
	inLines, outLines, rows, summary, err := s.resolveImportAdjustment(input.BranchID, input.Rows)
	if err != nil {
		return nil, err
	}
	_ = inLines
	_ = outLines
	return &ImportAdjustmentPreviewResult{
		Summary:    summary,
		Rows:       rows,
		CanConfirm: summary.ErrorRows == 0 && (summary.ValidInRows > 0 || summary.ValidOutRows > 0),
	}, nil
}

// ConfirmImportAdjustment crea y confirma hasta dos documentos (IN y OUT).
func (s *InventoryDocumentService) ConfirmImportAdjustment(input ImportAdjustmentConfirmInput, userID uint) (*ImportAdjustmentConfirmResult, error) {
	if input.BranchID == 0 || userID == 0 {
		return nil, errors.New("sucursal y usuario son requeridos")
	}
	reason := strings.TrimSpace(input.MovementReason)
	if reason == "" {
		reason = "Ajuste masivo por importación Excel"
	}

	inLines, outLines, _, summary, err := s.resolveImportAdjustment(input.BranchID, input.Rows)
	if err != nil {
		return nil, err
	}
	if summary.ErrorRows > 0 {
		return nil, fmt.Errorf("corrija %d fila(s) con error antes de confirmar", summary.ErrorRows)
	}
	if len(inLines) == 0 && len(outLines) == 0 {
		return nil, errors.New("no hay líneas válidas para procesar")
	}

	importRef := fmt.Sprintf("IMPORT-%s", time.Now().Format("20060102-150405"))
	notes := strings.TrimSpace(input.Notes)
	if notes == "" {
		notes = importRef
	} else {
		notes = importRef + " | " + notes
	}

	result := &ImportAdjustmentConfirmResult{
		ImportReference: importRef,
		Summary:         summary,
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		svc := &InventoryDocumentService{db: tx}
		if len(inLines) > 0 {
			op, err := LookupOperationTypeByCode(tx, "INVENTORY_ADJUSTMENT_IN")
			if err != nil {
				return err
			}
			docID, err := svc.createInventoryDocumentTx(tx, CreateDocumentInput{
				Direction:       "IN",
				OperationTypeID: op.ID,
				BranchID:        input.BranchID,
				DocumentDate:    time.Now(),
				Reference:       importRef,
				MovementReason:  reason,
				Notes:           notes,
				Lines:           inLines,
				UserID:          userID,
				Manual:          false,
				Source:          DocumentSourceImport,
			})
			if err != nil {
				return err
			}
			if err := svc.confirmInventoryDocumentTx(tx, docID, userID); err != nil {
				return err
			}
			var doc database.TenantInventoryDocument
			if err := tx.First(&doc, docID).Error; err != nil {
				return err
			}
			result.InDocumentID = &docID
			result.InDocumentNo = doc.Number
		}
		if len(outLines) > 0 {
			op, err := LookupOperationTypeByCode(tx, "INVENTORY_ADJUSTMENT_OUT")
			if err != nil {
				return err
			}
			docID, err := svc.createInventoryDocumentTx(tx, CreateDocumentInput{
				Direction:       "OUT",
				OperationTypeID: op.ID,
				BranchID:        input.BranchID,
				DocumentDate:    time.Now(),
				Reference:       importRef,
				MovementReason:  reason,
				Notes:           notes,
				Lines:           outLines,
				UserID:          userID,
				Manual:          false,
				Source:          DocumentSourceImport,
			})
			if err != nil {
				return err
			}
			if err := svc.confirmInventoryDocumentTx(tx, docID, userID); err != nil {
				return err
			}
			var doc database.TenantInventoryDocument
			if err := tx.First(&doc, docID).Error; err != nil {
				return err
			}
			result.OutDocumentID = &docID
			result.OutDocumentNo = doc.Number
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *InventoryDocumentService) resolveImportAdjustment(
	branchID uint,
	rows []ImportAdjustmentRowInput,
) ([]DocumentLineInput, []DocumentLineInput, []ImportAdjustmentPreviewRow, ImportAdjustmentPreviewSummary, error) {
	if len(rows) == 0 {
		return nil, nil, nil, ImportAdjustmentPreviewSummary{}, errors.New("el archivo no contiene filas")
	}

	seenBarcode := make(map[string]int)
	codes := make([]string, 0, len(rows))
	previewRows := make([]ImportAdjustmentPreviewRow, 0, len(rows))

	for _, row := range rows {
		barcode := normalizeBarcode(row.Barcode)
		preview := ImportAdjustmentPreviewRow{
			RowNumber: row.RowNumber,
			Barcode:   barcode,
			NewStock:  row.NewStock,
		}
		if barcode == "" {
			preview.Status = importStatusError
			preview.Error = "código de barras requerido"
			previewRows = append(previewRows, preview)
			continue
		}
		if firstRow, dup := seenBarcode[barcode]; dup {
			preview.Status = importStatusError
			preview.Error = fmt.Sprintf("código duplicado (primera aparición fila %d)", firstRow)
			previewRows = append(previewRows, preview)
			continue
		}
		seenBarcode[barcode] = row.RowNumber
		if row.NewStock < 0 {
			preview.Status = importStatusError
			preview.Error = "stock nuevo no puede ser negativo"
			previewRows = append(previewRows, preview)
			continue
		}
		codes = append(codes, barcode)
		previewRows = append(previewRows, preview)
	}

	productByCode, ambiguousCodes, err := loadProductsByCodes(s.db, codes)
	if err != nil {
		return nil, nil, nil, ImportAdjustmentPreviewSummary{}, err
	}

	productIDs := make([]uint, 0, len(productByCode))
	for _, p := range productByCode {
		productIDs = append(productIDs, p.ID)
	}
	stockByProduct, err := loadStockByProducts(s.db, branchID, productIDs)
	if err != nil {
		return nil, nil, nil, ImportAdjustmentPreviewSummary{}, err
	}
	avgCosts, err := NewInventoryService(s.db).WeightedAverageUnitCosts(productIDs, branchID)
	if err != nil {
		return nil, nil, nil, ImportAdjustmentPreviewSummary{}, err
	}

	var inLines []DocumentLineInput
	var outLines []DocumentLineInput
	summary := ImportAdjustmentPreviewSummary{TotalRows: len(rows)}

	for i := range previewRows {
		pr := &previewRows[i]
		if pr.Status == importStatusError {
			summary.ErrorRows++
			continue
		}
		barcode := pr.Barcode
		if ambiguousCodes[barcode] {
			pr.Status = importStatusError
			pr.Error = "código de barras ambiguo (varios productos con el mismo código)"
			summary.ErrorRows++
			continue
		}
		product, ok := productByCode[barcode]
		if !ok {
			pr.Status = importStatusError
			pr.Error = "producto no encontrado"
			summary.ErrorRows++
			continue
		}
		pr.ProductID = product.ID
		pr.ProductName = product.Name
		pr.ProductCode = product.Code
		if !product.Active {
			pr.Status = importStatusError
			pr.Error = "producto inactivo"
			summary.ErrorRows++
			continue
		}
		if !product.ManageStock {
			pr.Status = importStatusError
			pr.Error = "producto sin control de stock"
			summary.ErrorRows++
			continue
		}
		if product.ManageSeries {
			pr.Status = importStatusError
			pr.Error = "producto con series no soportado en importación masiva"
			summary.ErrorRows++
			continue
		}

		current := stockByProduct[product.ID]
		pr.CurrentStock = current
		delta := pr.NewStock - current
		pr.Delta = delta
		if delta == 0 {
			pr.Status = importStatusSkip
			summary.SkippedRows++
			continue
		}

		unitCost := avgCosts[product.ID]
		if delta > 0 {
			pr.Direction = "IN"
			pr.UnitCost = unitCost
			pr.LineTotal = delta * unitCost
			pr.Status = importStatusUpdate
			summary.ValidInRows++
			summary.TotalInQty += delta
			summary.TotalInValue += pr.LineTotal
			inLines = append(inLines, DocumentLineInput{
				ProductID: product.ID,
				Quantity:  delta,
				UnitCost:  unitCost,
			})
		} else {
			qty := -delta
			pr.Direction = "OUT"
			pr.UnitCost = unitCost
			pr.LineTotal = qty * unitCost
			if current < qty {
				pr.Status = importStatusError
				pr.Error = "stock insuficiente para el egreso"
				summary.ErrorRows++
				continue
			}
			pr.Status = importStatusUpdate
			summary.ValidOutRows++
			summary.TotalOutQty += qty
			summary.TotalOutValue += pr.LineTotal
			outLines = append(outLines, DocumentLineInput{
				ProductID: product.ID,
				Quantity:  qty,
				UnitCost:  unitCost,
			})
		}
	}

	return inLines, outLines, previewRows, summary, nil
}

func loadProductsByCodes(db *gorm.DB, codes []string) (map[string]database.TenantProduct, map[string]bool, error) {
	out := make(map[string]database.TenantProduct)
	ambiguous := make(map[string]bool)
	if len(codes) == 0 {
		return out, ambiguous, nil
	}
	unique := make([]string, 0, len(codes))
	seen := make(map[string]struct{})
	for _, c := range codes {
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		unique = append(unique, c)
	}
	var products []database.TenantProduct
	if err := db.Where("code IN ?", unique).Find(&products).Error; err != nil {
		return nil, nil, err
	}
	for _, p := range products {
		code := normalizeBarcode(p.Code)
		if code == "" {
			continue
		}
		if _, exists := out[code]; exists {
			ambiguous[code] = true
			continue
		}
		out[code] = p
	}
	return out, ambiguous, nil
}

func loadStockByProducts(db *gorm.DB, branchID uint, productIDs []uint) (map[uint]float64, error) {
	out := make(map[uint]float64)
	if len(productIDs) == 0 {
		return out, nil
	}
	var stocks []database.TenantProductStock
	if err := db.Where("branch_id = ? AND product_id IN ?", branchID, productIDs).Find(&stocks).Error; err != nil {
		return nil, err
	}
	for _, st := range stocks {
		out[st.ProductID] = st.Quantity
	}
	return out, nil
}
