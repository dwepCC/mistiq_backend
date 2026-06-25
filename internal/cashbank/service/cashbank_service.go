package service

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/taxpayment"

	"gorm.io/gorm"
)

// Marcador para concatenar notas de cierre sin perder las de apertura (un solo campo Notes en BD).
const sessionNotesClosingMarker = "\n\n[Notas de cierre]\n"

func mergeSessionNotesOnClose(existingNotes, closingNotes string) string {
	opening := strings.TrimSpace(existingNotes)
	closing := strings.TrimSpace(closingNotes)
	if closing == "" {
		return opening
	}
	if opening == "" {
		return closing
	}
	return opening + sessionNotesClosingMarker + closing
}

type CashBankService struct {
	db *gorm.DB
}

func NewCashBankService(db *gorm.DB) *CashBankService {
	return &CashBankService{db: db}
}

// =================== CAJA ===================

// OpenSessionInput datos al abrir caja (B+ / preparado Fase C).
type OpenSessionInput struct {
	BranchID       uint
	UserID         uint
	OpeningBalance float64
	Notes          string
	RegisterCode   *string
	RegisterName   *string
}

// OpenSession abre sesión de caja del usuario (máx. 1 open por branch_id + user_id).
func (s *CashBankService) OpenSession(in OpenSessionInput) (*database.TenantCashSession, error) {
	if in.BranchID == 0 || in.UserID == 0 {
		return nil, errors.New("sucursal y usuario requeridos")
	}
	var existing database.TenantCashSession
	if err := s.db.Where("branch_id = ? AND user_id = ? AND status = ?", in.BranchID, in.UserID, "open").First(&existing).Error; err == nil {
		return nil, errors.New("ya tienes una caja abierta en esta sucursal; ciérrala antes de abrir otra")
	}

	now := time.Now()
	session := &database.TenantCashSession{
		BranchID:       in.BranchID,
		UserID:         in.UserID,
		OpenedBy:       in.UserID,
		RegisterCode:   in.RegisterCode,
		RegisterName:   in.RegisterName,
		OpeningBalance: in.OpeningBalance,
		Notes:          in.Notes,
		Status:         "open",
		OpenedAt:       now,
	}
	err := s.db.Create(session).Error
	return session, err
}

func sessionOwnerID(st *database.TenantCashSession) uint {
	if st.UserID > 0 {
		return st.UserID
	}
	return st.OpenedBy
}

func (s *CashBankService) assertSessionOwnedBy(st *database.TenantCashSession, userID uint, allowAny bool) error {
	if allowAny {
		return nil
	}
	if sessionOwnerID(st) != userID {
		return errors.New("solo puede operar su propia sesión de caja")
	}
	return nil
}

// ValidateCashSessionForUser valida sesión para cobros y movimientos.
func (s *CashBankService) ValidateCashSessionForUser(cashSessionID, userID, branchID uint) (*database.TenantCashSession, error) {
	if cashSessionID == 0 {
		return nil, errors.New("sesión de caja requerida")
	}
	var st database.TenantCashSession
	if err := s.db.First(&st, cashSessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("sesión de caja no encontrada")
		}
		return nil, err
	}
	if st.Status != "open" {
		return nil, errors.New("la sesión de caja está cerrada")
	}
	if branchID > 0 && st.BranchID != branchID {
		return nil, errors.New("la sesión de caja no pertenece a la sucursal activa")
	}
	if err := s.assertSessionOwnedBy(&st, userID, false); err != nil {
		return nil, err
	}
	return &st, nil
}

