// Package service: órdenes de compra al proveedor. No mueven stock, caja ni cuentas por pagar;
// la compra real se registra aparte (1 OC → 1 compra) y marca la orden como "received".
package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/money"
	"tukifac/pkg/salecurrency"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

const (
	StatusDraft     = "draft"
	StatusSent      = "sent"
	StatusReceived  = "received"
	StatusCancelled = "cancelled"

	SeriesCategory = "orden_compra"
)

var (
	ErrNotFound       = errors.New("orden de compra no encontrada")
	ErrNotEditable    = errors.New("la orden ya fue recibida o anulada y no se puede modificar")
	ErrAlreadyClosed  = errors.New("la orden ya fue recibida o anulada")
	ErrSupplierNeeded = errors.New("el proveedor es obligatorio")
)

type PurchaseOrderService struct{ db *gorm.DB }

func NewPurchaseOrderService(db *gorm.DB) *PurchaseOrderService {
	return &PurchaseOrderService{db: db}
}

type ItemInput struct {
	ProductID          *uint   `json:"product_id"`
	PresentationID     *uint   `json:"presentation_id"`
	SaleUnitID         *uint   `json:"sale_unit_id"`
	Code               string  `json:"code"`
	Description        string  `json:"description"`
	Unit               string  `json:"unit"`
	Quantity           float64 `json:"quantity"`
	UnitCost           float64 `json:"unit_cost"`
	IgvAffectationType string  `json:"igv_affectation_type"`
	PriceIncludesIgv   bool    `json:"price_includes_igv"`
}

type SaveInput struct {
	BranchID         uint
	UserID           uint
	ContactID        *uint
	SeriesID         uint
	IssueDate        time.Time
	ExpectedDate     *time.Time
	Currency         string
	ExchangeRate     *float64
	PaymentCondition string
	Notes            string
	Items            []ItemInput
	TaxConfig        tax.Config
}

type ListParams struct {
	BranchID uint
	Query    string
	Status   string
	From     *time.Time
	To       *time.Time
	Limit    int
	Offset   int
}

func (s *PurchaseOrderService) validateSeries(seriesID, branchID uint) (database.TenantDocumentSeries, error) {
	series, err := docseries.ValidateForBranch(s.db, seriesID, branchID)
	if err != nil {
		return series, err
	}
	if strings.ToLower(strings.TrimSpace(series.Category)) != SeriesCategory {
		return series, errors.New("la serie debe ser de categoría orden de compra")
	}
	return series, nil
}

// validateHeader valida proveedor, moneda y TC. USD exige TC > 0 (la OC conserva su moneda
// original; el equivalente en soles se calcula al mostrarla).
func (s *PurchaseOrderService) validateHeader(in SaveInput) (string, *float64, error) {
	if in.ContactID == nil || *in.ContactID == 0 {
		return "", nil, ErrSupplierNeeded
	}
	var n int64
	if err := s.db.Model(&database.TenantContact{}).Where("id = ?", *in.ContactID).Count(&n).Error; err != nil {
		return "", nil, err
	}
	if n == 0 {
		return "", nil, errors.New("el proveedor seleccionado no existe")
	}
	cur, rate, _, err := salecurrency.ResolveDocumentRate(in.Currency, in.ExchangeRate, in.IssueDate)
	if err != nil {
		return "", nil, err
	}
	if cur == salecurrency.CurrencyUSD && rate == nil {
		return "", nil, errors.New("el tipo de cambio es obligatorio para órdenes en dólares (USD)")
	}
	if in.ExpectedDate != nil && in.ExpectedDate.Before(time.Date(in.IssueDate.Year(), in.IssueDate.Month(), in.IssueDate.Day(), 0, 0, 0, 0, in.IssueDate.Location())) {
		return "", nil, errors.New("la fecha de entrega no puede ser anterior a la fecha de emisión")
	}
	return cur, rate, nil
}

type calc struct {
	items                   []database.TenantPurchaseOrderItem
	subtotal, taxAmt, total float64
	anyIncludesIgv, anyExcl bool
}

