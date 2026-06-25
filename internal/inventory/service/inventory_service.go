package service

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type InventoryService struct {
	db *gorm.DB
}

func NewInventoryService(db *gorm.DB) *InventoryService {
	return &InventoryService{db: db}
}

type MovementInput struct {
	ProductID uint
	BranchID  uint
	Type      string // in, out, adjustment, transfer
	Quantity  float64
	UnitCost  float64
	Reference string
	Notes     string
	UserID    uint
}

// recordMovementTx registra un movimiento y actualiza stock dentro de una transacción existente.
func (s *InventoryService) recordMovementTx(tx *gorm.DB, input MovementInput) error {
	if input.ProductID == 0 || input.BranchID == 0 {
		return errors.New("producto y sucursal son requeridos")
	}
	if input.Quantity <= 0 {
		return errors.New("la cantidad debe ser mayor a cero")
	}
	var stock database.TenantProductStock
	tx.Where("product_id = ? AND branch_id = ?", input.ProductID, input.BranchID).First(&stock)
	var newBalance float64
	switch input.Type {
	case "in", "adjustment_in":
		newBalance = stock.Quantity + input.Quantity
	case "out", "adjustment_out":
		newBalance = stock.Quantity - input.Quantity
		if newBalance < 0 {
			return errors.New("stock insuficiente")
		}
	case "adjustment":
		newBalance = input.Quantity
	default:
		return errors.New("tipo de movimiento inválido")
	}
	movement := database.TenantStockMovement{
		ProductID: input.ProductID, BranchID: input.BranchID, Type: input.Type,
		Quantity: input.Quantity, UnitCost: input.UnitCost, Balance: newBalance,
		Reference: input.Reference, Notes: input.Notes, UserID: input.UserID,
		CreatedAt: time.Now(),
	}
	if err := tx.Create(&movement).Error; err != nil {
		return err
	}
	if stock.ID == 0 {
		return tx.Create(&database.TenantProductStock{
			ProductID: input.ProductID, BranchID: input.BranchID, Quantity: newBalance,
		}).Error
	}
	return tx.Model(&stock).Updates(map[string]interface{}{
		"quantity": newBalance, "updated_at": time.Now(),
	}).Error
}

// EnsureProductBranchLink crea fila de stock en 0 si no existe (asigna el producto a la sucursal para carta/POS).
func (s *InventoryService) EnsureProductBranchLink(productID, branchID uint) error {
	if productID == 0 || branchID == 0 {
		return errors.New("producto y sucursal son requeridos")
	}
	var stock database.TenantProductStock
	err := s.db.Where("product_id = ? AND branch_id = ?", productID, branchID).First(&stock).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return s.db.Create(&database.TenantProductStock{
		ProductID: productID,
		BranchID:  branchID,
		Quantity:  0,
	}).Error
}

// RecordInitialStock registra entrada inicial (kardex tipo in, referencia STOCK_INICIAL).
func (s *InventoryService) RecordInitialStock(productID, branchID uint, quantity float64, userID uint, notes string) error {
	if quantity <= 0 {
		return nil
	}
	if notes == "" {
		notes = "Stock inicial"
	}
	return s.RecordMovement(MovementInput{
		ProductID: productID,
		BranchID:  branchID,
		Type:      "in",
		Quantity:  quantity,
		Reference: "STOCK_INICIAL",
		Notes:     notes,
		UserID:    userID,
	})
}