// CloseSession cierra una sesión de caja. Arqueo opcional: si se envía, se valida la suma con el saldo esperado y se guarda.
// Si no se envía arqueo, closing_balance puede ser el esperado o uno manual; si se envía arqueo, closing_balance se sobrescribe con la suma del arqueo.
func (s *CashBankService) CloseSession(sessionID, userID uint, closingBalance float64, notes string, arqueo map[string]float64, allowAnyOwner bool) error {
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		return errors.New("sesión de caja no encontrada")
	}
	if session.Status == "closed" {
		return errors.New("la sesión ya está cerrada")
	}
	if err := s.assertSessionOwnedBy(&session, userID, allowAnyOwner); err != nil {
		return err
	}

	expected := s.getExpectedBalance(sessionID)
	now := time.Now()
	combinedNotes := mergeSessionNotesOnClose(session.Notes, notes)
	updates := map[string]interface{}{
		"closed_by":        userID,
		"expected_balance": expected,
		"notes":            combinedNotes,
		"status":           "closed",
		"closed_at":        now,
	}

	if len(arqueo) > 0 {
		arqueoSum := sumArqueo(arqueo)
		arqueoJSON, _ := json.Marshal(arqueo)
		updates["arqueo_json"] = string(arqueoJSON)
		updates["closing_balance"] = arqueoSum
		updates["difference"] = arqueoSum - expected
	} else {
		updates["closing_balance"] = closingBalance
		updates["difference"] = closingBalance - expected
	}

	return s.db.Model(&session).Updates(updates).Error
}

func (s *CashBankService) getExpectedBalance(sessionID uint) float64 {
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		return 0
	}
	var totalIncome, totalExpense float64
	s.db.Model(&database.TenantCashMovement{}).
		Where("cash_session_id = ? AND type = ?", sessionID, "income").
		Select("COALESCE(SUM(amount), 0)").Scan(&totalIncome)
	s.db.Model(&database.TenantCashMovement{}).
		Where("cash_session_id = ? AND type = ?", sessionID, "expense").
		Select("COALESCE(SUM(amount), 0)").Scan(&totalExpense)
	return session.OpeningBalance + totalIncome - totalExpense
}

// sumArqueo suma denominaciones: keys "200","100",...,"0.1" * cantidad.
func sumArqueo(arqueo map[string]float64) float64 {
	var total float64
	for denomStr, qty := range arqueo {
		denom, _ := strconv.ParseFloat(denomStr, 64)
		total += denom * qty
	}
	return total
}

// SaveArqueo guarda o actualiza el arqueo. Si la sesión está abierta, se puede actualizar cuando sea. Si está cerrada y no tiene arqueo, se puede registrar una vez (ya no se modifica).
func (s *CashBankService) SaveArqueo(sessionID, userID uint, arqueo map[string]float64) (sum float64, err error) {
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		return 0, errors.New("sesión de caja no encontrada")
	}
	arqueoJSON, _ := json.Marshal(arqueo)
	sum = sumArqueo(arqueo)

	if session.Status == "open" {
		if err := s.assertSessionOwnedBy(&session, userID, false); err != nil {
			return 0, err
		}
		return sum, s.db.Model(&session).Update("arqueo_json", string(arqueoJSON)).Error
	}
	// Caja cerrada
	if session.ArqueoJSON != "" {
		return 0, errors.New("esta caja ya tiene arqueo registrado y no se puede modificar")
	}
	// Registrar arqueo por primera vez en caja cerrada
	expected := s.getExpectedBalance(sessionID)
	diff := sum - expected
	return sum, s.db.Model(&session).Updates(map[string]interface{}{
		"arqueo_json":     string(arqueoJSON),
		"closing_balance": sum,
		"expected_balance": expected,
		"difference":      diff,
	}).Error
}

// GetSessionByID devuelve una sesión por ID (incluye arqueo_json para ver en modal).
func (s *CashBankService) GetSessionByID(sessionID uint) (*database.TenantCashSession, error) {
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("sesión no encontrada")
		}
		return nil, err
	}
	return &session, nil
}

