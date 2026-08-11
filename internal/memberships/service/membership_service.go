package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// validateMembershipLinkedProduct exige que el ítem del catálogo sea type=service (sin stock).
func (s *MembershipService) validateMembershipLinkedProduct(productID uint) error {
	if productID == 0 {
		return nil
	}
	var pr database.TenantProduct
	if err := s.db.First(&pr, productID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("servicio no encontrado")
		}
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(pr.Type), "service") {
		return errors.New("solo se pueden vincular servicios del catálogo")
	}
	return nil
}

var (
	ErrMembershipNotFound   = errors.New("membresía no encontrada")
	ErrContactRequired      = errors.New("el cliente es obligatorio")
	ErrInvalidAmount        = errors.New("el monto debe ser mayor a cero")
	ErrNotActive = errors.New("la membresía no está activa")
	ErrBillingNotDue        = errors.New("aún no corresponde emitir el cobro de este periodo (usa anticipado si tu rol lo permite)")
	ErrSeriesBranchMismatch = errors.New("la serie no pertenece a la sucursal de la membresía")
)

type MembershipService struct {
	db *gorm.DB
}

func NewMembershipService(db *gorm.DB) *MembershipService {
	return &MembershipService{db: db}
}

func peruLoc() *time.Location {
	loc, err := time.LoadLocation("America/Lima")
	if err != nil || loc == nil {
		return time.Local
	}
	return loc
}

func parseDateYMD(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("fecha vacía")
	}
	loc := peruLoc()
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc), nil
}

func todayPeru() time.Time {
	loc := peruLoc()
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
}

