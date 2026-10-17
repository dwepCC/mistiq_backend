package service

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// Etapas del seguimiento de envíos (vista del negocio). Se DERIVAN del estado del pedido y del
// despacho; no son un tercer estado almacenado.
const (
	StagePorConfirmar = "POR_CONFIRMAR"
	StagePorPreparar  = "POR_PREPARAR"
	StageListo        = "LISTO"
	StageEnCamino     = "EN_CAMINO"
	StageEnAgencia    = "EN_AGENCIA"
	StageEntregado    = "ENTREGADO"
	StageRetorno      = "RETORNO"
	StageCancelado    = "CANCELADO"
)

// stageSQL expresión SQL de la etapa. Alias: o = pedido, d = despacho.
const stageSQL = `CASE
	WHEN o.status = 'PENDIENTE' THEN 'POR_CONFIRMAR'
	WHEN o.status IN ('CONFIRMADO', 'EN_PREPARACION', 'EMPAQUETADO') THEN 'POR_PREPARAR'
	WHEN o.status = 'LISTO_PARA_DESPACHO' THEN 'LISTO'
	WHEN o.status = 'DESPACHADO' AND d.status = 'EN_AGENCIA' THEN 'EN_AGENCIA'
	WHEN o.status = 'DESPACHADO' THEN 'EN_CAMINO'
	WHEN o.status = 'ENTREGADO' THEN 'ENTREGADO'
	WHEN o.status = 'DEVUELTO' THEN 'RETORNO'
	ELSE 'CANCELADO' END`

var validStages = map[string]bool{
	StagePorConfirmar: true, StagePorPreparar: true, StageListo: true, StageEnCamino: true,
	StageEnAgencia: true, StageEntregado: true, StageRetorno: true, StageCancelado: true,
}

// ShippingBoardParams filtros del tablero y de la lista de envíos; todos opcionales y combinables.
type ShippingBoardParams struct {
	Stage      string
	CarrierID  uint
	BranchID   uint
	Query      string
	Department string // 2 dígitos del ubigeo (15 = Lima, 08 = Cusco…)
	DateFrom   *time.Time
	DateTo     *time.Time
	// OnlyConverted / OnlyUnconverted: solo pedidos ya convertidos en venta, o solo los que no.
	Limit  int
	Offset int
}

// ShippingRow una fila del tablero/lista: pedido + despacho + etapa derivada.
type ShippingRow struct {
	ID                 uint       `json:"id"`
	OrderNumber        string     `json:"order_number"`
	CreatedAt          time.Time  `json:"created_at"`
	CustomerName       string     `json:"customer_name"`
	CustomerPhone      string     `json:"customer_phone"`
	Status             string     `json:"status"`
	DeliveryMethod     string     `json:"delivery_method"`
	Total              float64    `json:"total"`
	ShippingCost       float64    `json:"shipping_cost"`
	BranchID           *uint      `json:"branch_id"`
	ConvertedSaleID    *uint      `json:"converted_sale_id"`
	Source             string     `json:"source"`
	ItemCount          int        `json:"item_count"`
	PackageCount       *int       `json:"package_count"`
	WeightKg           *float64   `json:"weight_kg"`
	CarrierID          *uint      `json:"carrier_id"`
	CarrierName        string     `json:"carrier_name"`
	DestinationUbigeo  string     `json:"destination_ubigeo"`
	Department         string     `json:"department"`
	DestinationAgency  string     `json:"destination_agency"`
	RecipientName      string     `json:"recipient_name"`
	RecipientDocType   string     `json:"recipient_doc_type"`
	RecipientDocNumber string     `json:"recipient_doc_number"`
	RecipientPhone     string     `json:"recipient_phone"`
	DispatchID         *uint      `json:"dispatch_id"`
	DispatchStatus     string     `json:"dispatch_status"`
	TrackingCode       string     `json:"tracking_code"`
	DispatchedAt       *time.Time `json:"dispatched_at"`
	AgencyArrivedAt    *time.Time `json:"agency_arrived_at"`
	PickupDeadline     *time.Time `json:"pickup_deadline"`
	// PickupDaysLeft días que le quedan al cliente para recoger (negativo = vencido). Solo en etapa EN_AGENCIA.
	PickupDaysLeft *int `json:"pickup_days_left"`
	// ReturnedAt / ReturnReason: cuándo regresó el paquete y el motivo (etapa RETORNO).
	ReturnedAt   *time.Time `json:"returned_at"`
	ReturnReason string     `json:"return_reason"`
	// Pagos: abonos validados, saldo y cuántos abonos esperan revisión.
	PaidTotal        float64 `json:"paid_total"`
	Balance          float64 `json:"balance"`
	PaymentsToReview int     `json:"payments_to_review"`
	PaymentStatus    string  `json:"payment_status"`
	Stage            string  `json:"stage"`
}

