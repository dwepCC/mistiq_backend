package service

import (
	"sort"
	"strings"

	"tukifac/pkg/database"
)

// MethodBalance saldo de un método de pago dentro de una sesión: cuánto entró, cuánto salió y el
// neto. IsCash marca el único método que representa dinero físico (mismo criterio que
// IsCashPaymentMethod, reusado tal cual — no se inventa un segundo criterio).
type MethodBalance struct {
	Method  string  `json:"method"`
	Label   string  `json:"label"`
	IsCash  bool    `json:"is_cash"`
	Income  float64 `json:"income"`
	Expense float64 `json:"expense"`
	Net     float64 `json:"net"`
}

// SessionBalanceSummary fuente única de "cuánto dinero se movió en esta sesión, por método".
// La consumen tanto la pantalla en vivo (CashPage.tsx) como el cierre/arqueo — nadie más vuelve
// a sumar movimientos por su cuenta.
//
// Total = saldo de la sesión con TODOS los métodos (lo que antes calculaba mal el frontend
// sumando cash_movements sin filtrar). CashExpected = idéntico a getExpectedBalance (el criterio
// de arqueo, exclusivamente efectivo) — se reusa la función existente sin tocarla, para que este
// resumen y el cierre de caja sean, por construcción, el mismo número leído dos veces.
type SessionBalanceSummary struct {
	ByMethod     []MethodBalance `json:"by_method"`
	Total        float64         `json:"total"`
	CashExpected float64         `json:"cash_expected"`
}

// GetSessionBalanceSummary arma el desglose por método de tenant_cash_movements (siempre ligados
// a la sesión) + tenant_bank_movements con cash_session_id poblado (Fase 2: pagos Yape/Plin/
// transferencia/tarjeta de ventas, compras e ingresos/egresos manuales). Movimientos bancarios
// de sesiones anteriores a la Fase 2 (cash_session_id nulo) no aparecen aquí — dato histórico
// que ya se veía por cuenta bancaria en la pantalla de Bancos, sin regresión respecto a hoy.
func (s *CashBankService) GetSessionBalanceSummary(sessionID uint) (*SessionBalanceSummary, error) {
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		return nil, err
	}

	totals := map[string]*MethodBalance{}
	order := make([]string, 0, 6)
	methodNames := s.paymentMethodDisplayNames()

	ensure := func(rawMethod string) *MethodBalance {
		method := normalizeReportMethod(rawMethod)
		mb, ok := totals[method]
		if !ok {
			mb = &MethodBalance{Method: method, Label: methodLabel(method, methodNames), IsCash: method == "efectivo"}
			totals[method] = mb
			order = append(order, method)
		}
		return mb
	}
	// El efectivo siempre aparece, aunque no haya habido ni un movimiento — es la apertura.
	ensure("efectivo")

	var cashMovs []database.TenantCashMovement
	s.db.Where("cash_session_id = ? AND type IN ?", sessionID, []string{"income", "expense"}).Find(&cashMovs)
	for _, m := range cashMovs {
		mb := ensure(m.PaymentMethod)
		if m.Type == "income" {
			mb.Income += m.Amount
		} else {
			mb.Expense += m.Amount
		}
	}

	// TODOS los movimientos bancarios de la sesión: los ligados a venta/compra (sale_id/
	// purchase_id no nulos) y los MANUALES (AddMovement, ambos nulos). Antes un manual por
	// método no efectivo generaba DOS filas (TenantCashMovement + TenantBankMovement) y aquí se
	// excluía a propósito el bancario para no contarlo dos veces junto con cashMovs de arriba.
	// AddMovement ya no duplica: un manual vive en EXACTAMENTE una de las dos tablas según su
	// método, así que ambas consultas (cashMovs arriba, bankMovs aquí) son necesariamente
	// disjuntas por construcción y no hay riesgo de doble conteo.
	var bankMovs []database.TenantBankMovement
	s.db.Where("cash_session_id = ?", sessionID).Find(&bankMovs)
	if len(bankMovs) > 0 {
		methodByAccount := s.paymentMethodCodesByBankAccount(bankMovs)
		for _, m := range bankMovs {
			code := methodByAccount[m.BankAccountID]
			if code == "" {
				// Cuenta sin método de pago vinculado (ni por FK ni por el texto legado): no
				// hay forma de saber cuál es, pero el movimiento sigue perteneciendo a la
				// sesión — se agrupa aparte en vez de colar como "efectivo" (normalizeReportMethod
				// trata "" como efectivo, criterio pensado para cash_movements sin dato, no para
				// este caso).
				code = "otros"
			}
			mb := ensure(code)
			if m.Type == "credit" {
				mb.Income += m.Amount
			} else {
				mb.Expense += m.Amount
			}
		}
	}

	// "efectivo" primero (es el que requiere arqueo), el resto alfabético.
	sort.Slice(order, func(i, j int) bool {
		if order[i] == "efectivo" {
			return true
		}
		if order[j] == "efectivo" {
			return false
		}
		return order[i] < order[j]
	})

	out := &SessionBalanceSummary{CashExpected: s.getExpectedBalance(sessionID)}
	for _, method := range order {
		mb := totals[method]
		// Income/Expense quedan como la suma pura de movimientos (útil para mostrar "Ingresos"/
		// "Egresos" de la sesión sin ensuciarlos con la apertura). La apertura de caja solo
		// existe para efectivo y solo afecta el Net — igual que getExpectedBalance.
		mb.Net = mb.Income - mb.Expense
		if method == "efectivo" {
			mb.Net += session.OpeningBalance
		}
		out.ByMethod = append(out.ByMethod, *mb)
		out.Total += mb.Net
	}
	return out, nil
}