func (s *PurchaseOrderService) build(items []ItemInput, cfg tax.Config) (*calc, error) {
	if len(items) == 0 {
		return nil, errors.New("la orden debe tener al menos un ítem")
	}
	if cfg.TaxRate == 0 {
		cfg = tax.LoadFromDB(s.db)
	}
	out := &calc{}
	for i, it := range items {
		label := strings.TrimSpace(it.Description)
		if label == "" {
			return nil, fmt.Errorf("ítem %d: la descripción es obligatoria", i+1)
		}
		if it.Quantity <= 0 {
			return nil, fmt.Errorf("ítem %q: la cantidad debe ser mayor a cero", label)
		}
		if it.UnitCost < 0 {
			return nil, fmt.Errorf("ítem %q: el costo no puede ser negativo", label)
		}
		if it.ProductID != nil && *it.ProductID > 0 {
			var n int64
			if err := s.db.Model(&database.TenantProduct{}).Where("id = ?", *it.ProductID).Count(&n).Error; err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, fmt.Errorf("el producto %q ya no existe o fue eliminado", label)
			}
		}
		aff := it.IgvAffectationType
		if aff == "" {
			aff = "10"
		}
		sub, tx, tot := tax.CalcItem(it.UnitCost, it.Quantity, 0, aff, it.PriceIncludesIgv, cfg)
		out.subtotal += sub
		out.taxAmt += tx
		out.total += tot
		if it.PriceIncludesIgv {
			out.anyIncludesIgv = true
		} else {
			out.anyExcl = true
		}
		out.items = append(out.items, database.TenantPurchaseOrderItem{
			ProductID:          it.ProductID,
			PresentationID:     it.PresentationID,
			SaleUnitID:         it.SaleUnitID,
			Code:               it.Code,
			Description:        label,
			Unit:               it.Unit,
			Quantity:           it.Quantity,
			UnitCost:           it.UnitCost,
			TaxRate:            cfg.EffectiveRate(aff),
			IgvAffectationType: aff,
			PriceIncludesIgv:   it.PriceIncludesIgv,
			Subtotal:           money.RoundSunat(sub),
			TaxAmount:          money.RoundSunat(tx),
			Total:              money.RoundSunat(tot),
		})
	}
	return out, nil
}

func (s *PurchaseOrderService) Create(in SaveInput) (*database.TenantPurchaseOrder, error) {
	if in.BranchID == 0 || in.UserID == 0 {
		return nil, errors.New("sucursal y usuario son requeridos")
	}
	if _, err := s.validateSeries(in.SeriesID, in.BranchID); err != nil {
		return nil, err
	}
	cur, rate, err := s.validateHeader(in)
	if err != nil {
		return nil, err
	}
	c, err := s.build(in.Items, in.TaxConfig)
	if err != nil {
		return nil, err
	}
	po := &database.TenantPurchaseOrder{
		BranchID:         in.BranchID,
		ContactID:        in.ContactID,
		UserID:           in.UserID,
		SeriesID:         in.SeriesID,
		IssueDate:        in.IssueDate,
		ExpectedDate:     in.ExpectedDate,
		Currency:         cur,
		ExchangeRate:     rate,
		PaymentCondition: strings.TrimSpace(in.PaymentCondition),
		Subtotal:         money.RoundSunat(c.subtotal),
		TaxAmount:        money.RoundSunat(c.taxAmt),
		Total:            money.RoundSunat(c.total),
		PriceIncludesIgv: c.anyIncludesIgv && !c.anyExcl,
		Notes:            in.Notes,
		Status:           StatusDraft,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		corr, ser, err := docseries.ReserveNext(tx, in.SeriesID)
		if err != nil {
			return err
		}
		po.Series = ser.Series
		po.Correlative = corr
		po.Number = fmt.Sprintf("%s-%08d", ser.Series, corr)
		if err := tx.Create(po).Error; err != nil {
			return err
		}
		for i := range c.items {
			c.items[i].PurchaseOrderID = po.ID
		}
		return tx.Create(&c.items).Error
	})
	if err != nil {
		return nil, err
	}
	po.ContactName = s.contactName(po.ContactID)
	return po, nil
}

