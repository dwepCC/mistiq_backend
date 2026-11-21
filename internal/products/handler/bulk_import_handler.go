package handler

import (
	"tukifac/internal/products/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// productImportQuota cupo de productos que le queda al tenant según su plan, para que las importaciones masivas no lo superen
// (cada petición lo recalcula, así que también se respeta al importar por lotes). nil = el plan no limita productos.
func productImportQuota(c fiber.Ctx, conn *gorm.DB) *service.ProductQuota {
	tenantID := middleware.TenantIDFromClaims(c)
	if tenantID == 0 || conn == nil {
		return nil
	}
	return service.NewProductQuota(saas.TenantPlanLimits(tenantID).MaxProducts, saas.CountProductSeats(conn))
}

// BulkImportRestaurantAPI POST /api/products/bulk-import/restaurant
func (h *ProductHandler) BulkImportRestaurantAPI(c fiber.Ctx) error {
	var body struct {
		BranchID uint                     `json:"branch_id"`
		Items    []service.BulkImportItem `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	if !middleware.CanViewProductCost(c) {
		for i := range body.Items {
			body.Items[i].PurchasePrice = nil // sin permiso de costo no se importa ni se pisa el costo
		}
	}
	uid, _ := c.Locals("user_id").(uint)
	svc := service.NewProductService(db(c))
	svc.SetImportQuota(productImportQuota(c, db(c)))
	res, err := svc.BulkImportRestaurant(body.Items, branchID, uid)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": res})
}

// BulkImportCatalogAPI POST /api/products/bulk-import/catalog
func (h *ProductHandler) BulkImportCatalogAPI(c fiber.Ctx) error {
	var body struct {
		BranchID uint                     `json:"branch_id"`
		Items    []service.BulkImportItem `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	if !middleware.CanViewProductCost(c) {
		for i := range body.Items {
			body.Items[i].PurchasePrice = nil // sin permiso de costo no se importa ni se pisa el costo
		}
	}
	uid, _ := c.Locals("user_id").(uint)
	svc := service.NewProductService(db(c))
	svc.SetImportQuota(productImportQuota(c, db(c)))
	res, err := svc.BulkImportCatalog(body.Items, branchID, uid)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": res})
}

// BulkUpdatePricesAPI PATCH /api/products/bulk-update-prices — "Actualizar precio" en Productos:
// actualiza precio de venta/compra en masa matcheando cada fila por código de barras.
func (h *ProductHandler) BulkUpdatePricesAPI(c fiber.Ctx) error {
	var body struct {
		BranchID uint `json:"branch_id"`
		Rows     []struct {
			RowNumber     int      `json:"row_number"`
			Code          string   `json:"code"`
			SalePrice     *float64 `json:"sale_price"`
			PurchasePrice *float64 `json:"purchase_price"`
		} `json:"rows"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	// branch_id > 0: precio POR SUCURSAL (override), solo de la sucursal activa (accesible y de escritura); antes se
	// validaba la sucursal pero se pisaba el precio GLOBAL de todas. Sin branch_id: precio global, solo de productos
	// visibles para el usuario.
	if _, err := branch.ResolveWriteBranchID(c, body.BranchID); err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	if body.BranchID > 0 && !branchPriceWriteAllowed(c, body.BranchID) {
		return writeForbidden(c)
	}
	// Sin sucursal el precio de venta es GLOBAL y el precio de compra (costo) siempre lo es: exigen products.manage_global.
	// El precio de venta por sucursal es información comercial de la sucursal y solo requiere products.edit.
	canGlobal := middleware.CanManageGlobalCatalog(c)
	if body.BranchID == 0 && !canGlobal {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "Actualizar el precio global requiere permiso sobre el catálogo global; elija una sucursal para cambiar su precio",
			"permission": middleware.PermProductsManageGlobal,
		})
	}
	canCost := middleware.CanViewProductCost(c) && canGlobal
	rows := make([]service.BulkPriceUpdateRow, 0, len(body.Rows))
	for _, r := range body.Rows {
		rows = append(rows, service.BulkPriceUpdateRow{
			RowNumber:     r.RowNumber,
			Code:          r.Code,
			SalePrice:     r.SalePrice,
			PurchasePrice: costIfAllowed(canCost, r.PurchasePrice),
		})
	}
	res, err := service.NewProductService(db(c)).BulkUpdatePricesWith(rows, service.BulkPriceUpdateOptions{
		BranchID: body.BranchID,
		Visible: func(p database.TenantProduct) bool { return productVisibleToUser(c, db(c), p.ID) },
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": res})
}

func costIfAllowed(allowed bool, v *float64) *float64 {
	if !allowed {
		return nil
	}
	return v
}

// BulkImportAdvancedAPI POST /api/products/bulk-import/advanced — importación avanzada: producto
// con presentaciones, unidades de venta, precios por sucursal y stock por sucursal (máx. 200
// productos por request). Ver service.BulkImportAdvanced.
func (h *ProductHandler) BulkImportAdvancedAPI(c fiber.Ctx) error {
	return h.bulkImportAdvanced(c, false)
}

// BulkImportRestaurantAdvancedAPI POST /api/products/bulk-import/restaurant-advanced — lo mismo para
// platos de Tukichef (is_restaurant forzado, sucursal activa obligatoria) con extras y combos.
func (h *ProductHandler) BulkImportRestaurantAdvancedAPI(c fiber.Ctx) error {
	return h.bulkImportAdvanced(c, true)
}

func (h *ProductHandler) bulkImportAdvanced(c fiber.Ctx, restaurant bool) error {
	var body struct {
		BranchID uint                             `json:"branch_id"`
		Items    []service.BulkImportAdvancedItem `json:"items"`
		// DryRun: simula la importación completa dentro de una transacción que se revierte, para
		// mostrar el resumen (creados/modificados/desactivados) y los errores antes de importar.
		DryRun bool `json:"dry_run"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	if !middleware.CanViewProductCost(c) {
		for i := range body.Items {
			body.Items[i].PurchasePrice = nil // sin permiso de costo no se importa ni se pisa el costo
		}
	}
	uid, _ := c.Locals("user_id").(uint)
	conn := db(c)
	quota := productImportQuota(c, conn) // se calcula ANTES de la transacción del dry_run: el simulacro respeta el mismo cupo
	if body.DryRun {
		tx := conn.Begin()
		if tx.Error != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": tx.Error.Error()})
		}
		defer tx.Rollback()
		conn = tx
	}
	opts := service.BulkImportAdvancedOpts{Restaurant: restaurant}
	if body.DryRun {
		// El dry_run simula el archivo completo en una sola transacción (los combos referencian
		// platos de otros lotes, que en un dry_run por lotes no existirían).
		opts.MaxItems = service.BulkImportMaxItems
	}
	svc := service.NewProductService(conn)
	svc.SetImportQuota(quota)
	res, err := svc.BulkImportAdvancedWith(body.Items, branchID, uid, opts)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": res})
}
