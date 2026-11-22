package service

import (
	"math"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

var validMethods = map[string]bool{"yape": true, "plin": true, "efectivo": true, "transferencia": true, "deposito": true, "otro": true}

type PaymentAllocationInput struct {
	OrderID uint    `json:"order_id"`
	Amount  float64 `json:"amount"`
}

// PaymentInput un cobro: dinero que entra a nombre de un cliente, aplicado a uno o varios pedidos.
type PaymentInput struct {
	CustomerID  *uint                    `json:"customer_id"`
	Amount      float64                  `json:"amount"`
	PaidAt      string                   `json:"paid_at"` // AAAA-MM-DD, AAAA-MM-DDTHH:MM o RFC 3339; vacío = ahora
	Method      string                   `json:"method"`
	Moment      string                   `json:"moment"` // anticipado | al_recoger
	Reference   string                   `json:"reference"`
	Invoice     string                   `json:"invoice"` // «F002-59» (opcional)
	Notes       string                   `json:"notes"`
	Allocations []PaymentAllocationInput `json:"allocations"`
	// AutoAllocate: sin asignaciones explícitas, aplica el cobro a los pedidos con saldo del cliente (el más antiguo primero).
	AutoAllocate bool `json:"auto_allocate"`
}

func parsePaidAt(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now(), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, raw, limaLoc()); err == nil {
			return t, nil
		}
	}
	if d, err := time.ParseInLocation("2006-01-02", raw, limaLoc()); err == nil {
		return noonLima(d), nil
	}
	return time.Time{}, invalid("fecha del cobro inválida")
}

// PaymentView cobro con sus aplicaciones.
type PaymentView struct {
	database.EquipPayment
	CustomerName string           `json:"customer_name"`
	Invoice      string           `json:"invoice"`
	Allocations  []AllocationView `json:"allocations"`
}

type AllocationView struct {
	OrderID     uint    `json:"order_id"`
	OrderNumber int     `json:"order_number"`
	Amount      float64 `json:"amount"`
}

func (s *Service) CreatePayment(in PaymentInput, userID uint) (*PaymentView, error) {
	var id uint
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if in.Amount <= 0 {
			return invalid("el monto del cobro debe ser mayor a cero")
		}
		amount := round2(in.Amount)
		method := strings.TrimSpace(in.Method)
		if method == "" {
			method = "otro"
		}
		if !validMethods[method] {
			return invalid("método de pago inválido")
		}
		moment := strings.TrimSpace(in.Moment)
		if moment == "" {
			moment = "anticipado"
		}
		if moment != "anticipado" && moment != "al_recoger" {
			return invalid("momento del cobro inválido (anticipado o al_recoger)")
		}
		paidAt, err := parsePaidAt(in.PaidAt)
		if err != nil {
			return err
		}
		if paidAt.After(time.Now().Add(time.Hour)) {
			return invalid("la fecha del cobro no puede ser futura")
		}
		docType, series, number, ok := parseInvoice(in.Invoice)
		if !ok {
			return invalid("el comprobante debe tener el formato SERIE-NÚMERO (ej. F002-59)")
		}
		if series != "" {
			var dup database.EquipPayment
			if found, err := findOne(tx, &dup, "invoice_series = ? AND invoice_number = ? AND status = ?", series, number, "vigente"); err != nil {
				return err
			} else if found {
				return invalid("el comprobante %s-%s ya está registrado en otro cobro", series, number)
			}
		}

		var customerID *uint
		if in.CustomerID != nil {
			var c database.EquipCustomer
			if found, err := findOne(tx, &c, "id = ?", *in.CustomerID); err != nil {
				return err
			} else if !found {
				return invalid("el cliente no existe")
			}
			cid := c.ID
			customerID = &cid
		}

		allocs := in.Allocations
		if len(allocs) == 0 && in.AutoAllocate && customerID != nil {
			allocs, err = s.autoAllocateTx(tx, *customerID, amount)
			if err != nil {
				return err
			}
		}
		total := 0.0
		for _, a := range allocs {
			total += a.Amount
		}
		if round2(total) > amount+0.004 {
			return invalid("lo aplicado a pedidos (S/ %.2f) supera el monto del cobro (S/ %.2f)", total, amount)
		}
		customerID, err = s.validateAllocationsTx(tx, allocs, customerID, 0)
		if err != nil {
			return err
		}
		unallocated := round2(amount - total)
		if unallocated > 0.004 && customerID == nil {
			return invalid("sobran S/ %.2f del cobro y no hay un cliente al que dejarlos a favor: indica el cliente o aplícalo a un pedido", unallocated)
		}
		if unallocated < 0 {
			unallocated = 0
		}
		p := database.EquipPayment{
			CustomerID: customerID, Amount: amount, PaidAt: paidAt, Method: method, Moment: moment,
			Reference: strings.TrimSpace(in.Reference), InvoiceDocType: docType, InvoiceSeries: series, InvoiceNumber: number,
			UnallocatedAmount: unallocated, Status: "vigente", Notes: strings.TrimSpace(in.Notes),
		}
		if userID > 0 {
			u := userID
			p.CreatedBy = &u
		}
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		id = p.ID
		return s.applyAllocationsTx(tx, p.ID, allocs)
	})
	if err != nil {
		return nil, err
	}
	return s.GetPayment(id)
}