// RecordMovement registra un movimiento de inventario y actualiza el stock.
func (s *InventoryService) RecordMovement(input MovementInput) error {
	if input.ProductID == 0 || input.BranchID == 0 {
		return errors.New("producto y sucursal son requeridos")
	}
	if input.Quantity <= 0 {
		return errors.New("la cantidad debe ser mayor a cero")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// Obtener stock actual
		var stock database.TenantProductStock
		tx.Where("product_id = ? AND branch_id = ?", input.ProductID, input.BranchID).First(&stock)

		// Calcular nuevo balance
		var newBalance float64
		switch input.Type {
		case "in", "adjustment_in":
			newBalance = stock.Quantity + input.Quantity
		case "out", "adjustment_out":
			newBalance = stock.Quantity - input.Quantity
			if newBalance < 0 {
				return errors.New("stock insuficiente")
			}
		case "adjustment":
			newBalance = input.Quantity // valor absoluto
		default:
			return errors.New("tipo de movimiento inválido")
		}

		// Guardar movimiento en kardex
		movement := database.TenantStockMovement{
			ProductID: input.ProductID,
			BranchID:  input.BranchID,
			Type:      input.Type,
			Quantity:  input.Quantity,
			UnitCost:  input.UnitCost,
			Balance:   newBalance,
			Reference: input.Reference,
			Notes:     input.Notes,
			UserID:    input.UserID,
			CreatedAt: time.Now(),
		}
		if err := tx.Create(&movement).Error; err != nil {
			return err
		}

		// Actualizar o crear registro de stock
		if stock.ID == 0 {
			return tx.Create(&database.TenantProductStock{
				ProductID: input.ProductID,
				BranchID:  input.BranchID,
				Quantity:  newBalance,
			}).Error
		}
		return tx.Model(&stock).Updates(map[string]interface{}{
			"quantity":   newBalance,
			"updated_at": time.Now(),
		}).Error
	})
}

// Transfer transfiere stock entre sucursales.
func (s *InventoryService) Transfer(productID, fromBranchID, toBranchID uint, quantity float64, userID uint, notes string) error {
	if fromBranchID == toBranchID {
		return errors.New("las sucursales origen y destino deben ser diferentes")
	}

	ref := "TRANSFER"
	if err := s.RecordMovement(MovementInput{
		ProductID: productID,
		BranchID:  fromBranchID,
		Type:      "out",
		Quantity:  quantity,
		Reference: ref,
		Notes:     notes,
		UserID:    userID,
	}); err != nil {
		return err
	}

	if err := s.RecordMovement(MovementInput{
		ProductID: productID,
		BranchID:  toBranchID,
		Type:      "in",
		Quantity:  quantity,
		Reference: ref,
		Notes:     notes,
		UserID:    userID,
	}); err != nil {
		return err
	}

	// Registrar en log para poder anular
	return s.db.Create(&database.TenantTransferLog{
		ProductID:    productID,
		FromBranchID: fromBranchID,
		ToBranchID:   toBranchID,
		Quantity:     quantity,
		SerialsJSON:  "",
		UserID:       userID,
		Notes:        notes,
		CreatedAt:    time.Now(),
	}).Error
}

// TransferByProduct transfiere stock: si el producto maneja series, usa TransferWithSerials con los primeros N seriales disponibles; si no, usa Transfer por cantidad.
// Solo permite transferir productos con ManageStock.
func (s *InventoryService) TransferByProduct(productID, fromBranchID, toBranchID uint, quantity float64, userID uint, notes string) error {
	var product database.TenantProduct
	if err := s.db.First(&product, productID).Error; err != nil {
		return err
	}
	if !product.ManageStock {
		return errors.New("el producto no controla stock; no se puede transferir")
	}
	if product.ManageSeries {
		n := int(quantity)
		if n <= 0 {
			return errors.New("la cantidad debe ser al menos 1 para productos con series")
		}
		var serials []database.TenantProductSerial
		if err := s.db.Where("product_id = ? AND branch_id = ? AND status = ?", productID, fromBranchID, "available").
			Order("id ASC").Limit(n).Find(&serials).Error; err != nil {
			return err
		}
		if len(serials) < n {
			return errors.New("no hay suficientes seriales disponibles en la sucursal origen para este producto")
		}
		serialStrs := make([]string, 0, len(serials))
		for _, ps := range serials {
			serialStrs = append(serialStrs, ps.Serial)
		}
		return s.TransferWithSerials(productID, fromBranchID, toBranchID, serialStrs, userID, notes)
	}
	return s.Transfer(productID, fromBranchID, toBranchID, quantity, userID, notes)
}

