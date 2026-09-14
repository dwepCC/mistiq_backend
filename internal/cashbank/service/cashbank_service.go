package service

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/salescope"
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
	income, expense := s.cashOnlyMovementTotals(sessionID)
	return session.OpeningBalance + income - expense
}

// cashOnlyMovementTotals ingresos/egresos de la sesión que afectan el EFECTIVO FÍSICO de la caja.
//
// Antes se sumaba/restaba cualquier TenantCashMovement de la sesión sin mirar payment_method —
// un egreso pagado por transferencia (plata que salió del banco, no del cajón) restaba igual que
// uno en efectivo, así que el "esperado" podía dar negativo o simplemente falso apenas la caja
// también registraba movimientos bancarios (pago a proveedor por transferencia, por ejemplo).
// IsCashPaymentMethod ya es el criterio que usa el resto de este paquete para "qué cuenta como
// efectivo" (ver payment_method_report.go) — se reusa acá para no tener dos definiciones de lo
// mismo.
func (s *CashBankService) cashOnlyMovementTotals(sessionID uint) (income, expense float64) {
	var movements []database.TenantCashMovement
	s.db.Where("cash_session_id = ? AND type IN ?", sessionID, []string{"income", "expense"}).
		Find(&movements)
	for _, m := range movements {
		if !IsCashPaymentMethod(m.PaymentMethod) {
			continue
		}
		if m.Type == "income" {
			income += m.Amount
		} else {
			expense += m.Amount
		}
	}
	return
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
		"arqueo_json":      string(arqueoJSON),
		"closing_balance":  sum,
		"expected_balance": expected,
		"difference":       diff,
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

// GetAnyOpenSessionInBranch sesión abierta más reciente de la sucursal (cualquier cajero).
// Sirve para vincular ventas de usuarios sin caja propia (p. ej. administrador) al turno activo.
func (s *CashBankService) GetAnyOpenSessionInBranch(branchID uint) (*database.TenantCashSession, error) {
	if branchID == 0 {
		return nil, nil
	}
	var session database.TenantCashSession
	err := s.db.Where("branch_id = ? AND status = ?", branchID, "open").
		Order("opened_at DESC").First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &session, err
}

// OpenSessionListItem fila para listado de cajas abiertas en sucursal (solo lectura).
type OpenSessionListItem struct {
	ID             uint    `json:"id"`
	BranchID       uint    `json:"branch_id"`
	UserID         uint    `json:"user_id"`
	UserName       string  `json:"user_name"`
	OpeningBalance float64 `json:"opening_balance"`
	CurrentBalance float64 `json:"current_balance"`
	OpenedAt       string  `json:"opened_at"`
	RegisterCode   *string `json:"register_code,omitempty"`
	RegisterName   *string `json:"register_name,omitempty"`
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

// sessionMovementTotals usada por ListOpenSessionsInBranch para mostrar el saldo actual de cada
// caja abierta — mismo criterio de "qué es efectivo" que getExpectedBalance (cashOnlyMovementTotals),
// para no calcular el mismo número con dos fórmulas distintas en dos pantallas.
func (s *CashBankService) sessionMovementTotals(sessionID uint) (income, expense float64) {
	return s.cashOnlyMovementTotals(sessionID)
}

// AddMovement registra un movimiento manual de caja. Verifica que la sesión exista y esté abierta.
// contactID: proveedor/cliente vinculado (opcional, típico en un egreso a proveedor sin compra
// registrada todavía) — nil cuando el movimiento no se vincula a ningún contacto.
func (s *CashBankService) AddMovement(sessionID, userID uint, movType, category, reference, paymentMethod string, amount float64, notes string, contactID *uint) error {
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
	if contactID != nil && *contactID > 0 {
		var contact database.TenantContact
		if err := s.db.First(&contact, *contactID).Error; err != nil {
			return errors.New("contacto no encontrado")
		}
	} else {
		contactID = nil
	}

	desc := "Caja: " + category
	if reference != "" {
		desc += " " + reference
	}

	// Un ingreso/egreso manual va a EXACTAMENTE un destino, nunca a los dos: TenantBankMovement
	// si el método tiene una cuenta bancaria asociada (Yape/Plin/Tarjeta/Transferencia/lo que
	// esté configurado), o TenantCashMovement en cualquier otro caso (efectivo, o un método sin
	// cuenta configurada). Antes se creaban SIEMPRE ambos — el TenantCashMovement de abajo,
	// incondicional, más esta misma llamada a RecordPaymentToAccount — así que un ingreso/egreso
	// manual por un método con cuenta bancaria quedaba duplicado: una vez como movimiento de
	// caja (con ese payment_method "impostado", contaminando tenant_cash_movements) y otra vez,
	// correctamente, como movimiento bancario. resolveAccountForPaymentMethod es la misma
	// resolución que usa RecordPaymentToAccount internamente, así que esta decisión nunca puede
	// divergir de lo que esa función haría.
	//
	// No se delega en RecordPaymentToAccount (que no conoce category/notes: los usa solo
	// GetMovements/GetSessionReport/ReverseManualMovement para mostrar y revertir un manual no
	// efectivo con los mismos datos que uno en efectivo) — se crea aquí mismo para no forzar esos
	// dos campos, ajenos a un pago de venta/compra, en la firma compartida por esos otros casos.
	if acc, err := s.resolveAccountForPaymentMethod(paymentMethod); err == nil && acc != nil {
		movType2 := "debit"
		delta := -amount
		if movType == "income" {
			movType2 = "credit"
			delta = amount
		}
		now := time.Now()
		if err := s.db.Create(&database.TenantBankMovement{
			BankAccountID: acc.ID,
			Type:          movType2,
			Amount:        amount,
			Description:   desc,
			Reference:     reference,
			Category:      category,
			Notes:         notes,
			Date:          now,
			UserID:        userID,
			CashSessionID: &sessionID,
			ContactID:     contactID,
			CreatedAt:     now,
		}).Error; err != nil {
			return err
		}
		return s.db.Model(&database.TenantBankAccount{}).
			Where("id = ?", acc.ID).
			Update("balance", gorm.Expr("balance + ?", delta)).Error
	}
	return s.db.Create(&database.TenantCashMovement{
		CashSessionID: sessionID,
		Type:          movType,
		Amount:        amount,
		PaymentMethod: paymentMethod,
		Category:      category,
		Reference:     reference,
		Notes:         notes,
		UserID:        userID,
		ContactID:     contactID,
		CreatedAt:     time.Now(),
	}).Error
}

// CashMovementView una fila de la lista de movimientos MANUALES de una sesión (pantalla de
// Caja): unifica tenant_cash_movements (efectivo) y tenant_bank_movements manuales (no efectivo,
// sale_id/purchase_id nulos) — desde el fix de la duplicación en AddMovement, un manual vive en
// EXACTAMENTE una de las dos tablas según su método de pago, así que antes de esto GetMovements
// (que solo consultaba tenant_cash_movements) dejaba invisibles todos los manuales no efectivo
// (Yape/Plin/transferencia/tarjeta) — sin poder verlos ni revertirlos desde esta pantalla.
//
// Kind distingue el origen porque ambas tablas tienen su propia secuencia de IDs (un mismo
// número puede repetirse entre ellas) — ReverseManualMovement lo exige tal cual, sin adivinar.
type CashMovementView struct {
	ID            uint      `json:"id"`
	Kind          string    `json:"kind"` // "cash" | "bank"
	CashSessionID uint      `json:"cash_session_id"`
	Type          string    `json:"type"` // income, expense
	Amount        float64   `json:"amount"`
	PaymentMethod string    `json:"payment_method"`
	Category      string    `json:"category"`
	Reference     string    `json:"reference"`
	Notes         string    `json:"notes"`
	SaleID        *uint     `json:"sale_id,omitempty"`
	PurchaseID    *uint     `json:"purchase_id,omitempty"`
	ReversalOfID  *uint     `json:"reversal_of_id,omitempty"`
	UserID        uint      `json:"user_id"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *CashBankService) GetMovements(sessionID uint) ([]CashMovementView, error) {
	var cashMovs []database.TenantCashMovement
	if err := s.db.Where("cash_session_id = ?", sessionID).Find(&cashMovs).Error; err != nil {
		return nil, err
	}
	views := make([]CashMovementView, 0, len(cashMovs))
	for _, m := range cashMovs {
		views = append(views, CashMovementView{
			ID: m.ID, Kind: "cash", CashSessionID: m.CashSessionID, Type: m.Type, Amount: m.Amount,
			PaymentMethod: m.PaymentMethod, Category: m.Category, Reference: m.Reference, Notes: m.Notes,
			SaleID: m.SaleID, PurchaseID: m.PurchaseID, ReversalOfID: m.ReversalOfID, UserID: m.UserID,
			CreatedAt: m.CreatedAt,
		})
	}

	var bankMovs []database.TenantBankMovement
	if err := s.db.Where("cash_session_id = ? AND sale_id IS NULL AND purchase_id IS NULL", sessionID).Find(&bankMovs).Error; err != nil {
		return nil, err
	}
	if len(bankMovs) > 0 {
		methodByAccount := s.paymentMethodCodesByBankAccount(bankMovs)
		for _, m := range bankMovs {
			movType := "income"
			if m.Type == "debit" {
				movType = "expense"
			}
			views = append(views, CashMovementView{
				ID: m.ID, Kind: "bank", CashSessionID: sessionID, Type: movType, Amount: m.Amount,
				PaymentMethod: methodByAccount[m.BankAccountID], Category: m.Category, Reference: m.Reference,
				Notes: m.Notes, ReversalOfID: m.ReversalOfID, UserID: m.UserID, CreatedAt: m.CreatedAt,
			})
		}
	}

	sort.Slice(views, func(i, j int) bool { return views[i].CreatedAt.After(views[j].CreatedAt) })
	return views, nil
}

// SessionListParams filtros/paginación para el historial de sesiones de Caja — mismo patrón que
// BankMovementListParams/ListBankMovementsPaged.
type SessionListParams struct {
	BranchID uint
	// OpenedBy: 0 = sin filtrar (quien administra cualquier caja ve todas); > 0 = solo las
	// sesiones abiertas por ese usuario. Se filtra en la propia consulta SQL (antes se traían
	// TODAS y se filtraban después, en el handler, con filterSessionsForCaller) — necesario para
	// que el total y el offset de la paginación sean correctos también para quien no administra
	// cualquier caja, no solo para calcular menos filas de más.
	OpenedBy uint
	Page     int
	PerPage  int
}

// ListSessions: Page/PerPage <= 0 → comportamiento histórico (hasta 50 más recientes, total=0,
// nadie lo necesitaba). Page/PerPage > 0 → esa página + el total real de filas que ve el
// llamador, para armar la paginación en pantalla.
func (s *CashBankService) ListSessions(params SessionListParams) ([]database.TenantCashSession, int64, error) {
	q := s.db.Model(&database.TenantCashSession{})
	if params.BranchID > 0 {
		q = q.Where("branch_id = ?", params.BranchID)
	}
	if params.OpenedBy > 0 {
		q = q.Where("opened_by = ?", params.OpenedBy)
	}

	if params.PerPage <= 0 {
		var sessions []database.TenantCashSession
		err := q.Order("opened_at DESC").Limit(50).Find(&sessions).Error
		return sessions, 0, err
	}

	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page := params.Page
	if page < 1 {
		page = 1
	}
	perPage := params.PerPage
	if perPage > 200 {
		perPage = 200
	}
	var sessions []database.TenantCashSession
	err := q.Order("opened_at DESC").Limit(perPage).Offset((page - 1) * perPage).Find(&sessions).Error
	return sessions, total, err
}

// CashSessionListItem sesión enriquecida para historial operativo.
type CashSessionListItem struct {
	database.TenantCashSession
	OpenedByName string  `json:"opened_by_name"`
	ClosedByName string  `json:"closed_by_name,omitempty"`
	TotalIncome  float64 `json:"total_income"`
	TotalExpense float64 `json:"total_expense"`
	// Total: saldo de la sesión con TODOS los métodos de pago (efectivo + Yape/Plin/
	// transferencia/tarjeta) — mismo número que SessionBalanceSummary.Total (la tarjeta de
	// "Total de la sesión" en pantalla). TotalIncome/TotalExpense de arriba son solo efectivo
	// (cashOnlyMovementTotals, pensados para comparar contra el arqueo) — no alcanzan para dar
	// una idea del saldo real de una sesión con ventas Yape/tarjeta, por eso este campo aparte.
	Total float64 `json:"total"`
	// Empty caja sin movimientos ni ventas: se puede eliminar sin perder nada.
	// No basta con que los totales den cero (un ingreso y un egreso iguales se anulan).
	Empty bool `json:"empty"`
}

func (s *CashBankService) ListSessionsEnriched(params SessionListParams) ([]CashSessionListItem, int64, error) {
	sessions, total, err := s.ListSessions(params)
	if err != nil {
		return nil, 0, err
	}
	out := make([]CashSessionListItem, 0, len(sessions))
	for _, st := range sessions {
		item := CashSessionListItem{TenantCashSession: st}
		income, expense := s.sessionMovementTotals(st.ID)
		item.TotalIncome = income
		item.TotalExpense = expense
		if summary, err := s.GetSessionBalanceSummary(st.ID); err == nil {
			item.Total = summary.Total
		}
		if usage, err := s.SessionUsageOf(st.ID); err == nil {
			item.Empty = usage.Empty()
		}
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
	return out, total, nil
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

// resolveAccountForPaymentMethod cuenta destino para un método de pago — Fase 4: prioriza el FK
// tenant_payment_methods.bank_account_id (fuente principal para configuraciones nuevas); si el
// método no tiene un TenantPaymentMethod activo o ese método no tiene cuenta vinculada por FK
// (o el FK apunta a una cuenta ya eliminada), cae al texto legado
// tenant_bank_accounts.payment_method — compatibilidad con tenants configurados solo por esa
// vía, sin migración de datos ni eliminación de ninguna columna.
//
// recordDirectedPayment (rama "bank_account", usada por ventas/compras) ya priorizaba el FK
// correctamente por su cuenta; el hueco estaba acá, en RecordPaymentToAccount (usada por
// AddMovement — ingresos/egresos manuales — y como fallback cuando el método no se encuentra
// en tenant_payment_methods), que solo miraba el texto legado.
func (s *CashBankService) resolveAccountForPaymentMethod(paymentMethod string) (*database.TenantBankAccount, error) {
	if pm, err := s.GetPaymentMethodByCode(paymentMethod); err == nil && pm != nil && pm.BankAccountID != nil && *pm.BankAccountID > 0 {
		var acc database.TenantBankAccount
		if err := s.db.First(&acc, *pm.BankAccountID).Error; err == nil {
			return &acc, nil
		}
	}
	return s.GetAccountByPaymentMethod(paymentMethod)
}

// RecordPaymentToAccount registra un movimiento en la cuenta asociada al método de pago y actualiza el saldo.
// db puede ser una transacción (tx) o nil para usar s.db. isCredit=true = ingreso (aumenta saldo), false = egreso.
// saleID/purchaseID: vínculo tipado opcional al documento de origen (nil si no aplica).
// cashSessionID: sesión de Caja en la que ocurrió el movimiento (nil si no hay ninguna resuelta,
// p. ej. compra sin sesión abierta) — trazabilidad de sesión para movimientos que van a cuenta
// bancaria/billetera, igual que ya tiene TenantSale.CashSessionID para la venta completa.
// Parámetro agregado sin romper compatibilidad: RecordPaymentToAccount solo se llama desde
// cashbank_service.go y purchase_service.go (verificado — internal/restaurant no la usa), así
// que no afecta a Tukichef.
func (s *CashBankService) RecordPaymentToAccount(db *gorm.DB, paymentMethod string, amount float64, isCredit bool, reference, description string, userID uint, saleID, purchaseID, cashSessionID *uint) error {
	if amount <= 0 {
		return nil
	}
	exec := s.db
	if db != nil {
		exec = db
	}
	acc, err := s.resolveAccountForPaymentMethod(paymentMethod)
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
		SaleID:        saleID,
		PurchaseID:    purchaseID,
		CashSessionID: cashSessionID,
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

// ResolveCashSessionForSale exige sesión de caja abierta del propio usuario.
// Nadie puede registrar ventas sin caja (administrador incluido).
func (s *CashBankService) ResolveCashSessionForSale(
	branchID, userID uint,
	cashSessionID *uint,
	payments []PaymentLineInput,
) (*uint, error) {
	return s.resolveCashSessionRequired(branchID, userID, cashSessionID, payments,
		"debe abrir una sesión de caja antes de registrar ventas")
}

// ResolveCashSessionForPurchase exige sesión de caja abierta del propio usuario, igual que
// ResolveCashSessionForSale — cualquier compra (efectivo o no) queda vinculada de forma
// determinística a la sesión del usuario que la registra, sin importar el método de pago.
// Antes, una compra no-efectivo no pasaba por aquí y quedaba con cash_session_id NULL (ver
// purchase_service.Create); esta función es el mismo mecanismo ya usado y probado para ventas,
// solo con el mensaje de error propio de compras.
func (s *CashBankService) ResolveCashSessionForPurchase(
	branchID, userID uint,
	cashSessionID *uint,
	payments []PaymentLineInput,
) (*uint, error) {
	return s.resolveCashSessionRequired(branchID, userID, cashSessionID, payments,
		"debe abrir una sesión de caja antes de registrar compras")
}

// ResolveCashSessionForCollection exige sesión de caja abierta del propio usuario para un COBRO
// posterior (venta a crédito ya registrada) — mismo mecanismo que ResolveCashSessionForSale/
// ResolveCashSessionForPurchase, sin importar el método de pago. Antes, ReceivableService.Collect
// llamaba directo a ResolveCashSessionForPayments (la versión condicional, que solo exige/resuelve
// sesión cuando el destino es efectivo), así que un cobro 100% no-efectivo podía registrarse sin
// ninguna sesión. Esta función cierra esa brecha reutilizando el mismo patrón ya probado.
func (s *CashBankService) ResolveCashSessionForCollection(
	branchID, userID uint,
	cashSessionID *uint,
	payments []PaymentLineInput,
) (*uint, error) {
	return s.resolveCashSessionRequired(branchID, userID, cashSessionID, payments,
		"debe abrir una sesión de caja antes de registrar un cobro")
}

// ResolveCashSessionForPayable exige sesión de caja abierta del propio usuario para un PAGO A
// PROVEEDOR posterior (compra a crédito ya registrada) — mismo mecanismo que
// ResolveCashSessionForCollection (su equivalente en ventas/CxC), sin importar el método de pago.
func (s *CashBankService) ResolveCashSessionForPayable(
	branchID, userID uint,
	cashSessionID *uint,
	payments []PaymentLineInput,
) (*uint, error) {
	return s.resolveCashSessionRequired(branchID, userID, cashSessionID, payments,
		"debe abrir una sesión de caja antes de registrar un pago a proveedor")
}

// resolveCashSessionRequired resuelve la sesión de caja del usuario para una operación que SIEMPRE
// debe quedar vinculada a una sesión (venta o compra), sin importar el método de pago — a
// diferencia de ResolveCashSessionForPayments, que solo exige/resuelve sesión cuando el destino es
// efectivo. Compartida por ResolveCashSessionForSale y ResolveCashSessionForPurchase para no
// duplicar esta lógica ni divergir entre ambas.
func (s *CashBankService) resolveCashSessionRequired(
	branchID, userID uint,
	cashSessionID *uint,
	payments []PaymentLineInput,
	errNoCash string,
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
		return nil, errors.New(errNoCash)
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
		// El pago no requiere efectivo (Yape/Plin/transferencia/tarjeta), pero si el llamador
		// igual indicó una sesión de caja (p. ej. cash_session_id enviado por el frontend para
		// trazabilidad), esa sesión debe validarse con la misma regla que ya se aplica a los
		// pagos en efectivo: que exista, que esté abierta y que pertenezca al usuario/sucursal.
		// Sin esto, un cliente podía asociar un pago no-efectivo a una sesión ya cerrada o ajena
		// sin ninguna verificación.
		if cashSessionID != nil && *cashSessionID > 0 {
			if _, err := s.ValidateCashSessionForUser(*cashSessionID, userID, branchID); err != nil {
				return nil, err
			}
		}
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
// Firma y comportamiento sin cambios (Tukichef la llama directamente desde restaurant_service.go)
// — es un wrapper fino sobre recordDirectedPayment con dirección "income"/"Venta".
func (s *CashBankService) RecordPayment(tx *gorm.DB, paymentMethodCode string, amount float64, cashSessionID *uint, saleNumber, description string, saleID *uint, userID uint) error {
	return s.recordDirectedPayment(tx, directedPaymentInput{
		paymentMethodCode: paymentMethodCode,
		amount:            amount,
		cashSessionID:     cashSessionID,
		reference:         saleNumber,
		description:       description,
		saleID:            saleID,
		userID:            userID,
		cashType:          "income",
		cashCategory:      "Venta",
		bankType:          "credit",
	})
}

// RecordExpensePayment es el equivalente de RecordPayment para EGRESOS ligados a un método de
// pago configurado (hoy: compras) — mismo enrutamiento cash/bank_account que RecordPayment,
// pero como egreso: TenantCashMovement type=expense/category="Compra", o TenantBankMovement
// type=debit con el saldo de la cuenta restando en vez de sumando.
//
// Función hermana en vez de agregarle una dirección a RecordPayment: RecordPayment la llama
// Tukichef directamente (restaurant_service.go) y no se quiso arriesgar su firma ni su
// comportamiento. Ambas comparten la lógica de enrutamiento vía recordDirectedPayment.
func (s *CashBankService) RecordExpensePayment(tx *gorm.DB, paymentMethodCode string, amount float64, cashSessionID *uint, docNumber, description string, purchaseID *uint, userID uint) error {
	return s.recordDirectedPayment(tx, directedPaymentInput{
		paymentMethodCode: paymentMethodCode,
		amount:            amount,
		cashSessionID:     cashSessionID,
		reference:         docNumber,
		description:       description,
		purchaseID:        purchaseID,
		userID:            userID,
		cashType:          "expense",
		cashCategory:      "Compra",
		bankType:          "debit",
	})
}

type directedPaymentInput struct {
	paymentMethodCode string
	amount            float64
	cashSessionID     *uint
	reference         string
	description       string
	saleID            *uint
	purchaseID        *uint
	userID            uint
	cashType          string // "income" | "expense" (tenant_cash_movements.type)
	cashCategory      string // "Venta" | "Compra" (tenant_cash_movements.category)
	bankType          string // "credit" | "debit" (tenant_bank_movements.type)
}

// recordDirectedPayment enrutamiento compartido de RecordPayment/RecordExpensePayment: mismo
// método de pago → mismo destino (caja o cuenta bancaria), solo cambia el sentido del dinero.
func (s *CashBankService) recordDirectedPayment(tx *gorm.DB, in directedPaymentInput) error {
	if in.amount <= 0 {
		return nil
	}
	if taxpayment.IsDetractionCode(in.paymentMethodCode) || paymentcondition.IsCreditCode(in.paymentMethodCode) {
		return nil
	}
	exec := s.db
	if tx != nil {
		exec = tx
	}
	isCredit := in.bankType == "credit"
	pm, err := s.GetPaymentMethodByCode(in.paymentMethodCode)
	if err != nil || pm == nil {
		// Fallback legacy: intentar RecordPaymentToAccount (cuenta por payment_method en TenantBankAccount)
		return s.RecordPaymentToAccount(tx, in.paymentMethodCode, in.amount, isCredit, in.reference, in.description, in.userID, in.saleID, in.purchaseID, in.cashSessionID)
	}
	switch pm.DestinationType {
	case "detraction", "receivable":
		return errors.New("tipo de destino obsoleto; use tenant_payment_methods operativos")
	case "cash":
		if in.cashSessionID == nil || *in.cashSessionID == 0 {
			return errors.New("se requiere sesión de caja abierta del usuario para pagos en efectivo")
		}
		var st database.TenantCashSession
		if err := exec.First(&st, *in.cashSessionID).Error; err != nil {
			return errors.New("sesión de caja no encontrada")
		}
		if st.Status != "open" {
			return errors.New("no se puede registrar pago en una caja cerrada")
		}
		if in.userID > 0 && sessionOwnerID(&st) != in.userID {
			return errors.New("el pago en efectivo debe registrarse en su propia sesión de caja")
		}
		return exec.Create(&database.TenantCashMovement{
			CashSessionID: *in.cashSessionID,
			Type:          in.cashType,
			Amount:        in.amount,
			PaymentMethod: pm.Code,
			Category:      in.cashCategory,
			Reference:     in.reference,
			SaleID:        in.saleID,
			PurchaseID:    in.purchaseID,
			UserID:        in.userID,
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
			return s.RecordPaymentToAccount(tx, in.paymentMethodCode, in.amount, isCredit, in.reference, in.description, in.userID, in.saleID, in.purchaseID, in.cashSessionID)
		}
		delta := in.amount
		if !isCredit {
			delta = -in.amount
		}
		if err := exec.Create(&database.TenantBankMovement{
			BankAccountID: bankAccID,
			Type:          in.bankType,
			Amount:        in.amount,
			Description:   in.description,
			Reference:     in.reference,
			Date:          time.Now(),
			UserID:        in.userID,
			SaleID:        in.saleID,
			PurchaseID:    in.purchaseID,
			CashSessionID: in.cashSessionID,
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

// BankMovementListParams filtros/paginación para el listado de movimientos de una cuenta bancaria.
type BankMovementListParams struct {
	DateFrom *time.Time
	DateTo   *time.Time
	// Type: "" = todos | "credit" | "debit". Cualquier otro valor se ignora (se trata como "").
	Type    string
	Page    int
	PerPage int
}

// BankMovementListSummary totales del período/filtros aplicados (no solo la página actual) —
// mismo patrón que SaleListSummary, para que las tarjetas de resumen del front cuadren con el
// total real aunque solo se muestre una página de filas.
type BankMovementListSummary struct {
	SumCredit float64 `json:"sum_credit"`
	SumDebit  float64 `json:"sum_debit"`
}

// BankMovementRow fila de movimiento + IsReversed: si algún otro movimiento apunta a esta fila
// con reversal_of_id, es decir, ya se revirtió (anulación de venta o devolución de nota parcial).
// Se calcula aparte de la fila en sí porque abarca toda la cuenta, no solo la página actual —
// el original y su reversión pueden caer en páginas distintas según el orden de fecha.
type BankMovementRow struct {
	database.TenantBankMovement
	IsReversed bool `json:"is_reversed"`
}

// ListBankMovementsPaged lista movimientos paginados y filtrados por fecha/tipo, con el total de
// filas (para armar la paginación) y el resumen de ingresos/egresos del período completo.
func (s *CashBankService) ListBankMovementsPaged(accountID uint, params BankMovementListParams) ([]BankMovementRow, int64, BankMovementListSummary, error) {
	q := s.db.Model(&database.TenantBankMovement{}).Where("bank_account_id = ?", accountID).
		// Backstop de lectura: ningún movimiento cuyo sale_id apunte a una nota de crédito/débito
		// debe sumar aquí ni al resumen del período — una nota nunca es un cobro real (ver
		// receivables/service/balance.go HasOpenReceivable, que evita que se genere uno nuevo; esto
		// cubre además cualquier fila ya existente de antes de ese fix, o creada por otra vía). Los
		// movimientos manuales (sale_id NULL) y los de reversión (que llevan el sale_id de la venta
		// ORIGINAL, nunca el de la nota — ver CreateBankReversal) no se ven afectados.
		Where("sale_id IS NULL OR sale_id NOT IN (?)",
			s.db.Model(&database.TenantSale{}).Select("id").Where("doc_type IN ?", salescope.NoteDocTypes))
	if params.DateFrom != nil {
		q = q.Where("date >= ?", params.DateFrom)
	}
	if params.DateTo != nil {
		q = q.Where("date <= ?", params.DateTo)
	}
	if params.Type == "credit" || params.Type == "debit" {
		q = q.Where("type = ?", params.Type)
	}

	var summaryRow struct {
		SumCredit float64
		SumDebit  float64
	}
	sumQ := q.Session(&gorm.Session{}).Select(`
		COALESCE(SUM(CASE WHEN type = 'credit' THEN amount ELSE 0 END), 0) AS sum_credit,
		COALESCE(SUM(CASE WHEN type = 'debit' THEN amount ELSE 0 END), 0) AS sum_debit`)
	if err := sumQ.Scan(&summaryRow).Error; err != nil {
		return nil, 0, BankMovementListSummary{}, err
	}
	summary := BankMovementListSummary{SumCredit: summaryRow.SumCredit, SumDebit: summaryRow.SumDebit}

	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, summary, err
	}

	page := params.Page
	if page < 1 {
		page = 1
	}
	perPage := params.PerPage
	if perPage <= 0 {
		perPage = 25
	}
	if perPage > 200 {
		perPage = 200
	}

	var movements []database.TenantBankMovement
	if err := q.Order("date DESC, created_at DESC").
		Limit(perPage).Offset((page - 1) * perPage).
		Find(&movements).Error; err != nil {
		return nil, total, summary, err
	}

	rows := make([]BankMovementRow, len(movements))
	ids := make([]uint, len(movements))
	for i, m := range movements {
		rows[i] = BankMovementRow{TenantBankMovement: m}
		ids[i] = m.ID
	}
	if len(ids) > 0 {
		var reversedIDs []uint
		if err := s.db.Model(&database.TenantBankMovement{}).
			Where("reversal_of_id IN ?", ids).
			Pluck("reversal_of_id", &reversedIDs).Error; err != nil {
			return rows, total, summary, err
		}
		reversedSet := make(map[uint]bool, len(reversedIDs))
		for _, id := range reversedIDs {
			reversedSet[id] = true
		}
		for i := range rows {
			rows[i].IsReversed = reversedSet[rows[i].ID]
		}
	}
	return rows, total, summary, nil
}
