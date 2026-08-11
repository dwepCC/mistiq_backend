package handler

import (
	"strconv"
	"time"

	billingsvc "tukifac/internal/billing/service"
	"tukifac/internal/purchases/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/datespe"
	"tukifac/pkg/tax"

	"github.com/gofiber/fiber/v3"
)

// GET /api/purchases?q=&contact_id=&from=&to=&page=&per_page=
func (h *PurchaseHandler) ListAPI(c fiber.Ctx) error {
	tdb := db(c)
	contactID, _ := strconv.ParseUint(c.Query("contact_id"), 10, 32)
	params := service.PurchaseListParams{
		Query:     c.Query("q"),
		ContactID: uint(contactID),
	}
	// El rango se interpreta en hora de Perú y cubre el día completo: la fecha de emisión
	// se guarda al mediodía, así que un `to` en 00:00 dejaría fuera las compras de ese día.
	loc := datespe.Location()
	if f := c.Query("from"); f != "" {
		if t, err := time.ParseInLocation("2006-01-02", f, loc); err == nil {
			from := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
			params.DateFrom = &from
		}
	}
	if t := c.Query("to"); t != "" {
		if ts, err := time.ParseInLocation("2006-01-02", t, loc); err == nil {
			to := time.Date(ts.Year(), ts.Month(), ts.Day(), 23, 59, 59, 999999999, loc)
			params.DateTo = &to
		}
	}
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	if perPage > 0 {
		params.Limit = perPage
		params.Offset = (page - 1) * perPage
	}
	purchases, total, err := service.NewPurchaseService(tdb).List(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	var contacts []database.TenantContact
	tdb.Find(&contacts)
	contactMap := make(map[uint]string)
	for _, ct := range contacts {
		contactMap[ct.ID] = ct.BusinessName
	}

	type PurchaseItem struct {
		ID              uint                               `json:"id"`
		DocType         string                             `json:"doc_type"`
		Series          string                             `json:"series"`
		Number          string                             `json:"number"`
		IssueDate       string                             `json:"issue_date"`
		SupplierName    string                             `json:"supplier_name"`
		Currency        string                             `json:"currency"`
		Subtotal        float64                            `json:"subtotal"`
		TaxAmount       float64                            `json:"tax_amount"`
		Total           float64                            `json:"total"`
		Status          string                             `json:"status"`
		LinkedRetention *billingsvc.LinkedFiscalDocSummary `json:"linked_retention,omitempty"`
	}
	purchaseIDs := make([]uint, 0, len(purchases))
	for _, p := range purchases {
		purchaseIDs = append(purchaseIDs, p.ID)
	}
	linkedByPurchase := billingsvc.NewBillingService(tdb).BatchLinkedRetentionsByPurchaseIDs(purchaseIDs)
	out := make([]PurchaseItem, 0, len(purchases))
	for _, p := range purchases {
		sname := ""
		if p.ContactID != nil {
			sname = contactMap[*p.ContactID]
		}
		var linked *billingsvc.LinkedFiscalDocSummary
		if s, ok := linkedByPurchase[p.ID]; ok {
			summary := s
			linked = &summary
		}
		out = append(out, PurchaseItem{
			ID:              p.ID,
			DocType:         p.DocType,
			Series:          p.Series,
			Number:          p.Number,
			IssueDate:       p.IssueDate.Format("2006-01-02"),
			SupplierName:    sname,
			Currency:        p.Currency,
			Subtotal:        p.Subtotal,
			TaxAmount:       p.TaxAmount,
			Total:           p.Total,
			Status:          p.Status,
			LinkedRetention: linked,
		})
	}
	return c.JSON(fiber.Map{"data": out, "total": total})
}

// GET /api/purchases/:id
func (h *PurchaseHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	tdb := db(c)
	svc := service.NewPurchaseService(tdb)
	p, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Compra no encontrada"})
	}
	items, _ := svc.GetItems(uint(id))

	// Enriquecer cada ítem con los números de serie registrados (productos con series)
	type itemRow struct {
		ID                 uint     `json:"id"`
		ProductID          *uint    `json:"product_id"`
		Code               string   `json:"code"`
		Description        string   `json:"description"`
		Unit               string   `json:"unit"`
		Quantity           float64  `json:"quantity"`
		UnitCost           float64  `json:"unit_cost"`
		TaxRate            float64  `json:"tax_rate"`
		IgvAffectationType string   `json:"igv_affectation_type"`
		PriceIncludesIgv   bool     `json:"price_includes_igv"`
		Subtotal           float64  `json:"subtotal"`
		TaxAmount          float64  `json:"tax_amount"`
		Total              float64  `json:"total"`
		Serials            []string `json:"serials"`
	}
	itemsWithSerials := make([]itemRow, 0, len(items))
	for _, it := range items {
		row := itemRow{
			ID:                 it.ID,
			ProductID:          it.ProductID,
			Code:               it.Code,
			Description:        it.Description,
			Unit:               it.Unit,
			Quantity:           it.Quantity,
			UnitCost:           it.UnitCost,
			TaxRate:            it.TaxRate,
			IgvAffectationType: it.IgvAffectationType,
			PriceIncludesIgv:   it.PriceIncludesIgv,
			Subtotal:           it.Subtotal,
			TaxAmount:          it.TaxAmount,
			Total:              it.Total,
			Serials:            []string{},
		}
		var serials []database.TenantProductSerial
		if tdb.Where("purchase_item_id = ?", it.ID).Find(&serials).Error == nil {
			for _, s := range serials {
				row.Serials = append(row.Serials, s.Serial)
			}
		}
		itemsWithSerials = append(itemsWithSerials, row)
	}

	// Nombre proveedor
	supplierName := ""
	if p.ContactID != nil {
		var ct database.TenantContact
		if tdb.First(&ct, *p.ContactID).Error == nil {
			supplierName = ct.BusinessName
		}
	}

	return c.JSON(fiber.Map{
		"data": fiber.Map{
			"id":                 p.ID,
			"branch_id":          p.BranchID,
			"doc_type":           p.DocType,
			"series":             p.Series,
			"number":             p.Number,
			"issue_date":         p.IssueDate.Format("2006-01-02"),
			"contact_id":         p.ContactID,
			"supplier_name":      supplierName,
			"currency":           p.Currency,
			"subtotal":           p.Subtotal,
			"tax_amount":         p.TaxAmount,
			"total":              p.Total,
			"status":             p.Status,
			"notes":              p.Notes,
			"price_includes_igv": p.PriceIncludesIgv,
			"items":              itemsWithSerials,
			"linked_retention": func() any {
				if linked, _ := billingsvc.NewBillingService(tdb).GetLinkedRetentionByPurchaseID(p.ID); linked != nil {
					return linked
				}
				return nil
			}(),
		},
	})
}