// TransferWithSerials transfiere stock con control de números de serie.
// Los seriales se marcan como "reserved" al iniciar y como "available" en la sucursal destino al confirmar.
func (s *InventoryService) TransferWithSerials(productID, fromBranchID, toBranchID uint, serials []string, userID uint, notes string) error {
	if fromBranchID == toBranchID {
		return errors.New("las sucursales origen y destino deben ser diferentes")
	}
	if len(serials) == 0 {
		return errors.New("debes especificar al menos un número de serie")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// Verificar que todos los seriales existen y están disponibles en la sucursal origen
		for _, serial := range serials {
			var ps database.TenantProductSerial
			if err := tx.Where("product_id = ? AND branch_id = ? AND serial = ? AND status = ?",
				productID, fromBranchID, serial, "available").First(&ps).Error; err != nil {
				return errors.New("el serial '" + serial + "' no está disponible en la sucursal origen")
			}
		}

		qty := float64(len(serials))

		// Registrar salida en kardex
		var fromStock database.TenantProductStock
		tx.Where("product_id = ? AND branch_id = ?", productID, fromBranchID).First(&fromStock)
		newFromQty := fromStock.Quantity - qty
		if newFromQty < 0 {
			return errors.New("stock insuficiente en sucursal origen")
		}

		ref := "TRANSFER-SERIAL"
		tx.Create(&database.TenantStockMovement{
			ProductID: productID, BranchID: fromBranchID, Type: "out",
			Quantity: qty, Balance: newFromQty, Reference: ref, Notes: notes, UserID: userID,
			CreatedAt: time.Now(),
		})
		tx.Model(&fromStock).Updates(map[string]interface{}{"quantity": newFromQty, "updated_at": time.Now()})

		// Registrar entrada en kardex destino
		var toStock database.TenantProductStock
		tx.Where("product_id = ? AND branch_id = ?", productID, toBranchID).First(&toStock)
		newToQty := toStock.Quantity + qty
		tx.Create(&database.TenantStockMovement{
			ProductID: productID, BranchID: toBranchID, Type: "in",
			Quantity: qty, Balance: newToQty, Reference: ref, Notes: notes, UserID: userID,
			CreatedAt: time.Now(),
		})
		if toStock.ID == 0 {
			tx.Create(&database.TenantProductStock{ProductID: productID, BranchID: toBranchID, Quantity: newToQty})
		} else {
			tx.Model(&toStock).Updates(map[string]interface{}{"quantity": newToQty, "updated_at": time.Now()})
		}

		// Reasignar seriales: cambiar branch_id a destino (disponibles de inmediato)
		// Para un flujo de confirmación, cambiar Status a "reserved" hasta que el destino confirme recepción
		for _, serial := range serials {
			tx.Model(&database.TenantProductSerial{}).
				Where("product_id = ? AND branch_id = ? AND serial = ?", productID, fromBranchID, serial).
				Updates(map[string]interface{}{
					"branch_id":  toBranchID,
					"status":     "available",
					"updated_at": time.Now(),
				})
		}

		serialsJSON, _ := json.Marshal(serials)
		return tx.Create(&database.TenantTransferLog{
			ProductID:    productID,
			FromBranchID: fromBranchID,
			ToBranchID:   toBranchID,
			Quantity:     qty,
			SerialsJSON:  string(serialsJSON),
			UserID:       userID,
			Notes:        notes,
			CreatedAt:    time.Now(),
		}).Error
	})
}

// ReserveSerials marca números de serie como "reserved" (ej: en proceso de traslado pendiente).
func (s *InventoryService) ReserveSerials(productID, branchID uint, serials []string) error {
	return s.db.Model(&database.TenantProductSerial{}).
		Where("product_id = ? AND branch_id = ? AND serial IN ? AND status = ?",
			productID, branchID, serials, "available").
		Updates(map[string]interface{}{"status": "reserved", "updated_at": time.Now()}).Error
}

// ReleaseSerials libera seriales reservados (cancela un traslado pendiente).
func (s *InventoryService) ReleaseSerials(productID, branchID uint, serials []string) error {
	return s.db.Model(&database.TenantProductSerial{}).
		Where("product_id = ? AND branch_id = ? AND serial IN ? AND status = ?",
			productID, branchID, serials, "reserved").
		Updates(map[string]interface{}{"status": "available", "updated_at": time.Now()}).Error
}

