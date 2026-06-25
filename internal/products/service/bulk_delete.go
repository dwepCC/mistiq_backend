package service

import (
	"errors"
	"log"
	"strings"

	restaurantsvc "tukifac/internal/restaurant/service"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

const (
	blockReasonNotFound        = "Producto no encontrado"
	blockReasonNotRestaurant   = "No es un producto restaurante"
	blockReasonBranchMismatch  = "El producto no pertenece a la sucursal activa"
	blockReasonHasSales        = "Tiene ventas registradas"
	blockReasonHasPurchases      = "Tiene compras registradas"
	blockReasonHasComandas     = "Tiene comandas registradas"
	blockReasonHasKardex       = "Tiene movimientos de inventario"
	blockReasonHasStock        = "Tiene stock disponible"
	blockReasonHasTransfers    = "Tiene transferencias registradas"
	blockReasonHasSerials      = "Tiene series asociadas"
	blockReasonHasMemberships  = "Tiene membresías asociadas"
)

// PinVerificationError indica PIN inválido o no configurado (HTTP 403).
type PinVerificationError struct {
	Message string
}

func (e *PinVerificationError) Error() string {
	if e == nil || e.Message == "" {
		return "PIN incorrecto"
	}
	return e.Message
}

// BulkDeleteRestaurantInput parámetros de eliminación masiva restaurante.
type BulkDeleteRestaurantInput struct {
	ProductIDs []uint
	Pin        string
	Reason     string
	UserID     uint
	BranchID   uint
}

type BulkDeleteProductRef struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type BulkDeleteBlockedItem struct {
	ID      uint     `json:"id"`
	Name    string   `json:"name"`
	Reasons []string `json:"reasons"`
}

// BulkDeleteRestaurantResult resultado parcial por producto.
type BulkDeleteRestaurantResult struct {
	Deleted []BulkDeleteProductRef  `json:"deleted"`
	Blocked []BulkDeleteBlockedItem `json:"blocked"`
}

// BulkDeleteRestaurant elimina productos restaurante tras validar PIN y dependencias.
// Estrategia: eliminación parcial; una transacción por producto válido.
func (s *ProductService) BulkDeleteRestaurant(in BulkDeleteRestaurantInput) (*BulkDeleteRestaurantResult, error) {
	if err := restaurantsvc.New(s.db).VerifyDeletionPin(in.Pin); err != nil {
		return nil, &PinVerificationError{Message: err.Error()}
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, errors.New("se requiere motivo")
	}
	if len(in.ProductIDs) == 0 {
		return nil, errors.New("se requiere al menos un producto")
	}

	unique := dedupeUints(in.ProductIDs)
	result := &BulkDeleteRestaurantResult{
		Deleted: make([]BulkDeleteProductRef, 0),
		Blocked: make([]BulkDeleteBlockedItem, 0),
	}

	productsByID := make(map[uint]database.TenantProduct, len(unique))
	var found []database.TenantProduct
	if err := s.db.Where("id IN ?", unique).Find(&found).Error; err != nil {
		return nil, err
	}
	for _, p := range found {
		productsByID[p.ID] = p
	}

	blockers := s.scanBulkDeleteBlockers(unique)

	for _, id := range unique {
		p, ok := productsByID[id]
		if !ok {
			result.Blocked = append(result.Blocked, BulkDeleteBlockedItem{
				ID: id, Name: "", Reasons: []string{blockReasonNotFound},
			})
			continue
		}
		reasons := append([]string{}, blockers[id]...)
		if !p.IsRestaurant {
			reasons = appendUniqueReason(reasons, blockReasonNotRestaurant)
		}
		if in.BranchID > 0 {
			if err := s.EnsureRestaurantBranchAccess(&p, in.BranchID); err != nil {
				reasons = appendUniqueReason(reasons, blockReasonBranchMismatch)
			}
		}
		if len(reasons) > 0 {
			result.Blocked = append(result.Blocked, BulkDeleteBlockedItem{
				ID: id, Name: p.Name, Reasons: reasons,
			})
			continue
		}
		if err := s.purgeRestaurantProduct(id); err != nil {
			result.Blocked = append(result.Blocked, BulkDeleteBlockedItem{
				ID: id, Name: p.Name, Reasons: []string{err.Error()},
			})
			continue
		}
		result.Deleted = append(result.Deleted, BulkDeleteProductRef{ID: id, Name: p.Name})
	}

	// No existe tabla de auditoría tenant para productos; traza mínima en log de aplicación.
	log.Printf(
		"[bulk-delete-restaurant] user_id=%d branch_id=%d requested=%d deleted=%d blocked=%d reason=%q",
		in.UserID, in.BranchID, len(unique), len(result.Deleted), len(result.Blocked), reason,
	)

	return result, nil
}

func dedupeUints(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func appendUniqueReason(reasons []string, reason string) []string {
	for _, r := range reasons {
		if r == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

type productIDRow struct {
	ProductID uint `gorm:"column:product_id"`
}

func (s *ProductService) scanBulkDeleteBlockers(ids []uint) map[uint][]string {
	blockers := make(map[uint][]string)
	if len(ids) == 0 {
		return blockers
	}
	add := func(pid uint, reason string) {
		if pid == 0 {
			return
		}
		blockers[pid] = appendUniqueReason(blockers[pid], reason)
	}

	var rows []productIDRow

	s.db.Model(&database.TenantSaleItem{}).
		Select("DISTINCT product_id").
		Where("product_id IN ?", ids).
		Scan(&rows)
	for _, r := range rows {
		add(r.ProductID, blockReasonHasSales)
	}

	rows = nil
	s.db.Model(&database.TenantPurchaseItem{}).
		Select("DISTINCT product_id").
		Where("product_id IN ?", ids).
		Scan(&rows)
	for _, r := range rows {
		add(r.ProductID, blockReasonHasPurchases)
	}

	rows = nil
	s.db.Model(&database.TenantComanda{}).
		Select("DISTINCT product_id").
		Where("product_id IN ?", ids).
		Scan(&rows)
	for _, r := range rows {
		add(r.ProductID, blockReasonHasComandas)
	}

	rows = nil
	s.db.Model(&database.TenantStockMovement{}).
		Select("DISTINCT product_id").
		Where("product_id IN ?", ids).
		Scan(&rows)
	for _, r := range rows {
		add(r.ProductID, blockReasonHasKardex)
	}

	type stockRow struct {
		ProductID uint `gorm:"column:product_id"`
	}
	var stockRows []stockRow
	s.db.Table("tenant_product_stocks").
		Select("product_id").
		Where("product_id IN ?", ids).
		Group("product_id").
		Having("COALESCE(SUM(quantity), 0) > 0").
		Scan(&stockRows)
	for _, r := range stockRows {
		add(r.ProductID, blockReasonHasStock)
	}

	rows = nil
	s.db.Model(&database.TenantTransferLog{}).
		Select("DISTINCT product_id").
		Where("product_id IN ?", ids).
		Scan(&rows)
	for _, r := range rows {
		add(r.ProductID, blockReasonHasTransfers)
	}

	rows = nil
	s.db.Model(&database.TenantProductSerial{}).
		Select("DISTINCT product_id").
		Where("product_id IN ?", ids).
		Scan(&rows)
	for _, r := range rows {
		add(r.ProductID, blockReasonHasSerials)
	}

	rows = nil
	s.db.Model(&database.TenantMembership{}).
		Select("DISTINCT product_id").
		Where("product_id IN ?", ids).
		Scan(&rows)
	for _, r := range rows {
		add(r.ProductID, blockReasonHasMemberships)
	}

	return blockers
}

// purgeRestaurantProduct elimina físicamente un producto restaurante válido y relaciones directas permitidas.
// Solo invocado desde BulkDeleteRestaurant (productos sin historial). No reutiliza Delete().
func (s *ProductService) purgeRestaurantProduct(productID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("product_id = ?", productID).Delete(&database.TenantProductPresentation{}).Error; err != nil {
			return err
		}
		if err := tx.Where("product_id = ?", productID).Delete(&database.TenantProductModifierGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("product_id = ?", productID).Delete(&database.TenantProductStock{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(&database.TenantProduct{}, productID).Error
	})
}
