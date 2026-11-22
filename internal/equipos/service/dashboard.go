package service

import (
	"fmt"
	"sort"
	"time"

	"tukifac/pkg/database"
)

// transitFollowUpDays días en tránsito a partir de los cuales conviene dar seguimiento al paquete.
const transitFollowUpDays = 5

type DashboardStock struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Current int    `json:"current"`
}

type Dashboard struct {
	Period            string           `json:"period"`
	Orders            int              `json:"orders"`
	SalesTotal        float64          `json:"sales_total"`
	Collected         float64          `json:"collected"`  // cobros vigentes del período
	Receivable        float64          `json:"receivable"` // saldo por cobrar de todos los pedidos
	PendingValidation int              `json:"pending_validation"`
	Observed          int              `json:"observed"`
	ReadyToDispatch   int              `json:"ready_to_dispatch"`
	WaitingDispatch   int              `json:"waiting_dispatch"`
	InTransit         int              `json:"in_transit"`
	InTransitFollowUp int              `json:"in_transit_follow_up"`
	InAgency          int              `json:"in_agency"`
	AgencyYellow      int              `json:"agency_yellow"`
	AgencyRed         int              `json:"agency_red"`
	AgencyExpired     int              `json:"agency_expired"`
	PickedUpWithDebt  int              `json:"picked_up_with_debt"`
	OpenReturns       int              `json:"open_returns"`
	ReturnCost        float64          `json:"return_cost"`
	ReturnUnpaid      float64          `json:"return_unpaid"`
	StockRed          []DashboardStock `json:"stock_red"`
	StockVisible      bool             `json:"stock_visible"`
	NegativeWatch     []DashboardStock `json:"negative_stock"`
}

// Dashboard resumen operativo del período (por defecto el mes en curso).
func (s *Service) Dashboard(period string, withStock bool) (*Dashboard, error) {
	if period == "" {
		period = currentPeriod()
	}
	start, end, err := periodBounds(period)
	if err != nil {
		return nil, err
	}
	d := &Dashboard{Period: period, StockVisible: withStock, StockRed: []DashboardStock{}, NegativeWatch: []DashboardStock{}}

	var sales struct {
		N     int
		Total float64
	}
	if err := s.db.Model(&database.EquipOrder{}).Select("COUNT(*) AS n, COALESCE(SUM(total_amount),0) AS total").
		Where("status = ? AND order_date >= ? AND order_date < ?", "registrado", start, end).Scan(&sales).Error; err != nil {
		return nil, err
	}
	d.Orders, d.SalesTotal = sales.N, round2(sales.Total)

	var coll struct{ Total float64 }
	if err := s.db.Model(&database.EquipPayment{}).Select("COALESCE(SUM(amount),0) AS total").
		Where("status = ? AND paid_at >= ? AND paid_at < ?", "vigente", start, end).Scan(&coll).Error; err != nil {
		return nil, err
	}
	d.Collected = round2(coll.Total)

	var rec struct{ Total float64 }
	if err := s.db.Model(&database.EquipOrder{}).Select("COALESCE(SUM(balance_amount),0) AS total").
		Where("status = ? AND payment_status <> ?", "registrado", "no_pago").Scan(&rec).Error; err != nil {
		return nil, err
	}
	d.Receivable = round2(rec.Total)

	count := func(dst *int, where string, args ...any) error {
		var n int64
		if err := s.db.Model(&database.EquipOrder{}).Where("status = ?", "registrado").Where(where, args...).Count(&n).Error; err != nil {
			return err
		}
		*dst = int(n)
		return nil
	}
	if err := count(&d.PendingValidation, "validation_status = ?", "pendiente_validacion"); err != nil {
		return nil, err
	}
	if err := count(&d.Observed, "validation_status = ?", "observado"); err != nil {
		return nil, err
	}

	rows, err := s.ListShipments(ShipmentFilter{})
	if err != nil {
		return nil, err
	}
	now := todayNoon()
	for _, r := range rows {
		switch r.Status {
		case "pendiente_envio":
			if r.ReadyToDispatch {
				d.ReadyToDispatch++
			} else {
				d.WaitingDispatch++
			}
		case "en_transito":
			d.InTransit++
			if r.DispatchedAt != nil && int(now.Sub(noonLima(*r.DispatchedAt)).Hours()/24) >= transitFollowUpDays {
				d.InTransitFollowUp++
			}
		case "en_agencia":
			d.InAgency++
			// El plazo de retorno es único (AgencyReturnDays); el del transportista solo gobierna el recojo.
			if agencyExpired(r.DaysSinceArrival) {
				d.AgencyExpired++
			} else {
				switch r.Alert {
				case "amarillo":
					d.AgencyYellow++
				case "rojo", "vencido":
					d.AgencyRed++
				}
			}
		}
	}
	var debt int64
	if err := s.db.Table("equip_orders o").Joins("JOIN equip_shipments sh ON sh.order_id = o.id AND sh.is_current = ?", true).
		Where("o.status = ? AND sh.status = ? AND o.balance_amount > 0 AND o.payment_status <> ?", "registrado", "entregado", "no_pago").Count(&debt).Error; err != nil {
		return nil, err
	}
	d.PickedUpWithDebt = int(debt)

	var open int64
	if err := s.db.Model(&database.EquipReturn{}).Where("status IN ?", []string{"solicitado", "en_camino"}).Count(&open).Error; err != nil {
		return nil, err
	}
	d.OpenReturns = int(open)
	var rc struct{ Cost, Unpaid float64 }
	if err := s.db.Model(&database.EquipReturn{}).Select("COALESCE(SUM(return_cost),0) AS cost, COALESCE(SUM(unpaid_balance),0) AS unpaid").
		Where("requested_at >= ? AND requested_at < ?", start, end).Scan(&rc).Error; err != nil {
		return nil, err
	}
	d.ReturnCost, d.ReturnUnpaid = round2(rc.Cost), round2(rc.Unpaid)

	if withStock {
		stock, err := s.StockReport(period)
		if err != nil {
			return nil, err
		}
		for _, r := range stock {
			it := DashboardStock{Code: r.Code, Name: r.Name, Current: r.Current}
			if r.Semaphore == "bajo" {
				d.StockRed = append(d.StockRed, it)
			}
			if r.Current < 0 {
				d.NegativeWatch = append(d.NegativeWatch, it)
			}
		}
	}
	return d, nil
}