// ReverseTransfer anula una transferencia registrada: revierte stock y series al almacén de origen.
func (s *InventoryService) ReverseTransfer(logID, userID uint) error {
	var logRow database.TenantTransferLog
	if err := s.db.First(&logRow, logID).Error; err != nil {
		return err
	}
	if logRow.RevertedAt != nil {
		return errors.New("la transferencia ya fue anulada")
	}

	notes := "Anulación transferencia"
	// Revertir: mover de destino (ToBranchID) hacia origen (FromBranchID)
	if logRow.SerialsJSON != "" {
		var serials []string
		if err := json.Unmarshal([]byte(logRow.SerialsJSON), &serials); err != nil {
			return errors.New("error al leer seriales de la transferencia")
		}
		if err := s.TransferWithSerials(logRow.ProductID, logRow.ToBranchID, logRow.FromBranchID, serials, userID, notes); err != nil {
			return err
		}
	} else {
		if err := s.Transfer(logRow.ProductID, logRow.ToBranchID, logRow.FromBranchID, logRow.Quantity, userID, notes); err != nil {
			return err
		}
	}

	now := time.Now()
	return s.db.Model(&logRow).Update("reverted_at", now).Error
}

// ListTransferLogs devuelve el historial de transferencias (líneas) para compatibilidad.
func (s *InventoryService) ListTransferLogs(limit int) ([]database.TenantTransferLog, error) {
	if limit <= 0 {
		limit = 100
	}
	var logs []database.TenantTransferLog
	err := s.db.Order("created_at DESC").Limit(limit).Find(&logs).Error
	return logs, err
}

// TransferLineInput es una línea al crear una transferencia (flujo por estados).
type TransferLineInput struct {
	ProductID uint
	Quantity  float64
}