// paymentMethodDisplayNames mapa método normalizado → nombre configurado (tenant_payment_methods.Name).
func (s *CashBankService) paymentMethodDisplayNames() map[string]string {
	var methods []database.TenantPaymentMethod
	s.db.Find(&methods)
	out := make(map[string]string, len(methods))
	for _, pm := range methods {
		out[normalizeReportMethod(pm.Code)] = pm.Name
	}
	return out
}

// paymentMethodCodesByBankAccount resuelve, para las cuentas involucradas en bankMovs, el código
// de método de pago que corresponde mostrar — prioriza el método configurado con esa cuenta como
// destino (tenant_payment_methods.bank_account_id, la vía "buena"), y si ninguno apunta ahí, cae
// al texto legado de la propia cuenta (tenant_bank_accounts.payment_method).
func (s *CashBankService) paymentMethodCodesByBankAccount(movs []database.TenantBankMovement) map[uint]string {
	accountIDs := make(map[uint]struct{}, len(movs))
	for _, m := range movs {
		accountIDs[m.BankAccountID] = struct{}{}
	}
	ids := make([]uint, 0, len(accountIDs))
	for id := range accountIDs {
		ids = append(ids, id)
	}
	result := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return result
	}

	var methods []database.TenantPaymentMethod
	s.db.Where("bank_account_id IN ? AND active = ?", ids, true).Find(&methods)
	for _, pm := range methods {
		if pm.BankAccountID != nil {
			if _, exists := result[*pm.BankAccountID]; !exists {
				result[*pm.BankAccountID] = pm.Code
			}
		}
	}

	var missing []uint
	for _, id := range ids {
		if _, ok := result[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		var accounts []database.TenantBankAccount
		s.db.Where("id IN ?", missing).Find(&accounts)
		for _, acc := range accounts {
			if acc.PaymentMethod != "" {
				result[acc.ID] = acc.PaymentMethod
			}
		}
	}
	return result
}

func methodLabel(method string, names map[string]string) string {
	if label, ok := names[method]; ok && label != "" {
		return label
	}
	if method == "" {
		return "Otros"
	}
	return strings.ToUpper(method[:1]) + method[1:]
}
