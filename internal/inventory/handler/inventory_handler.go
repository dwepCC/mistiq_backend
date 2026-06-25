package handler

import (
	"strconv"
	"strings"
	"time"

	"tukifac/internal/inventory/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type InventoryHandler struct{}

func NewInventoryHandler() *InventoryHandler { return &InventoryHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}
func email(c fiber.Ctx) string {
	v, _ := c.Locals("user_email").(string)
	return v
}
func userID(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}

func (h *InventoryHandler) IndexPage(c fiber.Ctx) error {
	svc := service.NewInventoryService(db(c))
	branchID, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	summary, _ := svc.StockSummary(uint(branchID))

	var branches []database.TenantBranch
	db(c).Where("active = ?", true).Find(&branches)

	return c.Render("inventory/index", fiber.Map{
		"Title":          "Inventario",
		"UserEmail":      email(c),
		"StockSummary":   summary,
		"Branches":       branches,
		"SelectedBranch": branchID,
		"Success":        c.Query("success"),
	}, "layouts/base")
}

func (h *InventoryHandler) KardexPage(c fiber.Ctx) error {
	productID, _ := strconv.ParseUint(c.Params("productId"), 10, 32)
	svc := service.NewInventoryService(db(c))

	var product database.TenantProduct
	if err := db(c).First(&product, productID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Producto no encontrado")
	}

	branchID, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	movements, _, _ := svc.GetKardex(service.KardexParams{
		ProductID: uint(productID),
		BranchID:  uint(branchID),
	})

	var branches []database.TenantBranch
	db(c).Where("active = ?", true).Find(&branches)

	return c.Render("inventory/kardex", fiber.Map{
		"Title":          "Kardex — " + product.Name,
		"UserEmail":      email(c),
		"Product":        product,
		"Movements":      movements,
		"Branches":       branches,
		"SelectedBranch": branchID,
	}, "layouts/base")
}

func (h *InventoryHandler) AdjustmentPage(c fiber.Ctx) error {
	var products []database.TenantProduct
	db(c).Where("manage_stock = ? AND active = ?", true, true).Order("name ASC").Find(&products)

	var branches []database.TenantBranch
	db(c).Where("active = ?", true).Find(&branches)

	return c.Render("inventory/adjustment", fiber.Map{
		"Title":    "Ajuste de Inventario",
		"UserEmail": email(c),
		"Products": products,
		"Branches": branches,
	}, "layouts/base")
}

func (h *InventoryHandler) AdjustmentSubmit(c fiber.Ctx) error {
	productID, _ := strconv.ParseUint(c.FormValue("product_id"), 10, 32)
	branchID, _ := strconv.ParseUint(c.FormValue("branch_id"), 10, 32)
	quantity, _ := strconv.ParseFloat(c.FormValue("quantity"), 64)
	unitCost, _ := strconv.ParseFloat(c.FormValue("unit_cost"), 64)
	movType := c.FormValue("type")
	if movType == "" {
		movType = "adjustment"
	}

	svc := service.NewInventoryService(db(c))
	err := svc.RecordMovement(service.MovementInput{
		ProductID: uint(productID),
		BranchID:  uint(branchID),
		Type:      movType,
		Quantity:  quantity,
		UnitCost:  unitCost,
		Reference: c.FormValue("reference"),
		Notes:     c.FormValue("notes"),
		UserID:    userID(c),
	})
	if err != nil {
		var products []database.TenantProduct
		db(c).Where("manage_stock = ? AND active = ?", true, true).Order("name ASC").Find(&products)
		var branches []database.TenantBranch
		db(c).Where("active = ?", true).Find(&branches)
		return c.Render("inventory/adjustment", fiber.Map{
			"Title":    "Ajuste de Inventario",
			"UserEmail": email(c),
			"Products": products,
			"Branches": branches,
			"Error":    err.Error(),
		}, "layouts/base")
	}
	return c.Redirect().To("/inventory?success=adjusted")
}

func (h *InventoryHandler) TransferPage(c fiber.Ctx) error {
	var products []database.TenantProduct
	db(c).Where("manage_stock = ? AND active = ?", true, true).Order("name ASC").Find(&products)

	var branches []database.TenantBranch
	db(c).Where("active = ?", true).Find(&branches)

	return c.Render("inventory/transfer", fiber.Map{
		"Title":    "Transferencia entre Sucursales",
		"UserEmail": email(c),
		"Products": products,
		"Branches": branches,
	}, "layouts/base")
}

func (h *InventoryHandler) TransferSubmit(c fiber.Ctx) error {
	productID, _ := strconv.ParseUint(c.FormValue("product_id"), 10, 32)
	fromBranchID, _ := strconv.ParseUint(c.FormValue("from_branch_id"), 10, 32)
	toBranchID, _ := strconv.ParseUint(c.FormValue("to_branch_id"), 10, 32)
	quantity, _ := strconv.ParseFloat(c.FormValue("quantity"), 64)

	svc := service.NewInventoryService(db(c))
	if err := svc.Transfer(uint(productID), uint(fromBranchID), uint(toBranchID), quantity, userID(c), c.FormValue("notes")); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/inventory?success=transferred")
}

// TransferAPI crea una transferencia en estado pending (flujo por estados). Body: from_branch_id, to_branch_id, notes, items: [{ product_id, quantity }].
func (h *InventoryHandler) TransferAPI(c fiber.Ctx) error {
	var body struct {
		FromBranchID uint `json:"from_branch_id"`
		ToBranchID   uint `json:"to_branch_id"`
		Notes        string `json:"notes"`
		Items        []struct {
			ProductID uint    `json:"product_id"`
			Quantity  float64 `json:"quantity"`
		} `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "payload inválido"})
	}
	if body.FromBranchID == 0 || body.ToBranchID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "from_branch_id y to_branch_id son requeridos"})
	}
	if body.FromBranchID == body.ToBranchID {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "origen y destino deben ser distintas sucursales"})
	}
	if len(body.Items) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "debe haber al menos un ítem"})
	}
	lines := make([]service.TransferLineInput, 0, len(body.Items))
	for _, it := range body.Items {
		if it.ProductID == 0 || it.Quantity <= 0 {
			continue
		}
		lines = append(lines, service.TransferLineInput{ProductID: it.ProductID, Quantity: it.Quantity})
	}
	if len(lines) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cada ítem debe tener product_id y quantity > 0"})
	}

	svc := service.NewInventoryService(db(c))
	transferID, err := svc.CreateTransferWithLines(body.FromBranchID, body.ToBranchID, userID(c), body.Notes, lines)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true, "transfer_id": transferID})
}

// TransfersListAPI devuelve el historial de transferencias por cabecera (estado pending/confirmed/cancelled) con líneas.
func (h *InventoryHandler) TransfersListAPI(c fiber.Ctx) error {
	svc := service.NewInventoryService(db(c))
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	transfers, logs, err := svc.ListTransfersByHeader(limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	var branches []database.TenantBranch
	db(c).Where("active = ?", true).Find(&branches)
	branchNames := make(map[uint]string)
	for _, b := range branches {
		branchNames[b.ID] = b.Name
	}
	productNames := make(map[uint]string)
	productIDs := make(map[uint]struct{})
	for _, l := range logs {
		productIDs[l.ProductID] = struct{}{}
	}
	if len(productIDs) > 0 {
		ids := make([]uint, 0, len(productIDs))
		for id := range productIDs {
			ids = append(ids, id)
		}
		var products []database.TenantProduct
		db(c).Where("id IN ?", ids).Find(&products)
		for _, p := range products {
			productNames[p.ID] = p.Name
		}
	}

	type lineRow struct {
		ProductID   uint    `json:"product_id"`
		ProductName string  `json:"product_name"`
		Quantity    float64 `json:"quantity"`
		WithSerials bool    `json:"with_serials"`
	}
	type transferRow struct {
		ID             uint        `json:"id"`
		FromBranchID   uint        `json:"from_branch_id"`
		FromBranchName string      `json:"from_branch_name"`
		ToBranchID     uint        `json:"to_branch_id"`
		ToBranchName   string      `json:"to_branch_name"`
		Status         string      `json:"status"`
		Notes          string      `json:"notes"`
		CreatedAt      time.Time   `json:"created_at"`
		ConfirmedAt    *time.Time  `json:"confirmed_at"`
		Lines          []lineRow   `json:"lines"`
	}
	out := make([]transferRow, 0, len(transfers))
	logsByTransfer := make(map[uint][]database.TenantTransferLog)
	for _, l := range logs {
		if l.TransferID != nil {
			logsByTransfer[*l.TransferID] = append(logsByTransfer[*l.TransferID], l)
		}
	}
	for _, t := range transfers {
		linesOut := make([]lineRow, 0)
		for _, l := range logsByTransfer[t.ID] {
			linesOut = append(linesOut, lineRow{
				ProductID:   l.ProductID,
				ProductName: productNames[l.ProductID],
				Quantity:    l.Quantity,
				WithSerials: l.SerialsJSON != "",
			})
		}
		out = append(out, transferRow{
			ID:             t.ID,
			FromBranchID:   t.FromBranchID,
			FromBranchName: branchNames[t.FromBranchID],
			ToBranchID:     t.ToBranchID,
			ToBranchName:   branchNames[t.ToBranchID],
			Status:         t.Status,
			Notes:          t.Notes,
			CreatedAt:      t.CreatedAt,
			ConfirmedAt:   t.ConfirmedAt,
			Lines:          linesOut,
		})
	}
	return c.JSON(fiber.Map{"data": out})
}

// TransferConfirmAPI confirma la recepción en destino. Solo transferencias en estado pending.
func (h *InventoryHandler) TransferConfirmAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewInventoryService(db(c))
	if err := svc.ConfirmTransfer(uint(id), userID(c)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true, "message": "Transferencia confirmada"})
}

// TransferCancelAPI cancela una transferencia pendiente (devuelve stock al origen).
func (h *InventoryHandler) TransferCancelAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewInventoryService(db(c))
	if err := svc.CancelTransfer(uint(id), userID(c)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true, "message": "Transferencia cancelada"})
}

// TransferReverseAPI anula una transferencia legacy (por ID de línea/log). Solo para registros sin transfer_id.
func (h *InventoryHandler) TransferReverseAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewInventoryService(db(c))
	if err := svc.ReverseTransfer(uint(id), userID(c)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true, "message": "Transferencia anulada"})
}

// StockAPI devuelve stock por producto (opcionalmente por sucursal).
func (h *InventoryHandler) StockAPI(c fiber.Ctx) error {
	productID, _ := strconv.ParseUint(c.Params("productId"), 10, 32)
	branchID, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)

	var stocks []database.TenantProductStock
	q := db(c).Where("product_id = ?", productID)
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	q.Find(&stocks)
	return c.JSON(fiber.Map{"data": stocks})
}

// StockSummaryAPI devuelve stock total por producto para una lista de IDs (query product_ids=1,2,3).
func (h *InventoryHandler) StockSummaryAPI(c fiber.Ctx) error {
	idsStr := c.Query("product_ids")
	if idsStr == "" {
		return c.JSON(fiber.Map{"data": map[string]float64{}})
	}
	var ids []uint
	for _, s := range strings.Split(idsStr, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if n, err := strconv.ParseUint(s, 10, 32); err == nil {
			ids = append(ids, uint(n))
		}
	}
	if len(ids) == 0 {
		return c.JSON(fiber.Map{"data": map[string]float64{}})
	}
	svc := service.NewInventoryService(db(c))
	totals, err := svc.StockTotalsByProductIDs(ids)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	out := make(map[string]float64)
	for k, v := range totals {
		out[strconv.FormatUint(uint64(k), 10)] = v
	}
	return c.JSON(fiber.Map{"data": out})
}

// AdjustmentAPI recibe un ajuste de inventario (aumentar o disminuir) y opcionalmente series.
func (h *InventoryHandler) AdjustmentAPI(c fiber.Ctx) error {
	var body struct {
		ProductID uint     `json:"product_id"`
		BranchID  uint     `json:"branch_id"`
		Type      string   `json:"type"` // "in" | "out"
		Quantity  float64  `json:"quantity"`
		Notes     string   `json:"notes"`
		Serials   []string `json:"serials"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "payload inválido"})
	}
	if body.ProductID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "product_id requerido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	svc := service.NewInventoryService(db(c))
	err = svc.RecordAdjustment(service.AdjustmentInput{
		ProductID: body.ProductID,
		BranchID:  branchID,
		Type:      body.Type,
		Quantity:  body.Quantity,
		Notes:     body.Notes,
		Serials:   body.Serials,
	}, userID(c))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (h *InventoryHandler) MovementsAPI(c fiber.Ctx) error {
	svc := service.NewInventoryService(db(c))
	tdb := db(c)
	productID, _ := strconv.ParseUint(c.Query("product_id"), 10, 32)
	catID, _ := strconv.ParseUint(c.Query("category_id"), 10, 32)
	reqB, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	branchID := branch.ResolveReadBranchFilter(c, uint(reqB))
	restaurantOnly := c.Query("restaurant_only") == "true" || c.Query("restaurant_only") == "1"

	var dateFrom, dateTo *time.Time
	if df := c.Query("date_from"); df != "" {
		if t, err := time.ParseInLocation("2006-01-02", df, time.Local); err == nil {
			start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
			dateFrom = &start
		}
	}
	if dt := c.Query("date_to"); dt != "" {
		if t, err := time.ParseInLocation("2006-01-02", dt, time.Local); err == nil {
			end := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.Local)
			dateTo = &end
		}
	}

	perPage, _ := strconv.Atoi(c.Query("per_page"))
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	params := service.KardexParams{
		ProductID:      uint(productID),
		ProductSearch:  c.Query("product_q"),
		CategoryID:     uint(catID),
		BranchID:       uint(branchID),
		DateFrom:       dateFrom,
		DateTo:         dateTo,
		MovementKind:   c.Query("movement_kind"),
		TextSearch:     c.Query("q"),
		RestaurantOnly: restaurantOnly,
	}
	if perPage > 0 {
		params.Limit = perPage
		params.Offset = (page - 1) * perPage
	}

	movements, total, err := svc.GetKardex(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Enriquecer con nombre de usuario
	userIDs := make(map[uint]struct{})
	for _, m := range movements {
		if m.UserID != 0 {
			userIDs[m.UserID] = struct{}{}
		}
	}
	userNames := make(map[uint]string)
	if len(userIDs) > 0 {
		ids := make([]uint, 0, len(userIDs))
		for id := range userIDs {
			ids = append(ids, id)
		}
		var users []database.TenantUser
		if err := tdb.Where("id IN ?", ids).Find(&users).Error; err == nil {
			for _, u := range users {
				display := u.Name
				if display == "" {
					display = u.Email
				}
				userNames[u.ID] = display
			}
		}
	}

	prodIDs := make(map[uint]struct{})
	brIDs := make(map[uint]struct{})
	for _, m := range movements {
		prodIDs[m.ProductID] = struct{}{}
		brIDs[m.BranchID] = struct{}{}
	}
	prodMap := make(map[uint]database.TenantProduct)
	if len(prodIDs) > 0 {
		pids := make([]uint, 0, len(prodIDs))
		for id := range prodIDs {
			pids = append(pids, id)
		}
		var products []database.TenantProduct
		if tdb.Where("id IN ?", pids).Find(&products).Error == nil {
			for _, p := range products {
				prodMap[p.ID] = p
			}
		}
	}
	brMap := make(map[uint]string)
	if len(brIDs) > 0 {
		bids := make([]uint, 0, len(brIDs))
		for id := range brIDs {
			bids = append(bids, id)
		}
		var branches []database.TenantBranch
		if tdb.Where("id IN ?", bids).Find(&branches).Error == nil {
			for _, b := range branches {
				brMap[b.ID] = b.Name
			}
		}
	}

	type movementRow struct {
		ID           uint      `json:"id"`
		ProductID    uint      `json:"product_id"`
		ProductCode  string    `json:"product_code"`
		ProductName  string    `json:"product_name"`
		BranchID     uint      `json:"branch_id"`
		BranchName   string    `json:"branch_name"`
		Type         string    `json:"type"`
		Quantity     float64   `json:"quantity"`
		UnitCost     float64   `json:"unit_cost"`
		Balance      float64   `json:"balance"`
		Reference    string    `json:"reference"`
		Notes        string    `json:"notes"`
		UserID       uint      `json:"user_id"`
		UserName     string    `json:"user_name"`
		CreatedAt    time.Time `json:"created_at"`
	}
	out := make([]movementRow, 0, len(movements))
	for _, m := range movements {
		pc, pn := "", ""
		if p, ok := prodMap[m.ProductID]; ok {
			pc = p.Code
			pn = p.Name
		}
		out = append(out, movementRow{
			ID:          m.ID,
			ProductID:   m.ProductID,
			ProductCode: pc,
			ProductName: pn,
			BranchID:    m.BranchID,
			BranchName:  brMap[m.BranchID],
			Type:        m.Type,
			Quantity:    m.Quantity,
			UnitCost:    m.UnitCost,
			Balance:     m.Balance,
			Reference:   m.Reference,
			Notes:       m.Notes,
			UserID:      m.UserID,
			UserName:    userNames[m.UserID],
			CreatedAt:   m.CreatedAt,
		})
	}
	return c.JSON(fiber.Map{"data": out, "total": total})
}