// Update reemplaza cabecera editable e ítems. Solo mientras la orden esté en draft/sent.
// Serie y correlativo no cambian nunca.
func (s *PurchaseOrderService) Update(id uint, in SaveInput) (*database.TenantPurchaseOrder, error) {
	var po database.TenantPurchaseOrder
	if err := s.db.First(&po, id).Error; err != nil {
		return nil, ErrNotFound
	}
	if po.Status == StatusReceived || po.Status == StatusCancelled {
		return nil, ErrNotEditable
	}
	cur, rate, err := s.validateHeader(in)
	if err != nil {
		return nil, err
	}
	c, err := s.build(in.Items, in.TaxConfig)
	if err != nil {
		return nil, err
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"contact_id":         in.ContactID,
			"issue_date":         in.IssueDate,
			"expected_date":      in.ExpectedDate,
			"currency":           cur,
			"exchange_rate":      rate,
			"payment_condition":  strings.TrimSpace(in.PaymentCondition),
			"subtotal":           money.RoundSunat(c.subtotal),
			"tax_amount":         money.RoundSunat(c.taxAmt),
			"total":              money.RoundSunat(c.total),
			"price_includes_igv": c.anyIncludesIgv && !c.anyExcl,
			"notes":              in.Notes,
		}
		if err := tx.Model(&po).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("purchase_order_id = ?", id).Delete(&database.TenantPurchaseOrderItem{}).Error; err != nil {
			return err
		}
		for i := range c.items {
			c.items[i].PurchaseOrderID = id
		}
		return tx.Create(&c.items).Error
	})
	if err != nil {
		return nil, err
	}
	out, err := s.header(id)
	if err != nil {
		return nil, err
	}
	out.ContactName = s.contactName(out.ContactID)
	return out, nil
}

func (s *PurchaseOrderService) header(id uint) (*database.TenantPurchaseOrder, error) {
	var po database.TenantPurchaseOrder
	if err := s.db.First(&po, id).Error; err != nil {
		return nil, ErrNotFound
	}
	return &po, nil
}

// BranchOf devuelve la sucursal de la orden (para validar acceso antes de operar).
func (s *PurchaseOrderService) BranchOf(id uint) (uint, error) {
	var po database.TenantPurchaseOrder
	if err := s.db.Select("id", "branch_id").First(&po, id).Error; err != nil {
		return 0, ErrNotFound
	}
	return po.BranchID, nil
}

func (s *PurchaseOrderService) contactName(id *uint) string {
	if id == nil || *id == 0 {
		return ""
	}
	var c database.TenantContact
	if s.db.Select("business_name", "trade_name").First(&c, *id).Error != nil {
		return ""
	}
	if n := strings.TrimSpace(c.BusinessName); n != "" {
		return n
	}
	return strings.TrimSpace(c.TradeName)
}

func (s *PurchaseOrderService) GetByID(id uint) (*database.TenantPurchaseOrder, []database.TenantPurchaseOrderItem, error) {
	po, err := s.header(id)
	if err != nil {
		return nil, nil, err
	}
	po.ContactName = s.contactName(po.ContactID)
	var items []database.TenantPurchaseOrderItem
	if err := s.db.Where("purchase_order_id = ?", id).Order("id").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	return po, items, nil
}

