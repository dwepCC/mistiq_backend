package service

import (
	"strings"
	"time"

	"tukifac/pkg/database"
)

// CancelledComandaView línea de comanda anulada con el contexto de su pedido/mesa y quién la anuló.
type CancelledComandaView struct {
	database.TenantComanda
	OrderNumber     int    `json:"order_number"`
	CancelledByName string `json:"cancelled_by_name"`
	KitchenSessionMeta
}

// CancelledComandasFilter filtros del historial de comandas anuladas. From/To son fechas de
// anulación (To inclusivo); cero = sin límite.
type CancelledComandasFilter struct {
	BranchID uint
	From     time.Time
	To       time.Time
	Query    string
	Page     int
	PerPage  int
}

// ListCancelledComandas historial de comandas anuladas de la sucursal, de la más reciente a la
// más antigua. Incluye las anuladas una a una y las que se anularon con la sesión completa.
func (s *RestaurantService) ListCancelledComandas(f CancelledComandasFilter) ([]CancelledComandaView, int64, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 || f.PerPage > 200 {
		f.PerPage = 50
	}

	q := s.db.Model(&database.TenantComanda{}).
		Joins("JOIN tenant_table_sessions ts ON ts.id = tenant_comandas.session_id").
		Where("ts.branch_id = ? AND tenant_comandas.cancelled_at IS NOT NULL", f.BranchID)
	if !f.From.IsZero() {
		q = q.Where("tenant_comandas.cancelled_at >= ?", f.From)
	}
	if !f.To.IsZero() {
		q = q.Where("tenant_comandas.cancelled_at < ?", f.To.AddDate(0, 0, 1))
	}
	if t := strings.TrimSpace(f.Query); t != "" {
		like := "%" + strings.ToLower(t) + "%"
		q = q.Where("LOWER(tenant_comandas.product_name) LIKE ? OR LOWER(tenant_comandas.cancel_reason) LIKE ?", like, like)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var comandas []database.TenantComanda
	if err := q.Order("tenant_comandas.cancelled_at DESC, tenant_comandas.id DESC").
		Limit(f.PerPage).Offset((f.Page - 1) * f.PerPage).
		Find(&comandas).Error; err != nil {
		return nil, 0, err
	}
	if len(comandas) == 0 {
		return []CancelledComandaView{}, total, nil
	}

	sessionIDs := make([]uint, 0, len(comandas))
	orderIDs := make([]uint, 0, len(comandas))
	userIDs := make([]uint, 0, len(comandas))
	seenS, seenO, seenU := map[uint]bool{}, map[uint]bool{}, map[uint]bool{}
	for _, c := range comandas {
		if !seenS[c.SessionID] {
			seenS[c.SessionID] = true
			sessionIDs = append(sessionIDs, c.SessionID)
		}
		if !seenO[c.OrderID] {
			seenO[c.OrderID] = true
			orderIDs = append(orderIDs, c.OrderID)
		}
		if c.CancelledByID != nil && !seenU[*c.CancelledByID] {
			seenU[*c.CancelledByID] = true
			userIDs = append(userIDs, *c.CancelledByID)
		}
	}

	meta := s.kitchenSessionMetaMap(sessionIDs)
	orderNum := map[uint]int{}
	var orders []database.TenantTableOrder
	if s.db.Where("id IN ?", orderIDs).Find(&orders).Error == nil {
		for _, o := range orders {
			orderNum[o.ID] = o.OrderNumber
		}
	}
	userName := map[uint]string{}
	if len(userIDs) > 0 {
		var users []database.TenantUser
		if s.db.Select("id", "name").Where("id IN ?", userIDs).Find(&users).Error == nil {
			for _, u := range users {
				userName[u.ID] = u.Name
			}
		}
	}

	out := make([]CancelledComandaView, 0, len(comandas))
	for _, c := range comandas {
		v := CancelledComandaView{
			TenantComanda:      c,
			OrderNumber:        orderNum[c.OrderID],
			KitchenSessionMeta: meta[c.SessionID],
		}
		if c.CancelledByID != nil {
			v.CancelledByName = userName[*c.CancelledByID]
		}
		out = append(out, v)
	}
	return out, total, nil
}