// CreateTransferWithLines crea una transferencia en estado pending: descuenta solo en origen; series quedan in_transit. Destino no se toca hasta ConfirmTransfer.
func (s *InventoryService) CreateTransferWithLines(fromBranchID, toBranchID, userID uint, notes string, lines []TransferLineInput) (transferID uint, err error) {
	if fromBranchID == toBranchID {
		return 0, errors.New("origen y destino deben ser distintas sucursales")
	}
	if len(lines) == 0 {
		return 0, errors.New("debe haber al menos una línea")
	}

	var transferIDOut uint
	err = s.db.Transaction(func(tx *gorm.DB) error {
		tr := database.TenantTransfer{
			FromBranchID: fromBranchID,
			ToBranchID:   toBranchID,
			Status:       "pending",
			Notes:        notes,
			CreatedAt:    time.Now(),
			CreatedBy:    userID,
		}
		if err := tx.Create(&tr).Error; err != nil {
			return err
		}
		transferIDOut = tr.ID

		ref := "TRANSFER-PENDING"
		for _, line := range lines {
			if line.ProductID == 0 || line.Quantity <= 0 {
				return errors.New("producto y cantidad requeridos en cada línea")
			}
			var product database.TenantProduct
			if err := tx.First(&product, line.ProductID).Error; err != nil {
				return errors.New("producto no encontrado")
			}
			if !product.ManageStock {
				return errors.New("el producto no controla stock: " + product.Name)
			}

			var serialsJSON string
			qty := line.Quantity

			if product.ManageSeries {
				n := int(qty)
				if n <= 0 {
					return errors.New("cantidad debe ser al menos 1 para productos con series")
				}
				var serials []database.TenantProductSerial
				if err := tx.Where("product_id = ? AND branch_id = ? AND status = ?", line.ProductID, fromBranchID, "available").
					Order("id ASC").Limit(n).Find(&serials).Error; err != nil {
					return err
				}
				if len(serials) < n {
					return errors.New("no hay suficientes seriales disponibles en origen para " + product.Name)
				}
				serialStrs := make([]string, 0, len(serials))
				for _, ps := range serials {
					serialStrs = append(serialStrs, ps.Serial)
				}
				serialsJSONBytes, _ := json.Marshal(serialStrs)
				serialsJSON = string(serialsJSONBytes)
				// Marcar seriales como in_transit (siguen en origen hasta confirmar)
				for _, serial := range serialStrs {
					if err := tx.Model(&database.TenantProductSerial{}).
						Where("product_id = ? AND branch_id = ? AND serial = ?", line.ProductID, fromBranchID, serial).
						Updates(map[string]interface{}{"status": "in_transit", "updated_at": time.Now()}).Error; err != nil {
						return err
					}
				}
			}

			// Salida en origen
			if err := s.recordMovementTx(tx, MovementInput{
				ProductID: line.ProductID, BranchID: fromBranchID, Type: "out",
				Quantity: qty, Reference: ref, Notes: notes, UserID: userID,
			}); err != nil {
				return err
			}

			logRow := database.TenantTransferLog{
				TransferID:   &tr.ID,
				ProductID:    line.ProductID,
				FromBranchID: fromBranchID,
				ToBranchID:   toBranchID,
				Quantity:     qty,
				SerialsJSON:  serialsJSON,
				UserID:       userID,
				Notes:        notes,
				CreatedAt:    time.Now(),
			}
			if err := tx.Create(&logRow).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return transferIDOut, nil
}

// ConfirmTransfer confirma la recepción en destino: suma stock/series en destino y marca la transferencia como confirmada. No reversible.
func (s *InventoryService) ConfirmTransfer(transferID, userID uint) error {
	var tr database.TenantTransfer
	if err := s.db.First(&tr, transferID).Error; err != nil {
		return err
	}
	if tr.Status != "pending" {
		return errors.New("solo se puede confirmar una transferencia en estado pendiente")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var logs []database.TenantTransferLog
		if err := tx.Where("transfer_id = ?", transferID).Find(&logs).Error; err != nil {
			return err
		}
		ref := "TRANSFER-CONFIRMED"
		now := time.Now()
		for _, logRow := range logs {
			if logRow.SerialsJSON != "" {
				var serials []string
				if err := json.Unmarshal([]byte(logRow.SerialsJSON), &serials); err != nil {
					return err
				}
				// Mover seriales de origen (in_transit) a destino (available)
				for _, serial := range serials {
					if err := tx.Model(&database.TenantProductSerial{}).
						Where("product_id = ? AND branch_id = ? AND serial = ? AND status = ?",
							logRow.ProductID, logRow.FromBranchID, serial, "in_transit").
						Updates(map[string]interface{}{
							"branch_id":  logRow.ToBranchID,
							"status":     "available",
							"updated_at": now,
						}).Error; err != nil {
						return err
					}
				}
			}
			// Entrada en destino
			if err := s.recordMovementTx(tx, MovementInput{
				ProductID: logRow.ProductID, BranchID: logRow.ToBranchID, Type: "in",
				Quantity: logRow.Quantity, Reference: ref, Notes: logRow.Notes, UserID: userID,
			}); err != nil {
				return err
			}
		}
		return tx.Model(&tr).Updates(map[string]interface{}{
			"status":       "confirmed",
			"confirmed_at": now,
			"confirmed_by": userID,
		}).Error
	})
}

// CancelTransfer cancela una transferencia pendiente: devuelve stock/series al origen. Solo permitido si status = pending.
func (s *InventoryService) CancelTransfer(transferID, userID uint) error {
	var tr database.TenantTransfer
	if err := s.db.First(&tr, transferID).Error; err != nil {
		return err
	}
	if tr.Status != "pending" {
		return errors.New("solo se puede cancelar una transferencia en estado pendiente")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var logs []database.TenantTransferLog
		if err := tx.Where("transfer_id = ?", transferID).Find(&logs).Error; err != nil {
			return err
		}
		ref := "TRANSFER-CANCELLED"
		now := time.Now()
		for _, logRow := range logs {
			if logRow.SerialsJSON != "" {
				var serials []string
				if err := json.Unmarshal([]byte(logRow.SerialsJSON), &serials); err != nil {
					return err
				}
				// Seriales siguen en origen; solo volver status a available
				for _, serial := range serials {
					if err := tx.Model(&database.TenantProductSerial{}).
						Where("product_id = ? AND branch_id = ? AND serial = ? AND status = ?",
							logRow.ProductID, logRow.FromBranchID, serial, "in_transit").
						Updates(map[string]interface{}{"status": "available", "updated_at": now}).Error; err != nil {
						return err
					}
				}
			}
			// Devolver stock al origen (entrada en origen = revertir la salida)
			if err := s.recordMovementTx(tx, MovementInput{
				ProductID: logRow.ProductID, BranchID: logRow.FromBranchID, Type: "in",
				Quantity: logRow.Quantity, Reference: ref, Notes: "Cancelación transferencia", UserID: userID,
			}); err != nil {
				return err
			}
		}
		return tx.Model(&tr).Update("status", "cancelled").Error
	})
}

// ListTransfersByHeader devuelve las transferencias agrupadas por cabecera (solo las que tienen transfer_id o todas las cabeceras).
func (s *InventoryService) ListTransfersByHeader(limit int) ([]database.TenantTransfer, []database.TenantTransferLog, error) {
	if limit <= 0 {
		limit = 50
	}
	var transfers []database.TenantTransfer
	if err := s.db.Order("created_at DESC").Limit(limit).Find(&transfers).Error; err != nil {
		return nil, nil, err
	}
	if len(transfers) == 0 {
		return transfers, nil, nil
	}
	ids := make([]uint, 0, len(transfers))
	for _, t := range transfers {
		ids = append(ids, t.ID)
	}
	var logs []database.TenantTransferLog
	if err := s.db.Where("transfer_id IN ?", ids).Order("transfer_id, id").Find(&logs).Error; err != nil {
		return nil, nil, err
	}
	return transfers, logs, nil
}

// StockTotalsByProductIDs devuelve el stock total (suma por sucursales) para cada product_id.
// Útil para listar productos con su stock en una sola llamada.
func (s *InventoryService) StockTotalsByProductIDs(productIDs []uint) (map[uint]float64, error) {
	if len(productIDs) == 0 {
		return map[uint]float64{}, nil
	}
	type row struct {
		ProductID uint
		Total     float64
	}
	var rows []row
	err := s.db.Model(&database.TenantProductStock{}).
		Select("product_id, COALESCE(SUM(quantity), 0) as total").
		Where("product_id IN ?", productIDs).
		Group("product_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]float64)
	for _, r := range rows {
		out[r.ProductID] = r.Total
	}
	for _, id := range productIDs {
		if _, ok := out[id]; !ok {
			out[id] = 0
		}
	}
	return out, nil
}

// AdjustmentInput para ajuste de inventario (desde API).
type AdjustmentInput struct {
	ProductID uint
	BranchID  uint
	Type      string   // "in" o "out"
	Quantity  float64
	Notes     string
	Serials   []string // Para productos con series: al in = nuevos seriales; al out = seriales a retirar
}

// RecordAdjustment registra un ajuste de stock y opcionalmente gestiona series.
// Solo para productos con manage_stock. Tipo "in" = aumentar, "out" = disminuir.
// Para productos con series: Type "in" requiere len(Serials)==Quantity (seriales nuevos);
// Type "out" requiere Serials con los seriales a retirar (cantidad debe coincidir).
func (s *InventoryService) RecordAdjustment(input AdjustmentInput, userID uint) error {
	if input.ProductID == 0 || input.BranchID == 0 {
		return errors.New("producto y sucursal son requeridos")
	}
	if input.Type != "in" && input.Type != "out" {
		return errors.New("tipo debe ser 'in' u 'out'")
	}
	if input.Quantity <= 0 {
		return errors.New("la cantidad debe ser mayor a cero")
	}

	var product database.TenantProduct
	if err := s.db.First(&product, input.ProductID).Error; err != nil {
		return err
	}
	if !product.ManageStock {
		return errors.New("el producto no controla stock")
	}

	ref := "AJUSTE"
	notes := input.Notes
	if notes == "" {
		notes = "Ajuste de inventario"
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
			return s.adjustmentInWithSerials(input.ProductID, input.BranchID, input.Serials, notes, userID)
		}
		// out
		if len(input.Serials) != n {
			return errors.New("debe seleccionar exactamente la misma cantidad de series a retirar")
		}
		return s.adjustmentOutWithSerials(input.ProductID, input.BranchID, input.Serials, notes, userID)
	}

	// Producto sin series
	movType := "adjustment_in"
	if input.Type == "out" {
		movType = "adjustment_out"
	}
	return s.RecordMovement(MovementInput{
		ProductID: input.ProductID,
		BranchID:  input.BranchID,
		Type:      movType,
		Quantity:  input.Quantity,
		Reference: ref,
		Notes:     notes,
		UserID:    userID,
	})
}

func (s *InventoryService) adjustmentInWithSerials(productID, branchID uint, serials []string, notes string, userID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, serial := range serials {
			serial = strings.TrimSpace(serial)
			if serial == "" {
				return errors.New("no se permiten seriales vacíos")
			}
			var exists int64
			tx.Model(&database.TenantProductSerial{}).Where("product_id = ? AND serial = ?", productID, serial).Count(&exists)
			if exists > 0 {
				return errors.New("el serial '" + serial + "' ya existe para este producto")
			}
		}

		qty := float64(len(serials))
		var stock database.TenantProductStock
		tx.Where("product_id = ? AND branch_id = ?", productID, branchID).First(&stock)
		newQty := stock.Quantity + qty

		tx.Create(&database.TenantStockMovement{
			ProductID: productID, BranchID: branchID, Type: "adjustment_in",
			Quantity: qty, Balance: newQty, Reference: "AJUSTE", Notes: notes, UserID: userID,
			CreatedAt: time.Now(),
		})
		if stock.ID == 0 {
			tx.Create(&database.TenantProductStock{ProductID: productID, BranchID: branchID, Quantity: newQty})
		} else {
			tx.Model(&stock).Updates(map[string]interface{}{"quantity": newQty, "updated_at": time.Now()})
		}
		for _, serial := range serials {
			tx.Create(&database.TenantProductSerial{
				ProductID: productID, BranchID: branchID, Serial: strings.TrimSpace(serial), Status: "available",
				CreatedAt: time.Now(), UpdatedAt: time.Now(),
			})
		}
		return nil
	})
}