func dateOnlyUTC(t time.Time) time.Time {
	loc := peruLoc()
	tt := t.In(loc)
	y, m, d := tt.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

type CreateMembershipInput struct {
	ContactID           uint     `json:"contact_id"`
	ProductID           *uint    `json:"product_id"`
	BranchID            uint     `json:"branch_id"`
	Title               string   `json:"title"`
	BillingCycle        string   `json:"billing_cycle"`
	BillingIntervalDays int      `json:"billing_interval_days"`
	Amount              float64  `json:"amount"`
	Currency            string   `json:"currency"`
	StartDate           string   `json:"start_date"` // YYYY-MM-DD
	EndDate             *string  `json:"end_date"`
	Notes               string   `json:"notes"`
	IgvAffectationType  string   `json:"igv_affectation_type"`
	PriceIncludesIgv    bool     `json:"price_includes_igv"`
}

type UpdateMembershipInput struct {
	Title               *string  `json:"title"`
	ProductID           *uint    `json:"product_id"`
	BranchID            *uint    `json:"branch_id"`
	BillingCycle        *string  `json:"billing_cycle"`
	BillingIntervalDays *int     `json:"billing_interval_days"`
	Amount              *float64 `json:"amount"`
	Currency            *string  `json:"currency"`
	EndDate             *string  `json:"end_date"`
	Notes               *string  `json:"notes"`
	IgvAffectationType  *string  `json:"igv_affectation_type"`
	PriceIncludesIgv    *bool    `json:"price_includes_igv"`
	NextBillingDate     *string  `json:"next_billing_date"`
}

type ListParams struct {
	Status    string
	ContactID uint
	BranchID  uint
	Query     string // nombre / RUC cliente (subconsulta contactos)
	Due       string // overdue | week | month | today (filtro próximo cobro, solo activas)
	Limit     int
	Offset    int
}

func (s *MembershipService) List(p ListParams) ([]database.TenantMembership, int64, error) {
	q := s.db.Model(&database.TenantMembership{})
	if p.ContactID > 0 {
		q = q.Where("contact_id = ?", p.ContactID)
	}
	if p.BranchID > 0 {
		q = q.Where("branch_id = ?", p.BranchID)
	}
	if qry := strings.TrimSpace(p.Query); qry != "" {
		like := "%" + qry + "%"
		sub := s.db.Model(&database.TenantContact{}).Select("id").Where(
			"(business_name LIKE ? OR COALESCE(trade_name,'') LIKE ? OR doc_number LIKE ?)",
			like, like, like)
		q = q.Where("contact_id IN (?)", sub)
	}
	dueFilter := strings.ToLower(strings.TrimSpace(p.Due))
	statusFromDue := dueFilter != ""
	if statusFromDue {
		day0 := todayPeru()
		switch dueFilter {
		case "overdue":
			q = q.Where("status = ? AND next_billing_date < ?", "active", day0)
		case "today":
			day1 := day0.AddDate(0, 0, 1)
			q = q.Where("status = ? AND next_billing_date >= ? AND next_billing_date < ?", "active", day0, day1)
		case "week":
			day8 := day0.AddDate(0, 0, 8)
			q = q.Where("status = ? AND next_billing_date >= ? AND next_billing_date < ?", "active", day0, day8)
		case "month":
			day31 := day0.AddDate(0, 0, 31)
			q = q.Where("status = ? AND next_billing_date >= ? AND next_billing_date < ?", "active", day0, day31)
		}
	} else if p.Status != "" {
		q = q.Where("status = ?", p.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := p.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}
	var rows []database.TenantMembership
	if err := q.Order("created_at desc").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	for i := range rows {
		s.hydrateNames(&rows[i])
	}
	return rows, total, nil
}

// ReminderCounts devuelve membresías activas con cobro vencido o con próximo cobro en los próximos 7 días (Perú).
func (s *MembershipService) ReminderCounts() (overdue int64, upcoming int64, err error) {
	day0 := todayPeru()
	day8 := day0.AddDate(0, 0, 8)
	if err = s.db.Model(&database.TenantMembership{}).Where("status = ? AND next_billing_date < ?", "active", day0).Count(&overdue).Error; err != nil {
		return 0, 0, err
	}
	if err = s.db.Model(&database.TenantMembership{}).Where("status = ? AND next_billing_date >= ? AND next_billing_date < ?", "active", day0, day8).Count(&upcoming).Error; err != nil {
		return 0, 0, err
	}
	return overdue, upcoming, nil
}

func (s *MembershipService) hydrateNames(m *database.TenantMembership) {
	var c database.TenantContact
	if err := s.db.First(&c, m.ContactID).Error; err == nil {
		m.ContactName = c.BusinessName
		m.ContactPhone = strings.TrimSpace(c.Phone)
	}
	if m.ProductID != nil && *m.ProductID > 0 {
		var pr database.TenantProduct
		if err := s.db.First(&pr, *m.ProductID).Error; err == nil {
			m.ProductName = pr.Name
		}
	}
}

func (s *MembershipService) GetByID(id uint) (*database.TenantMembership, error) {
	var m database.TenantMembership
	if err := s.db.First(&m, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMembershipNotFound
		}
		return nil, err
	}
	s.hydrateNames(&m)
	return &m, nil
}

func (s *MembershipService) Create(in CreateMembershipInput) (*database.TenantMembership, error) {
	if in.ContactID == 0 {
		return nil, ErrContactRequired
	}
	if in.BranchID == 0 {
		return nil, errors.New("sucursal obligatoria")
	}
	if in.Amount <= 0 {
		return nil, ErrInvalidAmount
	}
	cycle := NormalizeBillingCycle(in.BillingCycle)
	if cycle == CycleCustom && in.BillingIntervalDays <= 0 {
		return nil, errors.New("para ciclo personalizado indique billing_interval_days > 0")
	}
	start, err := parseDateYMD(in.StartDate)
	if err != nil {
		return nil, fmt.Errorf("start_date: %w", err)
	}
	var endPtr *time.Time
	if in.EndDate != nil && strings.TrimSpace(*in.EndDate) != "" {
		t, err := parseDateYMD(*in.EndDate)
		if err != nil {
			return nil, fmt.Errorf("end_date: %w", err)
		}
		if dateOnlyUTC(t).Before(dateOnlyUTC(start)) {
			return nil, errors.New("end_date no puede ser anterior a start_date")
		}
		endPtr = &t
	}
	var c database.TenantContact
	if err := s.db.First(&c, in.ContactID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("cliente no encontrado")
		}
		return nil, err
	}
	if c.Type != "customer" && c.Type != "both" {
		return nil, errors.New("el contacto debe ser cliente")
	}
	if in.ProductID != nil && *in.ProductID > 0 {
		if err := s.validateMembershipLinkedProduct(*in.ProductID); err != nil {
			return nil, err
		}
	}
	igv := strings.TrimSpace(in.IgvAffectationType)
	if igv == "" {
		igv = "10"
	}
	cur := strings.TrimSpace(in.Currency)
	if cur == "" {
		cur = "PEN"
	}
	next := NextBillingFrom(start, cycle, in.BillingIntervalDays)
	m := &database.TenantMembership{
		ContactID:           in.ContactID,
		ProductID:           in.ProductID,
		BranchID:            in.BranchID,
		Title:               strings.TrimSpace(in.Title),
		BillingCycle:        cycle,
		BillingIntervalDays: in.BillingIntervalDays,
		Amount:              in.Amount,
		Currency:            cur,
		StartDate:           start,
		EndDate:             endPtr,
		NextBillingDate:     next,
		Status:              "active",
		Notes:               in.Notes,
		IgvAffectationType:  igv,
		PriceIncludesIgv:    in.PriceIncludesIgv,
	}
	if err := s.db.Create(m).Error; err != nil {
		return nil, err
	}
	s.hydrateNames(m)
	return m, nil
}