// autoAllocateTx reparte el monto entre los pedidos con saldo del cliente, del más antiguo al más nuevo.
func (s *Service) autoAllocateTx(tx *gorm.DB, customerID uint, amount float64) ([]PaymentAllocationInput, error) {
	var orders []database.EquipOrder
	if err := tx.Where("customer_id = ? AND status <> ? AND balance_amount > ?", customerID, "anulado", 0.004).
		Order("order_date ASC, order_number ASC").Find(&orders).Error; err != nil {
		return nil, err
	}
	left := amount
	var out []PaymentAllocationInput
	for _, o := range orders {
		if left <= 0.004 {
			break
		}
		a := math.Min(left, o.BalanceAmount)
		out = append(out, PaymentAllocationInput{OrderID: o.ID, Amount: round2(a)})
		left = round2(left - a)
	}
	return out, nil
}

// validateAllocationsTx valida cada aplicación (pedido existente, no anulado, del mismo cliente, sin pasar de su saldo) y
// devuelve el cliente del cobro (el indicado o el de los pedidos).
func (s *Service) validateAllocationsTx(tx *gorm.DB, allocs []PaymentAllocationInput, customerID *uint, ignorePaymentID uint) (*uint, error) {
	seen := map[uint]bool{}
	for _, a := range allocs {
		if a.Amount <= 0 {
			return nil, invalid("cada monto aplicado debe ser mayor a cero")
		}
		if seen[a.OrderID] {
			return nil, invalid("un pedido aparece dos veces en la aplicación del cobro")
		}
		seen[a.OrderID] = true
		var o database.EquipOrder
		if found, err := findOne(tx, &o, "id = ?", a.OrderID); err != nil {
			return nil, err
		} else if !found {
			return nil, invalid("uno de los pedidos no existe")
		}
		if o.Status == "anulado" {
			return nil, invalid("el pedido %d está anulado: no recibe cobros", o.OrderNumber)
		}
		if customerID != nil && o.CustomerID != nil && *o.CustomerID != *customerID {
			return nil, invalid("el pedido %d es de otro cliente", o.OrderNumber)
		}
		if customerID == nil && o.CustomerID != nil {
			cid := *o.CustomerID
			customerID = &cid
		}
		balance := o.BalanceAmount
		if a.Amount > balance+0.004 {
			return nil, invalid("el pedido %d solo tiene S/ %.2f por cobrar: no se pueden aplicar S/ %.2f", o.OrderNumber, balance, a.Amount)
		}
	}
	return customerID, nil
}

func (s *Service) applyAllocationsTx(tx *gorm.DB, paymentID uint, allocs []PaymentAllocationInput) error {
	for _, a := range allocs {
		if err := tx.Create(&database.EquipPaymentAllocation{PaymentID: paymentID, OrderID: a.OrderID, Amount: round2(a.Amount)}).Error; err != nil {
			return err
		}
		var o database.EquipOrder
		if err := tx.First(&o, a.OrderID).Error; err != nil {
			return err
		}
		if err := s.recalcOrderPaymentsTx(tx, &o); err != nil {
			return err
		}
	}
	return nil
}

// AllocateCredit aplica el saldo a favor de un cobro a pedidos del mismo cliente.
func (s *Service) AllocateCredit(paymentID uint, allocs []PaymentAllocationInput) (*PaymentView, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var p database.EquipPayment
		if found, err := findOne(tx, &p, "id = ?", paymentID); err != nil {
			return err
		} else if !found {
			return invalid("cobro no encontrado")
		}
		if p.Status != "vigente" {
			return invalid("el cobro está anulado")
		}
		if len(allocs) == 0 {
			return invalid("indica a qué pedido se aplica")
		}
		total := 0.0
		for _, a := range allocs {
			total += a.Amount
		}
		if round2(total) > p.UnallocatedAmount+0.004 {
			return invalid("el cobro solo tiene S/ %.2f disponibles para aplicar", p.UnallocatedAmount)
		}
		if _, err := s.validateAllocationsTx(tx, allocs, p.CustomerID, p.ID); err != nil {
			return err
		}
		if err := s.applyAllocationsTx(tx, p.ID, allocs); err != nil {
			return err
		}
		return tx.Model(&database.EquipPayment{}).Where("id = ?", p.ID).Update("unallocated_amount", math.Max(0, round2(p.UnallocatedAmount-total))).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetPayment(paymentID)
}

