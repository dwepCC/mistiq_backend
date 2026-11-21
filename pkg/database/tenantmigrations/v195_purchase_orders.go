package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V195PurchaseOrders crea las órdenes de compra (cabecera + ítems), enlaza tenant_purchases con
// su orden de origen y siembra una serie de categoría "orden_compra" por sucursal activa.
// No mueve stock/caja/CxP: solo estructura y series. Idempotente.
type V195PurchaseOrders struct{}

func (V195PurchaseOrders) Version() int { return 195 }
func (V195PurchaseOrders) Name() string { return "purchase_orders" }

func (V195PurchaseOrders) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&database.TenantPurchaseOrder{},
		&database.TenantPurchaseOrderItem{},
		&v195Purchase{},
	); err != nil {
		return err
	}
	// La siembra recorre series de todas las sucursales: es acceso interno de migración.
	return seedPurchaseOrderSeries(branchscope.System(db, "V195 siembra de series de orden de compra"))
}

func seedPurchaseOrderSeries(db *gorm.DB) error {
	var branches []database.TenantBranch
	if err := db.Where("active = ?", true).Find(&branches).Error; err != nil {
		return err
	}
	for _, b := range branches {
		var count int64
		if err := db.Model(&database.TenantDocumentSeries{}).
			Where("branch_id = ? AND category = ?", b.ID, "orden_compra").
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		code, err := nextAvailablePurchaseOrderSeries(db)
		if err != nil {
			return fmt.Errorf("generar serie de orden de compra para sucursal %d: %w", b.ID, err)
		}
		row := database.TenantDocumentSeries{
			BranchID:    b.ID,
			DocType:     "Orden de compra",
			SunatCode:   "OC",
			Category:    "orden_compra",
			Series:      code,
			Correlative: 1,
			Active:      true,
		}
		if err := db.Create(&row).Error; err != nil {
			return fmt.Errorf("insertar serie %s para sucursal %d: %w", code, b.ID, err)
		}
	}
	return nil
}

// nextAvailablePurchaseOrderSeries: `series` es único por tenant completo (V052), así que el
// chequeo de disponibilidad no filtra por sucursal: OC01, OC02, ...
func nextAvailablePurchaseOrderSeries(db *gorm.DB) (string, error) {
	for i := 1; i <= 99; i++ {
		code := fmt.Sprintf("OC%02d", i)
		var dup int64
		if err := db.Model(&database.TenantDocumentSeries{}).Where("series = ?", code).Count(&dup).Error; err != nil {
			return "", err
		}
		if dup == 0 {
			return code, nil
		}
	}
	return "", fmt.Errorf("sin código de orden de compra disponible (OC01..OC99 agotado)")
}