func (s *MembershipService) Update(id uint, in UpdateMembershipInput) (*database.TenantMembership, error) {
	m, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	updates := map[string]interface{}{}
	if in.Title != nil {
		updates["title"] = strings.TrimSpace(*in.Title)
	}
	if in.ProductID != nil {
		pid := *in.ProductID
		if pid > 0 {
			if err := s.validateMembershipLinkedProduct(pid); err != nil {
				return nil, err
			}
			updates["product_id"] = pid
		} else {
			updates["product_id"] = nil
		}
	}
	if in.BranchID != nil && *in.BranchID > 0 {
		updates["branch_id"] = *in.BranchID
	}
	if in.BillingCycle != nil {
		updates["billing_cycle"] = NormalizeBillingCycle(*in.BillingCycle)
	}
	if in.BillingIntervalDays != nil {
		updates["billing_interval_days"] = *in.BillingIntervalDays
	}
	if in.Amount != nil {
		if *in.Amount <= 0 {
			return nil, ErrInvalidAmount
		}
		updates["amount"] = *in.Amount
	}
	if in.Currency != nil && strings.TrimSpace(*in.Currency) != "" {
		updates["currency"] = strings.TrimSpace(*in.Currency)
	}
	if in.Notes != nil {
		updates["notes"] = *in.Notes
	}
	if in.IgvAffectationType != nil {
		updates["igv_affectation_type"] = strings.TrimSpace(*in.IgvAffectationType)
	}
	if in.PriceIncludesIgv != nil {
		updates["price_includes_igv"] = *in.PriceIncludesIgv
	}
	if in.EndDate != nil {
		if strings.TrimSpace(*in.EndDate) == "" {
			updates["end_date"] = nil
		} else {
			t, err := parseDateYMD(*in.EndDate)
			if err != nil {
				return nil, err
			}
			updates["end_date"] = t
		}
	}
	if in.NextBillingDate != nil && strings.TrimSpace(*in.NextBillingDate) != "" {
		t, err := parseDateYMD(*in.NextBillingDate)
		if err != nil {
			return nil, err
		}
		updates["next_billing_date"] = t
	}
	if len(updates) == 0 {
		return m, nil
	}
	if err := s.db.Model(&database.TenantMembership{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, err
	}
	return s.GetByID(id)
}

func (s *MembershipService) SetStatus(id uint, status string) error {
	st := strings.ToLower(strings.TrimSpace(status))
	switch st {
	case "active", "paused", "cancelled", "expired":
	default:
		return errors.New("estado inválido")
	}
	res := s.db.Model(&database.TenantMembership{}).Where("id = ?", id).Update("status", st)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMembershipNotFound
	}
	return nil
}