func (s *InventoryService) adjustmentOutWithSerials(productID, branchID uint, serials []string, notes string, userID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, serial := range serials {
			var ps database.TenantProductSerial
			if err := tx.Where("product_id = ? AND branch_id = ? AND serial = ? AND status = ?",
				productID, branchID, serial, "available").First(&ps).Error; err != nil {
				return errors.New("el serial '" + serial + "' no está disponible en esta sucursal")
			}
		}

		qty := float64(len(serials))
		var stock database.TenantProductStock
		tx.Where("product_id = ? AND branch_id = ?", productID, branchID).First(&stock)
		newQty := stock.Quantity - qty
		if newQty < 0 {
			return errors.New("stock insuficiente")
		}

		tx.Create(&database.TenantStockMovement{
			ProductID: productID, BranchID: branchID, Type: "adjustment_out",
			Quantity: qty, Balance: newQty, Reference: "AJUSTE", Notes: notes, UserID: userID,
			CreatedAt: time.Now(),
		})
		tx.Model(&stock).Updates(map[string]interface{}{"quantity": newQty, "updated_at": time.Now()})
		for _, serial := range serials {
			tx.Model(&database.TenantProductSerial{}).
				Where("product_id = ? AND branch_id = ? AND serial = ?", productID, branchID, serial).
				Updates(map[string]interface{}{"status": "removed", "updated_at": time.Now()})
		}
		return nil
	})
}