// AlertItem aviso de seguimiento sobre un envío o pedido.
type AlertItem struct {
	Kind        string  `json:"kind"`     // agencia_vencido | agencia_rojo | agencia_amarillo | transito_seguimiento | recogido_con_saldo | observado
	Severity    string  `json:"severity"` // alta | media | baja
	OrderID     uint    `json:"order_id"`
	OrderNumber int     `json:"order_number"`
	Customer    string  `json:"customer_name"`
	Phone       string  `json:"customer_phone"`
	Carrier     string  `json:"carrier_name"`
	Guide       string  `json:"guide_number"`
	Days        int     `json:"days"`
	DaysLeft    *int    `json:"days_left"`
	Balance     float64 `json:"balance_amount"`
	Message     string  `json:"message"`
}

// Alerts reúne lo que requiere acción hoy, de lo más urgente a lo menos.
func (s *Service) Alerts() ([]AlertItem, error) {
	rows, err := s.ListShipments(ShipmentFilter{})
	if err != nil {
		return nil, err
	}
	now := todayNoon()
	out := []AlertItem{}
	add := func(r ShipmentRow, kind, sev, msg string, days int) {
		out = append(out, AlertItem{Kind: kind, Severity: sev, OrderID: r.OrderID, OrderNumber: r.OrderNumber, Customer: r.CustomerName,
			Phone: r.CustomerPhone, Carrier: r.CarrierName, Guide: r.GuideNumber, Days: days, DaysLeft: r.DaysLeft, Balance: r.BalanceAmount, Message: msg})
	}
	for _, r := range rows {
		switch r.Status {
		case "en_agencia":
			since := 0
			if r.DaysSinceArrival != nil {
				since = *r.DaysSinceArrival
			}
			if agencyExpired(r.DaysSinceArrival) {
				add(r, "agencia_vencido", "alta", fmt.Sprintf("%d días en agencia: corresponde el retorno", since), since)
				break
			}
			switch r.Alert {
			case "rojo", "vencido":
				add(r, "agencia_rojo", "alta", fmt.Sprintf("%d días en agencia: recordar al cliente que recoja", since), since)
			case "amarillo":
				add(r, "agencia_amarillo", "media", fmt.Sprintf("%d días en agencia", since), since)
			}
		case "en_transito":
			if r.DispatchedAt != nil {
				days := int(now.Sub(noonLima(*r.DispatchedAt)).Hours() / 24)
				if days >= transitFollowUpDays {
					add(r, "transito_seguimiento", "media", fmt.Sprintf("%d días en tránsito sin llegar: dar seguimiento a la guía", days), days)
				}
			}
		}
	}
	var debt []ShipmentRow
	_ = debt
	var obs []struct {
		ID                          uint
		OrderNumber                 int
		CustomerName, CustomerPhone string
	}
	if err := s.db.Model(&database.EquipOrder{}).Select("id, order_number, customer_name, customer_phone").
		Where("status = ? AND validation_status = ?", "registrado", "observado").Scan(&obs).Error; err != nil {
		return nil, err
	}
	for _, o := range obs {
		out = append(out, AlertItem{Kind: "observado", Severity: "media", OrderID: o.ID, OrderNumber: o.OrderNumber, Customer: o.CustomerName, Phone: o.CustomerPhone, Message: "Pedido observado: corregir y volver a validar"})
	}
	var picked []struct {
		ID                          uint
		OrderNumber                 int
		CustomerName, CustomerPhone string
		Balance                     float64
		PickedUpAt                  *time.Time
	}
	if err := s.db.Table("equip_orders o").
		Select("o.id, o.order_number, o.customer_name, o.customer_phone, o.balance_amount AS balance, sh.picked_up_at").
		Joins("JOIN equip_shipments sh ON sh.order_id = o.id AND sh.is_current = ?", true).
		Where("o.status = ? AND sh.status = ? AND o.balance_amount > 0 AND o.payment_status <> ?", "registrado", "entregado", "no_pago").Scan(&picked).Error; err != nil {
		return nil, err
	}
	for _, p := range picked {
		days := 0
		if p.PickedUpAt != nil {
			days = int(now.Sub(noonLima(*p.PickedUpAt)).Hours() / 24)
		}
		out = append(out, AlertItem{Kind: "recogido_con_saldo", Severity: "alta", OrderID: p.ID, OrderNumber: p.OrderNumber, Customer: p.CustomerName, Phone: p.CustomerPhone, Days: days, Balance: p.Balance, Message: "Recogido con saldo pendiente: cobrar"})
	}
	rank := map[string]int{"alta": 0, "media": 1, "baja": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		return out[i].Days > out[j].Days
	})
	return out, nil
}