type shippingRowDB struct {
	Source             string
	ID                 uint
	CreatedAt          time.Time
	CustomerName       string
	CustomerPhone      string
	Status             string
	DeliveryMethod     string
	Total              float64
	ShippingCost       float64
	BranchID           *uint
	ConvertedSaleID    *uint
	ItemCount          int
	PackageCount       *int
	WeightKg           *float64
	CarrierID          *uint
	CarrierName        string
	DestinationUbigeo  string
	DestinationAgency  string
	RecipientName      string
	RecipientDocType   string
	RecipientDocNumber string
	RecipientPhone     string
	DispatchID         *uint
	DispatchStatus     string
	TrackingCode       string
	DispatchedAt       *time.Time
	AgencyArrivedAt    *time.Time
	PickupDeadline     *time.Time
	ReturnedAt         *time.Time
	ReturnReason       string
	PaidTotal          float64
	PaymentsToReview   int
	PaymentStatus      string
	Stage              string
}

const shippingSelect = `SELECT o.id AS id, o.created_at AS created_at, o.customer_name AS customer_name,
	o.customer_phone AS customer_phone, o.status AS status, o.delivery_method AS delivery_method,
	o.total AS total, o.shipping_cost AS shipping_cost, o.branch_id AS branch_id,
	o.converted_sale_id AS converted_sale_id, o.source AS source,
	(SELECT COUNT(*) FROM tenant_ecommerce_order_items i WHERE i.order_id = o.id) AS item_count,
	COALESCE(d.package_count, o.package_count) AS package_count,
	COALESCE(d.weight_kg, o.weight_kg) AS weight_kg,
	COALESCE(d.carrier_id, o.carrier_id) AS carrier_id,
	COALESCE(NULLIF(d.carrier_name, ''), c.name, '') AS carrier_name,
	o.destination_ubigeo AS destination_ubigeo, o.destination_agency AS destination_agency,
	o.recipient_name AS recipient_name, o.recipient_doc_type AS recipient_doc_type,
	o.recipient_doc_number AS recipient_doc_number, o.recipient_phone AS recipient_phone,
	d.id AS dispatch_id, COALESCE(d.status, '') AS dispatch_status, COALESCE(d.tracking_code, '') AS tracking_code,
	d.dispatched_at AS dispatched_at, d.agency_arrived_at AS agency_arrived_at, d.pickup_deadline AS pickup_deadline,
	d.returned_at AS returned_at,
	COALESCE((SELECT SUM(p.amount) FROM tenant_ecommerce_order_payments p
		WHERE p.order_id = o.id AND p.status = 'VALIDADO' AND p.deleted_at IS NULL), 0) AS paid_total,
	COALESCE((SELECT COUNT(*) FROM tenant_ecommerce_order_payments p
		WHERE p.order_id = o.id AND p.status <> 'VALIDADO' AND p.deleted_at IS NULL), 0) AS payments_to_review,
	o.payment_status AS payment_status,
	COALESCE((SELECT h.notes FROM tenant_ecommerce_dispatch_status_histories h
		WHERE h.dispatch_id = d.id AND h.to_status = 'DEVUELTO' ORDER BY h.id DESC LIMIT 1), '') AS return_reason,
	` + stageSQL + ` AS stage
	FROM tenant_ecommerce_orders o
	LEFT JOIN tenant_ecommerce_dispatches d ON d.order_id = o.id
	LEFT JOIN tenant_carriers c ON c.id = COALESCE(d.carrier_id, o.carrier_id)`