// POST /api/purchases
func (h *PurchaseHandler) CreateAPI(c fiber.Ctx) error {
	tdb := db(c)

	var body struct {
		BranchID      uint   `json:"branch_id"`
		ContactID     *uint  `json:"contact_id"`
		DocType       string `json:"doc_type"`
		Series        string `json:"series"`
		Number        string `json:"number"`
		IssueDate     string `json:"issue_date"`
		Currency      string `json:"currency"`
		PaymentMethod string `json:"payment_method"`
		Notes         string `json:"notes"`
		// PriceIncludesIgv: criterio global de la compra (el costo tecleado ya trae IGV).
		PriceIncludesIgv bool                        `json:"price_includes_igv"`
		Items            []service.PurchaseItemInput `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}

	taxCfg := tax.LoadFromDB(tdb)

	// time.Parse("2006-01-02", ...) devuelve 00:00 en UTC. Con el driver en loc=Local
	// (contenedor en America/Lima) eso se persiste como las 19:00 del día anterior, así
	// que la compra quedaba guardada un día antes del que eligió el usuario. Mismo
	// tratamiento que ventas: se parsea en Perú y se fija al mediodía, para que ninguna
	// conversión de zona horaria vuelva a mover el documento de día.
	loc := datespe.Location()
	nowPe := time.Now().In(loc)
	issueDate := time.Date(nowPe.Year(), nowPe.Month(), nowPe.Day(), 12, 0, 0, 0, loc)
	if body.IssueDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", body.IssueDate, loc); err == nil {
			issueDate = time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
		}
	}

	input := service.CreatePurchaseInput{
		BranchID:         branchID,
		ContactID:        body.ContactID,
		UserID:           userID(c),
		DocType:          body.DocType,
		Series:           body.Series,
		Number:           body.Number,
		IssueDate:        issueDate,
		Currency:         body.Currency,
		PaymentMethod:    body.PaymentMethod,
		Notes:            body.Notes,
		PriceIncludesIgv: body.PriceIncludesIgv,
		Items:            body.Items,
		TaxConfig:        taxCfg,
	}

	purchase, err := service.NewPurchaseService(tdb).Create(input)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": purchase})
}

// POST /api/purchases/:id/void — anula la compra (revierte stock, kardex y seriales)
func (h *PurchaseHandler) VoidAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	tdb := db(c)
	err = service.NewPurchaseService(tdb).Void(uint(id), userID(c))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "message": "Compra anulada"})
}
