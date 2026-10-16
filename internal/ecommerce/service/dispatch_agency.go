package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultPickupDeadlineDays = 15

// AgencyArrivalInput llegada del paquete a la agencia de destino. ArrivedAt vacío = ahora.
type AgencyArrivalInput struct {
	DispatchTransitionInput
	ArrivedAt *time.Time
}

// MarkDispatchInAgency DESPACHADO|EN_TRANSITO → EN_AGENCIA. Registra cuándo llegó el paquete y fija
// la fecha límite de recojo (llegada + plazo del transportista, 15 días por defecto). El pedido se
// queda en DESPACHADO: «en agencia» es una etapa del despacho, no del pedido.
func (s *EcommerceService) MarkDispatchInAgency(dispatchID uint, in AgencyArrivalInput) (*database.TenantEcommerceDispatch, error) {
	var dispatch database.TenantEcommerceDispatch
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&dispatch, dispatchID).Error; err != nil {
			return errors.New("despacho no encontrado")
		}
		if dispatch.Status != DispatchStatusDespachado && dispatch.Status != DispatchStatusEnTransito {
			return fmt.Errorf("solo un despacho en camino puede pasar a la agencia (estado actual: %s)", dispatch.Status)
		}
		now := time.Now()
		arrived := now
		if in.ArrivedAt != nil {
			arrived = *in.ArrivedAt
			if arrived.After(now.Add(2 * time.Hour)) {
				return errors.New("la fecha de llegada no puede ser futura")
			}
			if dispatch.DispatchedAt != nil && arrived.Before(dispatch.DispatchedAt.Add(-24*time.Hour)) {
				return errors.New("la llegada no puede ser anterior al despacho")
			}
		}
		days := defaultPickupDeadlineDays
		if dispatch.CarrierID != nil {
			var carrier database.TenantCarrier
			if err := tx.Unscoped().First(&carrier, *dispatch.CarrierID).Error; err == nil && carrier.PickupDeadlineDays > 0 {
				days = carrier.PickupDeadlineDays
			}
		}
		deadline := arrived.Add(time.Duration(days) * 24 * time.Hour)
		if err := tx.Model(&database.TenantEcommerceDispatch{}).Where("id = ?", dispatchID).Updates(map[string]interface{}{
			"status": DispatchStatusEnAgencia, "agency_arrived_at": arrived, "pickup_deadline": deadline,
		}).Error; err != nil {
			return err
		}
		var userID *uint
		if in.UserID > 0 {
			userID = &in.UserID
		}
		if err := tx.Create(&database.TenantEcommerceDispatchStatusHistory{
			DispatchID: dispatchID, FromStatus: dispatch.Status, ToStatus: DispatchStatusEnAgencia,
			UserID: userID, Notes: strings.TrimSpace(in.Notes),
		}).Error; err != nil {
			return err
		}
		dispatch.Status = DispatchStatusEnAgencia
		dispatch.AgencyArrivedAt = &arrived
		dispatch.PickupDeadline = &deadline
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &dispatch, nil
}