// GetOpenSession retorna la sesión abierta del usuario en la sucursal (nunca la primera global).
func (s *CashBankService) GetOpenSession(branchID, userID uint) (*database.TenantCashSession, error) {
	if userID == 0 {
		return nil, errors.New("usuario requerido para consultar caja abierta")
	}
	var session database.TenantCashSession
	q := s.db.Where("status = ? AND user_id = ?", "open", userID)
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	err := q.Order("opened_at DESC").First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &session, err
}

// OpenSessionListItem fila para listado de cajas abiertas en sucursal (solo lectura).
type OpenSessionListItem struct {
	ID              uint    `json:"id"`
	BranchID        uint    `json:"branch_id"`
	UserID          uint    `json:"user_id"`
	UserName        string  `json:"user_name"`
	OpeningBalance  float64 `json:"opening_balance"`
	CurrentBalance  float64 `json:"current_balance"`
	OpenedAt        string  `json:"opened_at"`
	RegisterCode    *string `json:"register_code,omitempty"`
	RegisterName    *string `json:"register_name,omitempty"`
}

// ListOpenSessionsInBranch todas las sesiones abiertas de una sucursal (varios cajeros).
func (s *CashBankService) ListOpenSessionsInBranch(branchID uint) ([]OpenSessionListItem, error) {
	if branchID == 0 {
		return nil, errors.New("sucursal requerida")
	}
	var sessions []database.TenantCashSession
	if err := s.db.Where("branch_id = ? AND status = ?", branchID, "open").Order("opened_at ASC").Find(&sessions).Error; err != nil {
		return nil, err
	}
	items := make([]OpenSessionListItem, 0, len(sessions))
	for _, st := range sessions {
		income, expense := s.sessionMovementTotals(st.ID)
		cur := st.OpeningBalance + income - expense
		name := ""
		var u database.TenantUser
		if s.db.Select("name").First(&u, sessionOwnerID(&st)).Error == nil {
			name = u.Name
		}
		items = append(items, OpenSessionListItem{
			ID:             st.ID,
			BranchID:       st.BranchID,
			UserID:         sessionOwnerID(&st),
			UserName:       name,
			OpeningBalance: st.OpeningBalance,
			CurrentBalance: cur,
			OpenedAt:       st.OpenedAt.Format(time.RFC3339),
			RegisterCode:   st.RegisterCode,
			RegisterName:   st.RegisterName,
		})
	}
	return items, nil
}

func (s *CashBankService) sessionMovementTotals(sessionID uint) (income, expense float64) {
	s.db.Model(&database.TenantCashMovement{}).
		Where("cash_session_id = ? AND type = ?", sessionID, "income").
		Select("COALESCE(SUM(amount), 0)").Scan(&income)
	s.db.Model(&database.TenantCashMovement{}).
		Where("cash_session_id = ? AND type = ?", sessionID, "expense").
		Select("COALESCE(SUM(amount), 0)").Scan(&expense)
	return
}

// AddMovement registra un movimiento manual de caja. Verifica que la sesión exista y esté abierta.
func (s *CashBankService) AddMovement(sessionID, userID uint, movType, category, reference, paymentMethod string, amount float64, notes string) error {
	if amount <= 0 {
		return errors.New("el monto debe ser mayor a cero")
	}
	if movType != "income" && movType != "expense" {
		return errors.New("tipo de movimiento inválido")
	}
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("sesión de caja no encontrada")
		}
		return err
	}
	if session.Status != "open" {
		return errors.New("no se pueden registrar movimientos en una caja cerrada")
	}
	if err := s.assertSessionOwnedBy(&session, userID, false); err != nil {
		return err
	}
	if err := s.db.Create(&database.TenantCashMovement{
		CashSessionID: sessionID,
		Type:          movType,
		Amount:        amount,
		PaymentMethod: paymentMethod,
		Category:      category,
		Reference:     reference,
		Notes:         notes,
		UserID:        userID,
		CreatedAt:     time.Now(),
	}).Error; err != nil {
		return err
	}
	// Actualizar saldo de la cuenta financiera asociada al método de pago
	desc := "Caja: " + category
	if reference != "" {
		desc += " " + reference
	}
	return s.RecordPaymentToAccount(nil, paymentMethod, amount, movType == "income", reference, desc, userID)
}