// boardWhere arma el WHERE común (sin la etapa) y sus argumentos.
func boardWhere(p ShippingBoardParams) (string, []interface{}) {
	var conds []string
	var args []interface{}
	if p.CarrierID > 0 {
		conds = append(conds, "COALESCE(d.carrier_id, o.carrier_id) = ?")
		args = append(args, p.CarrierID)
	}
	if p.BranchID > 0 {
		conds = append(conds, "o.branch_id = ?")
		args = append(args, p.BranchID)
	}
	if dep := strings.TrimSpace(p.Department); len(dep) == 2 {
		conds = append(conds, "o.destination_ubigeo LIKE ?")
		args = append(args, dep+"%")
	}
	if p.DateFrom != nil {
		conds = append(conds, "o.created_at >= ?")
		args = append(args, *p.DateFrom)
	}
	if p.DateTo != nil {
		conds = append(conds, "o.created_at <= ?")
		args = append(args, *p.DateTo)
	}
	if q := strings.TrimSpace(p.Query); q != "" {
		term := "%" + q + "%"
		parts := []string{
			"o.customer_name LIKE ?", "o.customer_phone LIKE ?", "o.recipient_name LIKE ?",
			"o.recipient_doc_number LIKE ?", "d.tracking_code LIKE ?", "o.destination_agency LIKE ?",
		}
		qargs := []interface{}{term, term, term, term, term, term}
		num := strings.TrimPrefix(strings.ToUpper(q), "PW-")
		if id, err := strconv.ParseUint(num, 10, 32); err == nil {
			parts = append(parts, "o.id = ?")
			qargs = append(qargs, id)
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
		args = append(args, qargs...)
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func orderNumber(id uint) string { return "PW-" + strconv.FormatUint(uint64(id), 10) }

// ListShippingBoard filas del tablero/lista de envíos + conteo por etapa (respetando los demás filtros).
func (s *EcommerceService) ListShippingBoard(p ShippingBoardParams) ([]ShippingRow, int64, map[string]int64, error) {
	stage := strings.ToUpper(strings.TrimSpace(p.Stage))
	if stage != "" && stage != "ALL" && !validStages[stage] {
		return nil, 0, nil, fmt.Errorf("etapa inválida: %s", p.Stage)
	}
	where, args := boardWhere(p)
	base := shippingSelect + where

	// Conteo por etapa (sin filtrar por etapa).
	type stageCount struct {
		Stage string
		N     int64
	}
	var counts []stageCount
	if err := s.db.Raw("SELECT t.stage AS stage, COUNT(*) AS n FROM ("+base+") t GROUP BY t.stage", args...).Scan(&counts).Error; err != nil {
		return nil, 0, nil, err
	}
	summary := map[string]int64{}
	for st := range validStages {
		summary[st] = 0
	}
	for _, c := range counts {
		summary[c.Stage] = c.N
	}

	// Filas de la etapa pedida.
	listSQL := "SELECT * FROM (" + base + ") t"
	listArgs := append([]interface{}{}, args...)
	var total int64
	if stage != "" && stage != "ALL" {
		listSQL += " WHERE t.stage = ?"
		listArgs = append(listArgs, stage)
		total = summary[stage]
	} else {
		for _, n := range summary {
			total += n
		}
	}
	if stage == StageEnAgencia {
		listSQL += " ORDER BY t.pickup_deadline ASC, t.id ASC"
	} else {
		listSQL += " ORDER BY t.created_at DESC, t.id DESC"
	}
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	listSQL += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, maxInt(p.Offset, 0))

	var dbRows []shippingRowDB
	if err := s.db.Raw(listSQL, listArgs...).Scan(&dbRows).Error; err != nil {
		return nil, 0, nil, err
	}

	depNames := s.departmentNames(dbRows)
	now := time.Now()
	rows := make([]ShippingRow, len(dbRows))
	for i, r := range dbRows {
		row := ShippingRow{
			ID: r.ID, OrderNumber: orderNumber(r.ID), CreatedAt: r.CreatedAt,
			CustomerName: r.CustomerName, CustomerPhone: r.CustomerPhone, Status: r.Status,
			DeliveryMethod: r.DeliveryMethod, Total: r.Total, ShippingCost: r.ShippingCost,
			BranchID: r.BranchID, ConvertedSaleID: r.ConvertedSaleID, Source: r.Source, ItemCount: r.ItemCount,
			PackageCount: r.PackageCount, WeightKg: r.WeightKg, CarrierID: r.CarrierID,
			CarrierName: r.CarrierName, DestinationUbigeo: r.DestinationUbigeo,
			DestinationAgency: r.DestinationAgency, RecipientName: r.RecipientName,
			RecipientDocType: r.RecipientDocType, RecipientDocNumber: r.RecipientDocNumber,
			RecipientPhone: r.RecipientPhone, DispatchID: r.DispatchID, DispatchStatus: r.DispatchStatus,
			TrackingCode: r.TrackingCode, DispatchedAt: r.DispatchedAt,
			AgencyArrivedAt: r.AgencyArrivedAt, PickupDeadline: r.PickupDeadline, Stage: r.Stage,
			ReturnedAt: r.ReturnedAt, ReturnReason: r.ReturnReason,
			PaidTotal: r.PaidTotal, PaymentsToReview: r.PaymentsToReview, PaymentStatus: r.PaymentStatus,
			Balance: balanceOf(r.Total, r.PaidTotal),
		}
		if len(r.DestinationUbigeo) == 6 {
			row.Department = depNames[r.DestinationUbigeo[:2]]
		}
		if r.Stage == StageEnAgencia && r.PickupDeadline != nil {
			left := int(math.Ceil(r.PickupDeadline.Sub(now).Hours() / 24))
			row.PickupDaysLeft = &left
		}
		rows[i] = row
	}
	return rows, total, summary, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// departmentNames nombre de departamento por prefijo de ubigeo (2 dígitos), leído de ubi_regiones.
func (s *EcommerceService) departmentNames(rows []shippingRowDB) map[string]string {
	out := map[string]string{}
	var ids []string
	seen := map[string]bool{}
	for _, r := range rows {
		if len(r.DestinationUbigeo) == 6 {
			p := r.DestinationUbigeo[:2]
			if !seen[p] {
				seen[p] = true
				ids = append(ids, p+"0000")
			}
		}
	}
	if len(ids) == 0 {
		return out
	}
	var regions []database.UbiRegion
	if err := s.db.Where("id IN ?", ids).Find(&regions).Error; err != nil {
		return out
	}
	for _, r := range regions {
		if len(r.ID) >= 2 {
			out[r.ID[:2]] = r.Nombre
		}
	}
	return out
}

// UbigeoNames departamento, provincia y distrito de un ubigeo de 6 dígitos ("" si no existe).
func (s *EcommerceService) UbigeoNames(ubigeo string) (department, province, district string) {
	if len(ubigeo) != 6 {
		return "", "", ""
	}
	var r database.UbiRegion
	if s.db.Where("id = ?", ubigeo[:2]+"0000").First(&r).Error == nil {
		department = r.Nombre
	}
	var p database.UbiProvincia
	if s.db.Where("id = ?", ubigeo[:4]+"00").First(&p).Error == nil {
		province = p.Nombre
	}
	var d database.UbiDistrito
	if s.db.Where("id = ?", ubigeo).First(&d).Error == nil {
		district = d.Nombre
	}
	return department, province, district
}

// balanceOf lo que falta por pagar de un pedido (nunca negativo).
func balanceOf(total, paid float64) float64 {
	b := math.Round((total-paid)*100) / 100
	if b < 0 {
		return 0
	}
	return b
}