// MarkDispatchReturned DESPACHADO|EN_TRANSITO|EN_AGENCIA → DEVUELTO y pedido DESPACHADO → DEVUELTO,
// en una sola transacción: el paquete regresó al negocio (el cliente no lo recogió o la agencia lo
// devolvió). Exige el motivo. Si el pedido ya se había convertido en venta, la venta NO se anula
// aquí: el panel avisa para que se anule desde Mis ventas (eso repone el stock).
func (s *EcommerceService) MarkDispatchReturned(dispatchID uint, in DispatchTransitionInput) (*database.TenantEcommerceDispatch, error) {
	if strings.TrimSpace(in.Notes) == "" {
		return nil, errors.New("indica el motivo del retorno")
	}
	var dispatch database.TenantEcommerceDispatch
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&dispatch, dispatchID).Error; err != nil {
			return errors.New("despacho no encontrado")
		}
		from := dispatch.Status
		if from != DispatchStatusDespachado && from != DispatchStatusEnTransito && from != DispatchStatusEnAgencia {
			return fmt.Errorf("solo un paquete que está en camino o en la agencia puede registrarse como retorno (estado actual: %s)", from)
		}
		var order database.TenantEcommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, dispatch.OrderID).Error; err != nil {
			return errors.New("pedido no encontrado")
		}
		if order.Status != OrderStatusDespachado {
			return fmt.Errorf("el pedido debe estar DESPACHADO para registrar el retorno (estado actual: %s)", order.Status)
		}
		now := time.Now()
		if err := tx.Model(&database.TenantEcommerceDispatch{}).Where("id = ?", dispatchID).
			Updates(map[string]interface{}{"status": DispatchStatusDevuelto, "returned_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.TenantEcommerceOrder{}).Where("id = ?", order.ID).
			Update("status", OrderStatusDevuelto).Error; err != nil {
			return err
		}
		var userID *uint
		if in.UserID > 0 {
			userID = &in.UserID
		}
		notes := strings.TrimSpace(in.Notes)
		if err := tx.Create(&database.TenantEcommerceDispatchStatusHistory{
			DispatchID: dispatchID, FromStatus: from, ToStatus: DispatchStatusDevuelto, UserID: userID, Notes: notes,
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.TenantEcommerceOrderStatusHistory{
			OrderID: order.ID, FromStatus: OrderStatusDespachado, ToStatus: OrderStatusDevuelto, UserID: userID, Notes: notes,
		}).Error; err != nil {
			return err
		}
		dispatch.Status = DispatchStatusDevuelto
		dispatch.ReturnedAt = &now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &dispatch, nil
}

// BatchDispatchItem un pedido a despachar dentro de un lote, con su guía.
type BatchDispatchItem struct {
	OrderID      uint
	TrackingCode *string
	PackageCount *int
	WeightKg     *float64
}

// BatchDispatchResult resultado por pedido: un lote nunca falla completo por un solo pedido.
type BatchDispatchResult struct {
	OrderID  uint                              `json:"order_id"`
	OK       bool                              `json:"ok"`
	Error    string                            `json:"error,omitempty"`
	Dispatch *database.TenantEcommerceDispatch `json:"dispatch,omitempty"`
	// SaleID / SaleError: resultado de emitir la venta al despachar. Si la venta falla, el despacho
	// igual quedó hecho y la venta se puede emitir después desde el pedido.
	SaleID    *uint  `json:"sale_id,omitempty"`
	SaleError string `json:"sale_error,omitempty"`
}

const maxBatchDispatch = 100

// BatchDispatch despacha varios pedidos (cada uno en su propia transacción) y devuelve el resultado
// de cada uno. Con emit != nil, cada pedido despachado también emite su venta.
func (s *EcommerceService) BatchDispatch(items []BatchDispatchItem, userID uint, emit *EmitSaleInput) ([]BatchDispatchResult, error) {
	if len(items) == 0 {
		return nil, errors.New("no hay pedidos para despachar")
	}
	if len(items) > maxBatchDispatch {
		return nil, fmt.Errorf("máximo %d pedidos por lote", maxBatchDispatch)
	}
	results := make([]BatchDispatchResult, 0, len(items))
	seen := map[uint]bool{}
	for _, it := range items {
		res := BatchDispatchResult{OrderID: it.OrderID}
		if seen[it.OrderID] {
			res.Error = "el pedido está repetido en el lote"
			results = append(results, res)
			continue
		}
		seen[it.OrderID] = true
		d, err := s.CreateDispatch(it.OrderID, CreateDispatchInput{
			TrackingCode: it.TrackingCode, PackageCount: it.PackageCount, WeightKg: it.WeightKg, UserID: userID,
		})
		if err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
			res.Dispatch = d
			if emit != nil {
				in := *emit
				in.UserID = userID
				if sale, serr := s.EmitSaleForOrder(it.OrderID, in); serr != nil {
					res.SaleError = serr.Error()
				} else {
					id := sale.ID
					res.SaleID = &id
				}
			}
		}
		results = append(results, res)
	}
	return results, nil
}