type KardexParams struct {
	ProductID       uint
	ProductSearch   string // LIKE en nombre o código de producto (JOIN)
	CategoryID      uint   // filtro por categoría del producto (JOIN)
	BranchID        uint
	DateFrom        *time.Time
	DateTo          *time.Time
	MovementKind    string // vacío, purchase_in, sale_out, transfer, adjustment, in, out
	TextSearch      string // referencia o notas
	RestaurantOnly  bool   // is_restaurant=true y manage_stock=true
	Limit           int
	Offset          int
}

// GetKardex lista movimientos de kardex con filtros opcionales y paginación (Limit>0).
func (s *InventoryService) GetKardex(params KardexParams) ([]database.TenantStockMovement, int64, error) {
	var movements []database.TenantStockMovement
	q := s.db.Model(&database.TenantStockMovement{})

	if params.ProductID > 0 {
		q = q.Where("tenant_stock_movements.product_id = ?", params.ProductID)
	}
	ps := strings.TrimSpace(params.ProductSearch)
	needProductJoin := params.RestaurantOnly || ps != "" || params.CategoryID > 0
	if needProductJoin {
		q = q.Joins("INNER JOIN tenant_products p ON p.id = tenant_stock_movements.product_id")
		if params.RestaurantOnly {
			q = q.Where("p.is_restaurant = ? AND p.manage_stock = ?", true, true)
		}
		if params.CategoryID > 0 {
			q = q.Where("p.category_id = ?", params.CategoryID)
		}
		if ps != "" {
			term := "%" + ps + "%"
			q = q.Where("(p.name LIKE ? OR p.code LIKE ?)", term, term)
		}
	}
	if params.BranchID > 0 {
		q = q.Where("tenant_stock_movements.branch_id = ?", params.BranchID)
	}
	if params.DateFrom != nil {
		q = q.Where("tenant_stock_movements.created_at >= ?", params.DateFrom)
	}
	if params.DateTo != nil {
		q = q.Where("tenant_stock_movements.created_at <= ?", params.DateTo)
	}

	switch strings.TrimSpace(params.MovementKind) {
	case "purchase_in":
		q = q.Where("tenant_stock_movements.type = ? AND tenant_stock_movements.reference LIKE ?", "in", "COMPRA/%")
	case "sale_out":
		q = q.Where("tenant_stock_movements.type = ? AND tenant_stock_movements.reference LIKE ?", "out", "VENTA/%")
	case "transfer":
		q = q.Where("tenant_stock_movements.reference LIKE ?", "TRANSFER%")
	case "adjustment":
		q = q.Where("tenant_stock_movements.type IN ?", []string{"adjustment_in", "adjustment_out"})
	case "in":
		q = q.Where("tenant_stock_movements.type = ?", "in")
	case "out":
		q = q.Where("tenant_stock_movements.type = ?", "out")
	}

	if ts := strings.TrimSpace(params.TextSearch); ts != "" {
		t := "%" + ts + "%"
		q = q.Where("(tenant_stock_movements.reference LIKE ? OR tenant_stock_movements.notes LIKE ?)", t, t)
	}

	var total int64
	if params.Limit > 0 {
		if err := q.Count(&total).Error; err != nil {
			return nil, 0, err
		}
		q = q.Offset(params.Offset).Limit(params.Limit)
	}

	err := q.Order("tenant_stock_movements.created_at DESC").Find(&movements).Error
	if err != nil {
		return nil, 0, err
	}
	if params.Limit == 0 {
		total = int64(len(movements))
	}
	return movements, total, nil
}