func (s *MembershipService) Delete(id uint) error {
	res := s.db.Delete(&database.TenantMembership{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMembershipNotFound
	}
	return nil
}

type GenerateSaleInput struct {
	SeriesID       uint                   `json:"series_id"`
	IssueDate      string                 `json:"issue_date"` // YYYY-MM-DD, opcional (hoy Perú)
	PaymentMethod  string                 `json:"payment_method"`
	Payments       []salessvc.PaymentInput `json:"payments"`
	CashSessionID  *uint                  `json:"cash_session_id"`
	AllowEarly     bool                   `json:"allow_early"`
	Notes          string                 `json:"notes"`
}

func (s *MembershipService) GenerateSale(membershipID uint, userID uint, centralTenantID uint, in GenerateSaleInput) (*database.TenantSale, *database.TenantMembershipInvoice, error) {
	m, err := s.GetByID(membershipID)
	if err != nil {
		return nil, nil, err
	}
	if m.Status != "active" {
		return nil, nil, ErrNotActive
	}
	if m.EndDate != nil && dateOnlyUTC(*m.EndDate).Before(todayPeru()) {
		return nil, nil, errors.New("la membresía está vencida (end_date)")
	}
	nextDay := dateOnlyUTC(m.NextBillingDate)
	today := todayPeru()
	if !in.AllowEarly && today.Before(nextDay) {
		return nil, nil, ErrBillingNotDue
	}
	var series database.TenantDocumentSeries
	if err := s.db.First(&series, in.SeriesID).Error; err != nil {
		return nil, nil, errors.New("serie no encontrada")
	}
	if series.BranchID != m.BranchID {
		return nil, nil, ErrSeriesBranchMismatch
	}
	if !series.Active || !strings.EqualFold(strings.TrimSpace(series.Category), "venta") {
		return nil, nil, errors.New("la serie debe estar activa y ser de categoría venta")
	}
	loc := peruLoc()
	issue := time.Now().In(loc)
	if strings.TrimSpace(in.IssueDate) != "" {
		t, err := parseDateYMD(in.IssueDate)
		if err != nil {
			return nil, nil, err
		}
		issue = t
	}
	periodEnd := m.NextBillingDate
	periodStart := PrevBillingFrom(periodEnd, m.BillingCycle, m.BillingIntervalDays)

	var item salessvc.SaleItemInput
	if m.ProductID != nil && *m.ProductID > 0 {
		var pr database.TenantProduct
		if err := s.db.First(&pr, *m.ProductID).Error; err != nil {
			return nil, nil, errors.New("producto de la membresía no encontrado")
		}
		unit := pr.Unit
		if unit == "" {
			unit = "NIU"
		}
		desc := strings.TrimSpace(m.Title)
		if desc == "" {
			desc = pr.Name
		}
		desc = fmt.Sprintf("%s — periodo hasta %s", desc, periodEnd.In(loc).Format("02/01/2006"))
		pid := *m.ProductID
		item = salessvc.SaleItemInput{
			ProductID:          &pid,
			Code:               pr.Code,
			Description:        desc,
			Unit:               unit,
			Quantity:           1,
			UnitPrice:          m.Amount,
			Discount:           0,
			IgvAffectationType: m.IgvAffectationType,
			PriceIncludesIgv:   m.PriceIncludesIgv,
		}
	} else {
		desc := strings.TrimSpace(m.Title)
		if desc == "" {
			desc = "Membresía / cuota"
		}
		desc = fmt.Sprintf("%s — periodo hasta %s", desc, periodEnd.In(loc).Format("02/01/2006"))
		item = salessvc.SaleItemInput{
			ProductID:          nil,
			Code:               fmt.Sprintf("MEM-%d", m.ID),
			Description:        desc,
			Unit:               "NIU",
			Quantity:           1,
			UnitPrice:          m.Amount,
			Discount:           0,
			IgvAffectationType: m.IgvAffectationType,
			PriceIncludesIgv:   m.PriceIncludesIgv,
		}
	}
	cid := m.ContactID
	notes := strings.TrimSpace(in.Notes)
	if notes != "" {
		notes = "Membresía #" + fmt.Sprintf("%d", m.ID) + ". " + notes
	} else {
		notes = "Membresía #" + fmt.Sprintf("%d", m.ID)
	}
	taxCfg := tax.LoadFromDB(s.db)
	saleSvc := salessvc.NewSaleService(s.db)
	sale, err := saleSvc.Create(salessvc.CreateSaleInput{
		BranchID:        m.BranchID,
		ContactID:       &cid,
		UserID:          userID,
		CashSessionID:   in.CashSessionID,
		SeriesID:        in.SeriesID,
		DocType:         series.DocType,
		IssueDate:       issue,
		DueDate:         nil,
		Currency:        m.Currency,
		PaymentMethod:   strings.TrimSpace(in.PaymentMethod),
		Payments:        in.Payments,
		Notes:           notes,
		Items:           []salessvc.SaleItemInput{item},
		TaxConfig:       taxCfg,
		CentralTenantID: centralTenantID,
	})
	if err != nil {
		return nil, nil, err
	}
	inv := &database.TenantMembershipInvoice{
		MembershipID: m.ID,
		SaleID:       sale.ID,
		PeriodStart:  dateOnlyUTC(periodStart),
		PeriodEnd:    dateOnlyUTC(periodEnd),
	}
	if err := s.db.Create(inv).Error; err != nil {
		return sale, nil, fmt.Errorf("venta creada pero error vinculando historial: %w", err)
	}
	now := time.Now()
	newNext := NextBillingFrom(m.NextBillingDate, m.BillingCycle, m.BillingIntervalDays)
	if err := s.db.Model(&database.TenantMembership{}).Where("id = ?", m.ID).Updates(map[string]interface{}{
		"last_billed_at":    now,
		"next_billing_date": newNext,
	}).Error; err != nil {
		return sale, inv, fmt.Errorf("venta registrada; actualice manualmente next_billing_date: %w", err)
	}
	return sale, inv, nil
}

// MembershipBillingRow fila de historial de cobros vinculados a ventas.
type MembershipBillingRow struct {
	ID         uint      `json:"id"`
	SaleID     uint      `json:"sale_id"`
	SaleNumber string    `json:"sale_number"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *MembershipService) ListBillingHistory(membershipID uint) ([]MembershipBillingRow, error) {
	var rows []MembershipBillingRow
	err := s.db.Table("tenant_membership_invoices as i").
		Select("i.id, i.sale_id, s.number as sale_number, i.period_start, i.period_end, i.created_at").
		Joins("JOIN tenant_sales s ON s.id = i.sale_id AND s.deleted_at IS NULL").
		Where("i.membership_id = ?", membershipID).
		Order("i.id DESC").
		Limit(100).
		Scan(&rows).Error
	return rows, err
}