// VoidPayment anula un cobro: lo aplicado a pedidos deja de contar (el pedido vuelve a tener saldo).
func (s *Service) VoidPayment(id uint, reason string) (*PaymentView, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, invalid("indica el motivo de la anulación del cobro")
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var p database.EquipPayment
		if found, err := findOne(tx, &p, "id = ?", id); err != nil {
			return err
		} else if !found {
			return invalid("cobro no encontrado")
		}
		if p.Status == "anulado" {
			return invalid("el cobro ya está anulado")
		}
		p.Status = "anulado"
		p.Notes = strings.TrimSpace(p.Notes + " | ANULADO: " + reason)
		if err := tx.Save(&p).Error; err != nil {
			return err
		}
		var allocs []database.EquipPaymentAllocation
		if err := tx.Where("payment_id = ?", id).Find(&allocs).Error; err != nil {
			return err
		}
		for _, a := range allocs {
			var o database.EquipOrder
			if err := tx.First(&o, a.OrderID).Error; err != nil {
				return err
			}
			if err := s.recalcOrderPaymentsTx(tx, &o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetPayment(id)
}

func (s *Service) paymentViews(pays []database.EquipPayment) ([]PaymentView, error) {
	if len(pays) == 0 {
		return []PaymentView{}, nil
	}
	ids := make([]uint, len(pays))
	custIDs := []uint{}
	for i, p := range pays {
		ids[i] = p.ID
		if p.CustomerID != nil {
			custIDs = append(custIDs, *p.CustomerID)
		}
	}
	type allocRow struct {
		PaymentID   uint
		OrderID     uint
		OrderNumber int
		Amount      float64
	}
	var allocs []allocRow
	if err := s.db.Table("equip_payment_allocations a").
		Select("a.payment_id, a.order_id, o.order_number, a.amount").
		Joins("JOIN equip_orders o ON o.id = a.order_id").Where("a.payment_id IN ?", ids).Order("a.id").Scan(&allocs).Error; err != nil {
		return nil, err
	}
	byPay := map[uint][]AllocationView{}
	for _, a := range allocs {
		byPay[a.PaymentID] = append(byPay[a.PaymentID], AllocationView{OrderID: a.OrderID, OrderNumber: a.OrderNumber, Amount: a.Amount})
	}
	names := map[uint]string{}
	if len(custIDs) > 0 {
		var cs []database.EquipCustomer
		if err := s.db.Select("id", "name").Where("id IN ?", custIDs).Find(&cs).Error; err != nil {
			return nil, err
		}
		for _, c := range cs {
			names[c.ID] = c.Name
		}
	}
	out := make([]PaymentView, 0, len(pays))
	for _, p := range pays {
		v := PaymentView{EquipPayment: p, Invoice: invoiceText(p), Allocations: byPay[p.ID]}
		if v.Allocations == nil {
			v.Allocations = []AllocationView{}
		}
		if p.CustomerID != nil {
			v.CustomerName = names[*p.CustomerID]
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) GetPayment(id uint) (*PaymentView, error) {
	var p database.EquipPayment
	if found, err := findOne(s.db, &p, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("cobro no encontrado")
	}
	views, err := s.paymentViews([]database.EquipPayment{p})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

type PaymentFilter struct {
	CustomerID    uint
	From, To      string
	Method        string
	Status        string
	Q             string
	Page, PerPage int
}

type PaymentListResult struct {
	Rows    []PaymentView `json:"rows"`
	Total   int64         `json:"total"`
	Page    int           `json:"page"`
	PerPage int           `json:"per_page"`
	Sum     float64       `json:"sum"` // suma de los cobros vigentes del filtro
}

func (s *Service) ListPayments(f PaymentFilter) (*PaymentListResult, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 || f.PerPage > 200 {
		f.PerPage = 25
	}
	q := s.db.Model(&database.EquipPayment{})
	if f.CustomerID > 0 {
		q = q.Where("customer_id = ?", f.CustomerID)
	}
	if d, err := parseDateOnly(f.From); err != nil {
		return nil, err
	} else if d != nil {
		q = q.Where("paid_at >= ?", time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, limaLoc()))
	}
	if d, err := parseDateOnly(f.To); err != nil {
		return nil, err
	} else if d != nil {
		q = q.Where("paid_at < ?", time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, limaLoc()).AddDate(0, 0, 1))
	}
	if m := strings.TrimSpace(f.Method); m != "" {
		q = q.Where("method = ?", m)
	}
	if st := strings.TrimSpace(f.Status); st != "" {
		q = q.Where("status = ?", st)
	}
	if t := strings.TrimSpace(f.Q); t != "" {
		like := "%" + strings.ToLower(t) + "%"
		q = q.Where("LOWER(reference) LIKE ? OR invoice_number = ? OR customer_id IN (?)", like, t,
			s.db.Model(&database.EquipCustomer{}).Select("id").Where("LOWER(name) LIKE ? OR doc_number LIKE ?", like, "%"+t+"%"))
	}
	res := &PaymentListResult{Page: f.Page, PerPage: f.PerPage}
	if err := q.Session(&gorm.Session{}).Count(&res.Total).Error; err != nil {
		return nil, err
	}
	var sum float64
	if err := q.Session(&gorm.Session{}).Where("status = ?", "vigente").Select("COALESCE(SUM(amount), 0)").Scan(&sum).Error; err != nil {
		return nil, err
	}
	res.Sum = round2(sum)
	var pays []database.EquipPayment
	if err := q.Session(&gorm.Session{}).Order("paid_at DESC, id DESC").Limit(f.PerPage).Offset((f.Page - 1) * f.PerPage).Find(&pays).Error; err != nil {
		return nil, err
	}
	rows, err := s.paymentViews(pays)
	if err != nil {
		return nil, err
	}
	res.Rows = rows
	return res, nil
}

// ── Estado de cuenta ───────────────────────────────────────────────────────

type CustomerAccount struct {
	Customer     database.EquipCustomer `json:"customer"`
	TotalOrdered float64                `json:"total_ordered"`
	TotalPaid    float64                `json:"total_paid"`
	Balance      float64                `json:"balance"` // por cobrar
	Credit       float64                `json:"credit"`  // saldo a favor (cobros sin aplicar)
	Orders       []AccountOrder         `json:"orders"`
	Payments     []PaymentView          `json:"payments"`
}

type AccountOrder struct {
	ID             uint      `json:"id"`
	OrderNumber    int       `json:"order_number"`
	OrderDate      time.Time `json:"order_date"`
	TotalAmount    float64   `json:"total_amount"`
	PaidAmount     float64   `json:"paid_amount"`
	BalanceAmount  float64   `json:"balance_amount"`
	PaymentStatus  string    `json:"payment_status"`
	Status         string    `json:"status"`
	ShipmentStatus string    `json:"shipment_status"`
}

func (s *Service) CustomerAccount(id uint) (*CustomerAccount, error) {
	var c database.EquipCustomer
	if found, err := findOne(s.db, &c, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("cliente no encontrado")
	}
	acc := &CustomerAccount{Customer: c, Orders: []AccountOrder{}}
	if err := s.db.Table("equip_orders o").
		Select("o.id, o.order_number, o.order_date, o.total_amount, o.paid_amount, o.balance_amount, o.payment_status, o.status, COALESCE(sh.status, '') AS shipment_status").
		Joins("LEFT JOIN equip_shipments sh ON sh.order_id = o.id AND sh.is_current = ?", true).
		Where("o.customer_id = ?", id).Order("o.order_number DESC").Scan(&acc.Orders).Error; err != nil {
		return nil, err
	}
	for _, o := range acc.Orders {
		if o.Status == "anulado" {
			continue
		}
		acc.TotalOrdered += o.TotalAmount
		acc.Balance += o.BalanceAmount
	}
	var pays []database.EquipPayment
	if err := s.db.Where("customer_id = ?", id).Order("paid_at DESC, id DESC").Limit(300).Find(&pays).Error; err != nil {
		return nil, err
	}
	for _, p := range pays {
		if p.Status == "vigente" {
			acc.TotalPaid += p.Amount
			acc.Credit += p.UnallocatedAmount
		}
	}
	acc.TotalOrdered, acc.TotalPaid, acc.Balance, acc.Credit = round2(acc.TotalOrdered), round2(acc.TotalPaid), round2(acc.Balance), round2(acc.Credit)
	views, err := s.paymentViews(pays)
	if err != nil {
		return nil, err
	}
	acc.Payments = views
	return acc, nil
}

// OpenBalances pedidos con saldo de un cliente (para proponer a qué aplicar un cobro).
func (s *Service) OpenBalances(customerID uint) ([]AccountOrder, error) {
	var rows []AccountOrder
	err := s.db.Table("equip_orders").
		Select("id, order_number, order_date, total_amount, paid_amount, balance_amount, payment_status, status").
		Where("customer_id = ? AND status <> ? AND balance_amount > ?", customerID, "anulado", 0.004).
		Order("order_date ASC, order_number ASC").Scan(&rows).Error
	if rows == nil {
		rows = []AccountOrder{}
	}
	return rows, err
}