type StockSummary struct {
	ProductID   uint
	ProductName string
	ProductCode string
	BranchID    uint
	BranchName  string
	Quantity    float64
	MinStock    float64
	IsLow       bool
}

func (s *InventoryService) StockSummary(branchID uint) ([]StockSummary, error) {
	type result struct {
		ProductID   uint    `json:"product_id"`
		ProductName string  `json:"product_name"`
		ProductCode string  `json:"product_code"`
		BranchID    uint    `json:"branch_id"`
		BranchName  string  `json:"branch_name"`
		Quantity    float64 `json:"quantity"`
		MinStock    float64 `json:"min_stock"`
	}

	var results []result
	q := s.db.Table("tenant_product_stocks ps").
		Select("ps.product_id, p.name as product_name, p.code as product_code, ps.branch_id, b.name as branch_name, ps.quantity, p.min_stock").
		Joins("JOIN tenant_products p ON p.id = ps.product_id").
		Joins("JOIN tenant_branches b ON b.id = ps.branch_id").
		Where("p.manage_stock = ? AND p.active = ?", true, true)

	if branchID > 0 {
		q = q.Where("ps.branch_id = ?", branchID)
	}

	if err := q.Order("p.name ASC").Scan(&results).Error; err != nil {
		return nil, err
	}

	summaries := make([]StockSummary, len(results))
	for i, r := range results {
		summaries[i] = StockSummary{
			ProductID:   r.ProductID,
			ProductName: r.ProductName,
			ProductCode: r.ProductCode,
			BranchID:    r.BranchID,
			BranchName:  r.BranchName,
			Quantity:    r.Quantity,
			MinStock:    r.MinStock,
			IsLow:       r.Quantity <= r.MinStock,
		}
	}
	return summaries, nil
}
