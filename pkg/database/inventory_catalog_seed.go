package database

import (
	"gorm.io/gorm"
)

type inventoryOpSeed struct {
	Direction        string
	Code             string
	Name             string
	SunatCode        string
	AllowManual      bool
	RequiresDocument bool
	SortOrder        int
}

var inventoryOperationTypeSeeds = []inventoryOpSeed{
	// IN
	{Direction: "IN", Code: "PURCHASE", Name: "Compra", SunatCode: "02", AllowManual: false, RequiresDocument: true, SortOrder: 10},
	{Direction: "IN", Code: "INITIAL_STOCK", Name: "Inventario inicial", SunatCode: "16", AllowManual: false, RequiresDocument: false, SortOrder: 20},
	{Direction: "IN", Code: "RETURN_IN", Name: "Devolución recibida", SunatCode: "05", AllowManual: true, RequiresDocument: true, SortOrder: 30},
	{Direction: "IN", Code: "CONSIGNMENT_IN", Name: "Consignación recibida", SunatCode: "03", AllowManual: true, RequiresDocument: false, SortOrder: 40},
	{Direction: "IN", Code: "OTHER_IN", Name: "Otros", SunatCode: "99", AllowManual: true, RequiresDocument: false, SortOrder: 90},
	{Direction: "IN", Code: "INVENTORY_ADJUSTMENT_IN", Name: "Ajuste de Inventario (+)", SunatCode: "99", AllowManual: true, RequiresDocument: false, SortOrder: 85},
	{Direction: "IN", Code: "TRANSFER", Name: "Transferencia entre almacenes", SunatCode: "11", AllowManual: false, RequiresDocument: false, SortOrder: 95},
	// OUT
	{Direction: "OUT", Code: "SALE", Name: "Venta", SunatCode: "01", AllowManual: false, RequiresDocument: true, SortOrder: 10},
	{Direction: "OUT", Code: "RETURN_OUT", Name: "Devolución entregada", SunatCode: "06", AllowManual: true, RequiresDocument: true, SortOrder: 20},
	{Direction: "OUT", Code: "DONATION", Name: "Donación", SunatCode: "09", AllowManual: true, RequiresDocument: false, SortOrder: 30},
	{Direction: "OUT", Code: "PRODUCTION_OUT", Name: "Salida a producción", SunatCode: "10", AllowManual: true, RequiresDocument: false, SortOrder: 40},
	{Direction: "OUT", Code: "WITHDRAWAL", Name: "Retiro", SunatCode: "12", AllowManual: true, RequiresDocument: false, SortOrder: 50},
	{Direction: "OUT", Code: "SHRINKAGE", Name: "Merma", SunatCode: "13", AllowManual: true, RequiresDocument: false, SortOrder: 60},
	{Direction: "OUT", Code: "WASTE", Name: "Desmedro", SunatCode: "14", AllowManual: true, RequiresDocument: false, SortOrder: 70},
	{Direction: "OUT", Code: "DESTRUCTION", Name: "Destrucción", SunatCode: "15", AllowManual: true, RequiresDocument: false, SortOrder: 80},
	{Direction: "OUT", Code: "CONSIGNMENT_OUT", Name: "Consignación entregada", SunatCode: "04", AllowManual: true, RequiresDocument: false, SortOrder: 90},
	{Direction: "OUT", Code: "PROMOTION", Name: "Promoción", SunatCode: "07", AllowManual: true, RequiresDocument: false, SortOrder: 100},
	{Direction: "OUT", Code: "PRIZE", Name: "Premio", SunatCode: "08", AllowManual: true, RequiresDocument: false, SortOrder: 110},
	{Direction: "OUT", Code: "OTHER_OUT", Name: "Otros", SunatCode: "99", AllowManual: true, RequiresDocument: false, SortOrder: 190},
	{Direction: "OUT", Code: "INVENTORY_ADJUSTMENT_OUT", Name: "Ajuste de Inventario (-)", SunatCode: "99", AllowManual: true, RequiresDocument: false, SortOrder: 185},
}

// SeedInventoryOperationTypes inserta el catálogo de tipos de operación si no existe (idempotente por code).
func SeedInventoryOperationTypes(db *gorm.DB) error {
	for _, s := range inventoryOperationTypeSeeds {
		var count int64
		if err := db.Model(&TenantInventoryOperationType{}).Where("code = ?", s.Code).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		row := TenantInventoryOperationType{
			Direction:        s.Direction,
			Code:             s.Code,
			Name:             s.Name,
			SunatCode:        s.SunatCode,
			AllowManual:      s.AllowManual,
			RequiresDocument: s.RequiresDocument,
			SortOrder:        s.SortOrder,
			IsActive:         true,
		}
		if err := db.Select(
			"Direction", "Code", "Name", "SunatCode",
			"AllowManual", "RequiresDocument", "SortOrder", "IsActive",
		).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// SeedInventoryDocumentSeriesForBranch crea series ING001/EGR001 de almacén para una sucursal (idempotente).
func SeedInventoryDocumentSeriesForBranch(db *gorm.DB, branchID uint) error {
	if branchID == 0 {
		return nil
	}
	if err := ensureInventorySeries(db, branchID, "ING001", "INGRESO_INVENTARIO"); err != nil {
		return err
	}
	return ensureInventorySeries(db, branchID, "EGR001", "EGRESO_INVENTARIO")
}

// SeedInventoryDocumentSeriesForAllBranches siembra series en todas las sucursales activas.
func SeedInventoryDocumentSeriesForAllBranches(db *gorm.DB) error {
	var branches []TenantBranch
	if err := db.Where("active = ?", true).Find(&branches).Error; err != nil {
		return err
	}
	for _, b := range branches {
		if err := SeedInventoryDocumentSeriesForBranch(db, b.ID); err != nil {
			return err
		}
	}
	return nil
}

func ensureInventorySeries(db *gorm.DB, branchID uint, seriesCode, docType string) error {
	var count int64
	if err := db.Model(&TenantDocumentSeries{}).
		Where("branch_id = ? AND category = ? AND doc_type = ?", branchID, "almacen", docType).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	row := TenantDocumentSeries{
		BranchID:    branchID,
		DocType:     docType,
		SunatCode:   "00",
		Category:    "almacen",
		Series:      seriesCode,
		Correlative: 1,
		Active:      true,
	}
	return db.Create(&row).Error
}