func (s *PurchaseOrderService) List(p ListParams) ([]database.TenantPurchaseOrder, int64, error) {
	q := s.db.Model(&database.TenantPurchaseOrder{})
	if p.BranchID > 0 {
		q = q.Where("branch_id = ?", p.BranchID)
	}
	if p.Status != "" {
		q = q.Where("status = ?", p.Status)
	}
	if p.From != nil {
		q = q.Where("issue_date >= ?", *p.From)
	}
	if p.To != nil {
		q = q.Where("issue_date <= ?", *p.To)
	}
	if t := strings.TrimSpace(p.Query); t != "" {
		like := "%" + t + "%"
		q = q.Where("number LIKE ? OR notes LIKE ? OR contact_id IN (SELECT id FROM tenant_contacts WHERE business_name LIKE ? OR trade_name LIKE ? OR doc_number LIKE ?)", like, like, like, like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 25
	}
	var rows []database.TenantPurchaseOrder
	if err := q.Order("issue_date DESC, id DESC").Limit(limit).Offset(p.Offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	names := map[uint]string{}
	for i := range rows {
		if rows[i].ContactID == nil {
			continue
		}
		id := *rows[i].ContactID
		if n, ok := names[id]; ok {
			rows[i].ContactName = n
			continue
		}
		n := s.contactName(rows[i].ContactID)
		names[id] = n
		rows[i].ContactName = n
	}
	return rows, total, nil
}

// MarkSent: draft → sent (se entregó al proveedor). Idempotente sobre "sent".
func (s *PurchaseOrderService) MarkSent(id uint) error {
	po, err := s.header(id)
	if err != nil {
		return err
	}
	if po.Status == StatusReceived || po.Status == StatusCancelled {
		return ErrAlreadyClosed
	}
	return s.db.Model(po).Update("status", StatusSent).Error
}

// Cancel anula la orden. No se puede anular una ya recibida (tiene compra asociada).
func (s *PurchaseOrderService) Cancel(id uint) error {
	po, err := s.header(id)
	if err != nil {
		return err
	}
	if po.Status == StatusReceived {
		return errors.New("la orden ya tiene una compra registrada; anule primero la compra")
	}
	if po.Status == StatusCancelled {
		return nil
	}
	return s.db.Model(po).Update("status", StatusCancelled).Error
}

// ConvertibleSummary ítems/cabecera para precargar la compra. Solo órdenes abiertas.
func (s *PurchaseOrderService) EnsureConvertible(id uint) (*database.TenantPurchaseOrder, error) {
	po, err := s.header(id)
	if err != nil {
		return nil, err
	}
	switch po.Status {
	case StatusReceived:
		return nil, errors.New("la orden ya fue convertida en una compra")
	case StatusCancelled:
		return nil, errors.New("la orden está anulada")
	}
	return po, nil
}

// MarkReceivedTx enlaza la orden con la compra dentro de la transacción de la compra.
// Falla si la orden ya fue recibida (evita dos compras sobre la misma orden, incluso con
// peticiones concurrentes: el UPDATE condicional solo afecta a una).
func MarkReceivedTx(tx *gorm.DB, orderID, purchaseID uint, purchaseCurrency string) error {
	// La compra debe estar en la misma moneda que la orden: convertir una orden en USD con una compra
	// en soles (o al revés) cambiaría el importe pactado con el proveedor. El TC sí puede diferir
	// (el de la orden es el de su fecha; el de la compra, el de la fecha del comprobante).
	var cur string
	if err := tx.Model(&database.TenantPurchaseOrder{}).Where("id = ?", orderID).Select("currency").Scan(&cur).Error; err != nil {
		return err
	}
	if cur == "" {
		cur = salecurrency.CurrencyPEN
	}
	if pc := strings.ToUpper(strings.TrimSpace(purchaseCurrency)); pc == "" && cur != salecurrency.CurrencyPEN || pc != "" && pc != cur {
		return fmt.Errorf("la compra debe registrarse en la moneda de la orden (%s)", cur)
	}
	now := time.Now()
	res := tx.Model(&database.TenantPurchaseOrder{}).
		Where("id = ? AND status IN ?", orderID, []string{StatusDraft, StatusSent}).
		Updates(map[string]interface{}{"status": StatusReceived, "purchase_id": purchaseID, "received_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("la orden de compra no está disponible (ya recibida, anulada o inexistente)")
	}
	return nil
}

// ReleaseOnPurchaseVoidTx devuelve a "sent" la orden cuando se anula la compra que la recibió,
// para poder volver a convertirla.
func ReleaseOnPurchaseVoidTx(tx *gorm.DB, purchaseID uint) error {
	return tx.Model(&database.TenantPurchaseOrder{}).
		Where("purchase_id = ? AND status = ?", purchaseID, StatusReceived).
		Updates(map[string]interface{}{"status": StatusSent, "purchase_id": nil, "received_at": nil}).Error
}