func (s *CashBankService) GetMovements(sessionID uint) ([]database.TenantCashMovement, error) {
	var movements []database.TenantCashMovement
	err := s.db.Where("cash_session_id = ?", sessionID).Order("created_at DESC").Find(&movements).Error
	return movements, err
}

func (s *CashBankService) ListSessions(branchID uint) ([]database.TenantCashSession, error) {
	var sessions []database.TenantCashSession
	q := s.db.Model(&database.TenantCashSession{})
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	err := q.Order("opened_at DESC").Limit(50).Find(&sessions).Error
	return sessions, err
}

// CashSessionListItem sesión enriquecida para historial operativo.
type CashSessionListItem struct {
	database.TenantCashSession
	OpenedByName string  `json:"opened_by_name"`
	ClosedByName string  `json:"closed_by_name,omitempty"`
	TotalIncome  float64 `json:"total_income"`
	TotalExpense float64 `json:"total_expense"`
}

func (s *CashBankService) ListSessionsEnriched(branchID uint) ([]CashSessionListItem, error) {
	sessions, err := s.ListSessions(branchID)
	if err != nil {
		return nil, err
	}
	out := make([]CashSessionListItem, 0, len(sessions))
	for _, st := range sessions {
		item := CashSessionListItem{TenantCashSession: st}
		income, expense := s.sessionMovementTotals(st.ID)
		item.TotalIncome = income
		item.TotalExpense = expense
		var opener database.TenantUser
		if s.db.Select("name").First(&opener, st.OpenedBy).Error == nil {
			item.OpenedByName = opener.Name
		}
		if st.ClosedBy != nil && *st.ClosedBy > 0 {
			var closer database.TenantUser
			if s.db.Select("name").First(&closer, *st.ClosedBy).Error == nil {
				item.ClosedByName = closer.Name
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// =================== BANCOS ===================

// NormalizePaymentMethod normaliza el método de pago para buscar la cuenta asociada.
func NormalizePaymentMethod(m string) string {
	if m == "" {
		return "efectivo"
	}
	switch m {
	case "Efectivo", "efectivo":
		return "efectivo"
	case "Yape", "yape":
		return "yape"
	case "Plin", "plin":
		return "plin"
	case "Tarjeta", "tarjeta":
		return "tarjeta"
	case "Transferencia", "transferencia":
		return "transferencia"
	default:
		return m
	}
}

// GetAccountByPaymentMethod devuelve la primera cuenta activa asociada al método de pago.
func (s *CashBankService) GetAccountByPaymentMethod(paymentMethod string) (*database.TenantBankAccount, error) {
	method := NormalizePaymentMethod(paymentMethod)
	var acc database.TenantBankAccount
	err := s.db.Where("active = ? AND payment_method = ?", true, method).First(&acc).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &acc, nil
}

// RecordPaymentToAccount registra un movimiento en la cuenta asociada al método de pago y actualiza el saldo.
// db puede ser una transacción (tx) o nil para usar s.db. isCredit=true = ingreso (aumenta saldo), false = egreso.
func (s *CashBankService) RecordPaymentToAccount(db *gorm.DB, paymentMethod string, amount float64, isCredit bool, reference, description string, userID uint) error {
	if amount <= 0 {
		return nil
	}
	exec := s.db
	if db != nil {
		exec = db
	}
	acc, err := s.GetAccountByPaymentMethod(paymentMethod)
	if err != nil || acc == nil {
		return nil // sin cuenta configurada para este método, no fallar
	}
	movType := "debit"
	delta := -amount
	if isCredit {
		movType = "credit"
		delta = amount
	}
	now := time.Now()
	if err := exec.Create(&database.TenantBankMovement{
		BankAccountID: acc.ID,
		Type:          movType,
		Amount:        amount,
		Description:   description,
		Reference:     reference,
		Date:          now,
		UserID:        userID,
		CreatedAt:     now,
	}).Error; err != nil {
		return err
	}
	return exec.Model(&database.TenantBankAccount{}).
		Where("id = ?", acc.ID).
		Update("balance", gorm.Expr("balance + ?", delta)).Error
}

func (s *CashBankService) ListBankAccounts() ([]database.TenantBankAccount, error) {
	var accounts []database.TenantBankAccount
	err := s.db.Where("active = ?", true).Order("type ASC, name ASC").Find(&accounts).Error
	return accounts, err
}

// ListPaymentMethods devuelve códigos operativos de cobro (legacy). Preferir ListPaymentMethodRecords.
func (s *CashBankService) ListPaymentMethods() []string {
	recs, err := s.ListPaymentMethodRecords()
	if err != nil || len(recs) == 0 {
		return []string{"cash", "yape", "plin", "transferencia", "tarjeta"}
	}
	codes := make([]string, 0, len(recs))
	for _, r := range recs {
		codes = append(codes, r.Code)
	}
	return codes
}

// ListPaymentMethodRecords medios de cobro activos (solo tenant_payment_methods).
func (s *CashBankService) ListPaymentMethodRecords() ([]database.TenantPaymentMethod, error) {
	var list []database.TenantPaymentMethod
	err := s.db.Where("active = ?", true).Order("sort_order ASC, id ASC").Find(&list).Error
	return list, err
}

// ListAllPaymentMethodRecords incluye inactivos.
func (s *CashBankService) ListAllPaymentMethodRecords() ([]database.TenantPaymentMethod, error) {
	var list []database.TenantPaymentMethod
	err := s.db.Order("sort_order ASC, id ASC").Find(&list).Error
	return list, err
}

// GetPaymentMethodByID devuelve un método de pago por ID.
func (s *CashBankService) GetPaymentMethodByID(id uint) (*database.TenantPaymentMethod, error) {
	var pm database.TenantPaymentMethod
	if err := s.db.First(&pm, id).Error; err != nil {
		return nil, err
	}
	return &pm, nil
}

// GetPaymentMethodByCode devuelve un método de pago por código (cash, yape, etc.).
func (s *CashBankService) GetPaymentMethodByCode(code string) (*database.TenantPaymentMethod, error) {
	code = NormalizePaymentMethodCode(code)
	var pm database.TenantPaymentMethod
	if err := s.db.Where("code = ? AND active = ?", code, true).First(&pm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &pm, nil
}

// NormalizePaymentMethodCode normaliza para búsqueda: efectivo->cash, etc.
func NormalizePaymentMethodCode(c string) string {
	switch c {
	case "Efectivo", "efectivo":
		return "cash"
	case "Yape", "yape":
		return "yape"
	case "Plin", "plin":
		return "plin"
	case "Tarjeta", "tarjeta":
		return "tarjeta"
	case "Transferencia", "transferencia":
		return "transferencia"
	default:
		if c == "" {
			return "cash"
		}
		return c
	}
}

// CreatePaymentMethod crea un método de pago.
func (s *CashBankService) CreatePaymentMethod(name, code, destinationType string, bankAccountID *uint) (*database.TenantPaymentMethod, error) {
	if name == "" || code == "" {
		return nil, errors.New("nombre y código son requeridos")
	}
	if destinationType != "cash" && destinationType != "bank_account" {
		return nil, errors.New("destination_type debe ser cash o bank_account")
	}
	if destinationType == "bank_account" && (bankAccountID == nil || *bankAccountID == 0) {
		return nil, errors.New("debe indicar la cuenta bancaria cuando el destino es bank_account")
	}
	code = NormalizePaymentMethodCode(code)
	// Evitar duplicados de código
	var existing database.TenantPaymentMethod
	if err := s.db.Where("code = ?", code).First(&existing).Error; err == nil {
		return nil, errors.New("ya existe un método de pago con ese código")
	}
	var maxOrder int
	s.db.Model(&database.TenantPaymentMethod{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder)
	pm := &database.TenantPaymentMethod{
		Name:            name,
		Code:            code,
		DestinationType: destinationType,
		BankAccountID:   bankAccountID,
		IsSystem:        false,
		SortOrder:       maxOrder + 1,
		Active:          true,
	}
	return pm, s.db.Create(pm).Error
}

// UpdatePaymentMethod actualiza un método de pago.
func (s *CashBankService) UpdatePaymentMethod(id uint, name, code, destinationType string, bankAccountID *uint, active bool) error {
	var pm database.TenantPaymentMethod
	if err := s.db.First(&pm, id).Error; err != nil {
		return err
	}
	if pm.IsSystem {
		sysCode := NormalizePaymentMethodCode(pm.Code)
		if code != "" && NormalizePaymentMethodCode(code) != sysCode {
			return errors.New("no se puede cambiar el código de un método de sistema")
		}
		if destinationType != "" && destinationType != pm.DestinationType {
			return errors.New("no se puede cambiar el destino de un método de sistema")
		}
	}
	if name != "" {
		pm.Name = name
	}
	if code != "" && !pm.IsSystem {
		pm.Code = NormalizePaymentMethodCode(code)
	}
	if destinationType != "" && !pm.IsSystem {
		if destinationType != "cash" && destinationType != "bank_account" {
			return errors.New("destination_type debe ser cash o bank_account")
		}
		pm.DestinationType = destinationType
		if destinationType == "bank_account" && (bankAccountID == nil || *bankAccountID == 0) {
			return errors.New("debe indicar la cuenta bancaria cuando el destino es bank_account")
		}
		if destinationType == "cash" {
			pm.BankAccountID = nil
		} else if destinationType == "bank_account" {
			pm.BankAccountID = bankAccountID
		}
	}
	pm.Active = active
	return s.db.Save(&pm).Error
}

// DeletePaymentMethod elimina (soft) un método de pago. No permite eliminar is_system (cash).
func (s *CashBankService) DeletePaymentMethod(id uint) error {
	var pm database.TenantPaymentMethod
	if err := s.db.First(&pm, id).Error; err != nil {
		return err
	}
	if pm.IsSystem {
		return errors.New("no se puede eliminar un método de pago del sistema")
	}
	return s.db.Delete(&pm).Error
}

// PaymentLineInput línea de pago para resolver sesión de caja (método + monto).
type PaymentLineInput struct {
	Method string
	Amount float64
}

// ResolveCashSessionForSale vincula la venta a la sesión del cajero.
// Exige sesión abierta si hay efectivo; si el pago es solo digital, usa la sesión abierta del usuario (reportes de caja).
func (s *CashBankService) ResolveCashSessionForSale(
	branchID, userID uint,
	cashSessionID *uint,
	payments []PaymentLineInput,
) (*uint, error) {
	resolved, err := s.ResolveCashSessionForPayments(branchID, userID, cashSessionID, payments)
	if err != nil {
		return nil, err
	}
	if resolved != nil && *resolved > 0 {
		return resolved, nil
	}
	if cashSessionID != nil && *cashSessionID > 0 {
		if _, err := s.ValidateCashSessionForUser(*cashSessionID, userID, branchID); err != nil {
			return nil, err
		}
		return cashSessionID, nil
	}
	sess, err := s.GetOpenSession(branchID, userID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, nil
	}
	sid := sess.ID
	return &sid, nil
}

// ResolveCashSessionForPayments asigna la sesión de caja del usuario si hay pagos a destino efectivo.
func (s *CashBankService) ResolveCashSessionForPayments(
	branchID, userID uint,
	cashSessionID *uint,
	payments []PaymentLineInput,
) (*uint, error) {
	needsCash := false
	for _, p := range payments {
		if p.Amount <= 0 {
			continue
		}
		if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		pm, err := s.GetPaymentMethodByCode(p.Method)
		if err == nil && pm != nil && pm.DestinationType == "cash" {
			needsCash = true
			break
		}
		if strings.EqualFold(strings.TrimSpace(p.Method), "cash") || strings.EqualFold(strings.TrimSpace(p.Method), "efectivo") {
			needsCash = true
			break
		}
	}
	if !needsCash {
		return cashSessionID, nil
	}
	var sid uint
	if cashSessionID != nil && *cashSessionID > 0 {
		sid = *cashSessionID
	} else {
		sess, err := s.GetOpenSession(branchID, userID)
		if err != nil {
			return nil, err
		}
		if sess == nil {
			return nil, errors.New("se requiere sesión de caja abierta del usuario para pagos en efectivo")
		}
		sid = sess.ID
	}
	if _, err := s.ValidateCashSessionForUser(sid, userID, branchID); err != nil {
		return nil, err
	}
	return &sid, nil
}

// RecordPayment distribuye un pago según la configuración del método: a caja (TenantCashMovement) o a cuenta bancaria (TenantBankMovement).
// cashSessionID: requerido cuando destination_type=cash. saleNumber y description para referencias.
func (s *CashBankService) RecordPayment(tx *gorm.DB, paymentMethodCode string, amount float64, cashSessionID *uint, saleNumber, description string, saleID *uint, userID uint) error {
	if amount <= 0 {
		return nil
	}
	if taxpayment.IsDetractionCode(paymentMethodCode) || paymentcondition.IsCreditCode(paymentMethodCode) {
		return nil
	}
	exec := s.db
	if tx != nil {
		exec = tx
	}
	pm, err := s.GetPaymentMethodByCode(paymentMethodCode)
	if err != nil || pm == nil {
		// Fallback legacy: intentar RecordPaymentToAccount (cuenta por payment_method en TenantBankAccount)
		return s.RecordPaymentToAccount(tx, paymentMethodCode, amount, true, saleNumber, description, userID)
	}
	switch pm.DestinationType {
	case "detraction", "receivable":
		return errors.New("tipo de destino obsoleto; use tenant_payment_methods operativos")
	case "cash":
		if cashSessionID == nil || *cashSessionID == 0 {
			return errors.New("se requiere sesión de caja abierta del usuario para pagos en efectivo")
		}
		var st database.TenantCashSession
		if err := exec.First(&st, *cashSessionID).Error; err != nil {
			return errors.New("sesión de caja no encontrada")
		}
		if st.Status != "open" {
			return errors.New("no se puede registrar pago en una caja cerrada")
		}
		if userID > 0 && sessionOwnerID(&st) != userID {
			return errors.New("el pago en efectivo debe registrarse en su propia sesión de caja")
		}
		return exec.Create(&database.TenantCashMovement{
			CashSessionID: *cashSessionID,
			Type:          "income",
			Amount:        amount,
			PaymentMethod: pm.Code,
			Category:      "Venta",
			Reference:     saleNumber,
			SaleID:        saleID,
			UserID:        userID,
			CreatedAt:     time.Now(),
		}).Error
	case "bank_account":
		// Cuenta explícita en el método de pago, o la primera cuenta activa con payment_method coincidente
		// (misma lógica que Caja → Cuentas: muchos tenants vinculan solo TenantBankAccount.payment_method).
		bankAccID := uint(0)
		if pm.BankAccountID != nil && *pm.BankAccountID > 0 {
			bankAccID = *pm.BankAccountID
		} else if acc, err := s.GetAccountByPaymentMethod(pm.Code); err == nil && acc != nil {
			bankAccID = acc.ID
		}
		if bankAccID == 0 {
			return s.RecordPaymentToAccount(tx, paymentMethodCode, amount, true, saleNumber, description, userID)
		}
		delta := amount
		if err := exec.Create(&database.TenantBankMovement{
			BankAccountID: bankAccID,
			Type:          "credit",
			Amount:        amount,
			Description:   description,
			Reference:     saleNumber,
			Date:          time.Now(),
			UserID:        userID,
			CreatedAt:     time.Now(),
		}).Error; err != nil {
			return err
		}
		return exec.Model(&database.TenantBankAccount{}).
			Where("id = ?", bankAccID).
			Update("balance", gorm.Expr("balance + ?", delta)).Error
	default:
		return nil
	}
}

// ListAllBankAccounts lista todas las cuentas (activas e inactivas) para administración.
func (s *CashBankService) ListAllBankAccounts() ([]database.TenantBankAccount, error) {
	var accounts []database.TenantBankAccount
	err := s.db.Order("type ASC, name ASC").Find(&accounts).Error
	return accounts, err
}

func (s *CashBankService) CreateBankAccount(name, bankName, accountNumber, currency, accountType, paymentMethod string, initialBalance float64) (*database.TenantBankAccount, error) {
	if name == "" {
		return nil, errors.New("nombre de cuenta requerido")
	}
	if currency == "" {
		currency = "PEN"
	}
	if accountType == "" {
		accountType = "bank"
	}
	acc := &database.TenantBankAccount{
		Name:          name,
		BankName:      bankName,
		AccountNumber: accountNumber,
		Currency:      currency,
		Balance:       initialBalance,
		Type:          accountType,
		Active:        true,
	}
	if paymentMethod != "" {
		acc.PaymentMethod = NormalizePaymentMethod(paymentMethod)
	}
	err := s.db.Create(acc).Error
	return acc, err
}

// UpdateBankAccount actualiza nombre, tipo, método de pago y estado. No modifica el saldo.
func (s *CashBankService) UpdateBankAccount(id uint, name, bankName, accountNumber, accountType, paymentMethod string, active bool) error {
	updates := map[string]interface{}{
		"name": name, "bank_name": bankName, "account_number": accountNumber,
		"type": accountType, "active": active,
	}
	if paymentMethod != "" {
		updates["payment_method"] = NormalizePaymentMethod(paymentMethod)
	} else {
		updates["payment_method"] = ""
	}
	return s.db.Model(&database.TenantBankAccount{}).Where("id = ?", id).Updates(updates).Error
}

// GetBankAccountByID devuelve una cuenta por ID.
func (s *CashBankService) GetBankAccountByID(id uint) (*database.TenantBankAccount, error) {
	var acc database.TenantBankAccount
	if err := s.db.First(&acc, id).Error; err != nil {
		return nil, err
	}
	return &acc, nil
}

func (s *CashBankService) AddBankMovement(accountID, userID uint, movType, description, reference string, amount float64, date time.Time) error {
	if amount <= 0 {
		return errors.New("monto inválido")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// Registrar movimiento
		if err := tx.Create(&database.TenantBankMovement{
			BankAccountID: accountID,
			Type:          movType,
			Amount:        amount,
			Description:   description,
			Reference:     reference,
			Date:          date,
			UserID:        userID,
			CreatedAt:     time.Now(),
		}).Error; err != nil {
			return err
		}

		// Actualizar saldo
		var delta float64
		if movType == "credit" {
			delta = amount
		} else {
			delta = -amount
		}
		return tx.Model(&database.TenantBankAccount{}).
			Where("id = ?", accountID).
			Update("balance", gorm.Expr("balance + ?", delta)).Error
	})
}

func (s *CashBankService) ListBankMovements(accountID uint) ([]database.TenantBankMovement, error) {
	var movements []database.TenantBankMovement
	err := s.db.Where("bank_account_id = ?", accountID).Order("date DESC, created_at DESC").Find(&movements).Error
	return movements, err
}
