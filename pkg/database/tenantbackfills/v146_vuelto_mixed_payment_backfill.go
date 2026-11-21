package tenantbackfills

import (
	"log/slog"

	"gorm.io/gorm"

	"tukifac/pkg/logger"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
)

// NOTA DE VERSIÓN: este backfill nació registrado como versión 37 y NUNCA llegó a correr en
// producción con ese número — tenant_migration_history comparte el mismo espacio de versiones
// entre migraciones de esquema (tenantmigrations) y backfills (tenantbackfills), y la versión 37
// ya la tenía tomada V146StaffSchemaRepair (tenantmigrations) desde junio. El motor de selección
// de tenants pendientes trata "versión" como una sola progresión por tenant sin filtrar por
// `type`, así que cualquier tenant que ya tuviera esa fila de esquema (la mayoría, por ser vieja)
// quedaba marcado como "ya al día" y este backfill jamás se le intentaba — confirmado en
// producción: cero filas type='backfill' version=37 para los tenants afectados, ni siquiera un
// intento fallido. Renombrado a 146 (siguiente versión libre real, ver el máximo entre
// tenantmigrations y tenantbackfills) para evitar la colisión. Lección: el número de versión de
// un nuevo backfill se elige contra el máximo de AMBOS registros, nunca solo tenantbackfills.
//
// V146VueltoMixedPaymentBackfill corrige tenant_cash_movements/tenant_bank_movements (y el saldo
// cacheado de tenant_bank_accounts.balance que depende de ellos) de ventas con pago MIXTO
// (efectivo + electrónico) donde el vuelto se había repartido proporcionalmente entre TODOS los
// métodos en vez de salir 100% de efectivo (bug reportado 2026-09-25, corregido en
// pkg/money/payment_report.go::AllocateSalePaymentReportAmounts). tenant_sale_payments NUNCA se
// toca — es el registro histórico de lo realmente entregado por el cliente; lo que se corrige es
// el movimiento real que alimenta caja/banco y sus reportes (arqueo, reporte de caja, reporte de
// ventas, saldo de cuentas bancarias).
//
// tenant_bank_accounts.balance es un saldo CACHEADO (CashBankService.RecordPaymentToAccount hace
// `balance = balance + delta` en el momento del pago) — nunca se recalcula solo desde
// tenant_bank_movements, así que corregir el movimiento sin también corregir este saldo dejaría
// la cuenta (p.ej. "Billetera Yape") mostrando un total desalineado con su propio historial de
// movimientos. tenant_cash_sessions NO tiene un saldo equivalente cacheado mientras la sesión
// sigue abierta (el arqueo se calcula en vivo desde tenant_cash_movements en cada carga), así que
// el efectivo no necesita este segundo ajuste — confirmado en la validación contra datos reales
// de este backfill (ver informe): el arqueo se corrigió solo con el UPDATE de los movimientos.
//
// ALCANCE (por decisión del usuario, ver informe 2026-09-25) — solo corrige cuando las TRES
// condiciones se cumplen:
//  1. La venta tiene pago mixto (2+ métodos distintos, excluyendo el marcador "credito"), incluye
//     al menos una línea de efectivo, y la suma de pagos supera el total (hay vuelto real).
//  2. La sesión de caja de la venta sigue ABIERTA. Las de sesiones ya cerradas se dejan intactas
//     a propósito: ya se hizo (o no) el arqueo físico con el número que el sistema mostró en su
//     momento — recalcularlo ahora no cambia nada real, solo movería el número que se ve al mirar
//     atrás. Esto excluye de forma natural (sin lista aparte) los 2 casos anómalos encontrados en
//     el escaneo de producción (industrialrafaz, guillenbegazo — errores de digitación, no vuelto
//     real): sus ventas afectadas están TODAS en sesiones ya cerradas.
//  3. El vuelto no supera el efectivo disponible de la venta — si lo supera (mismo criterio de
//     anomalía que AllocateSalePaymentReportAmounts: no hay de dónde restarlo), se cuenta como
//     "requiere revisión manual" y NUNCA se corrige a ciegas.
//
// CORRELACIÓN pago → movimiento: tenant_cash_movements/tenant_bank_movements no tienen FK al
// TenantSalePayment que los generó (mismo problema documentado en
// V036SalePaymentCashSessionBackfill). Para no inventar una relación por orden de inserción (que
// puede no coincidir si algún pago no generó movimiento — p.ej. método sin cuenta bancaria
// configurada, ver CashBankService.RecordPaymentToAccount), se recalcula cuánto habría reportado
// la fórmula ANTERIOR (proporcional entre todas las líneas, ver v146LegacyAllocate) para cada
// línea de pago y se busca, entre los movimientos de esa venta y esa clase (cash/bank), exactamente
// UNO cuyo monto actual coincida (± centavos). 0 o 2+ candidatos → esa línea queda sin corregir y
// se cuenta en NeedsReview, igual que "ambiguo"/"sin candidato" en V036.
type V146VueltoMixedPaymentBackfill struct{}

func (V146VueltoMixedPaymentBackfill) Version() int { return 146 }
func (V146VueltoMixedPaymentBackfill) Name() string { return "vuelto_mixed_payment_backfill" }

func (V146VueltoMixedPaymentBackfill) Description() string {
	return "Corrige tenant_cash_movements/tenant_bank_movements de ventas con pago mixto " +
		"(efectivo + electrónico) donde el vuelto se prorrateaba entre todos los métodos en vez " +
		"de salir 100% de efectivo. Solo sesiones de caja ABIERTAS; nunca toca tenant_sale_payments " +
		"ni una venta donde el vuelto supere el efectivo disponible."
}

// V146BackfillResult contadores de una pasada — los usan tanto Run como Diagnose.
type V146BackfillResult struct {
	SalesAnalyzed    int
	MovementsFixed   int
	SalesNeedsReview int
	Errors           int
}

func (b V146VueltoMixedPaymentBackfill) Diagnose(db *gorm.DB) (V146BackfillResult, error) {
	result, updates, err := v146Analyze(db)
	// MovementsFixed en v146Analyze cuenta lo que HAY que corregir (independientemente de si se
	// llega a escribir o no) — Diagnose nunca escribe, así que reporta directamente len(updates);
	// Run reutiliza el mismo resultado y solo descuenta lo que falle al aplicar el UPDATE real.
	result.MovementsFixed = len(updates)
	return result, err
}

func (b V146VueltoMixedPaymentBackfill) Run(db *gorm.DB) error {
	result, updates, err := v146Analyze(db)
	if err != nil {
		return err
	}
	for _, u := range updates {
		table := "tenant_bank_movements"
		if u.IsCash {
			table = "tenant_cash_movements"
		}
		upd := db.Table(table).
			Where("id = ? AND amount = ?", u.MovementID, u.OldAmount).
			Update("amount", u.NewAmount)
		if upd.Error != nil {
			result.Errors++
			continue
		}
		if upd.RowsAffected == 0 {
			// El monto ya cambió desde que Analyze lo leyó (otra corrida concurrente, o ya
			// corregido) — no reintentar a ciegas sobre un valor que ya no es el esperado.
			continue
		}
		// tenant_bank_accounts.balance es un saldo cacheado que CashBankService.
		// RecordPaymentToAccount incrementa en el momento del pago (balance + delta) — nunca se
		// recalcula solo desde tenant_bank_movements, así que hay que corregirlo con el mismo
		// delta que este UPDATE le está aplicando al movimiento. tenant_cash_sessions no tiene
		// un saldo equivalente cacheado mientras la sesión sigue abierta (se calcula en vivo
		// desde tenant_cash_movements), así que el efectivo no necesita este segundo ajuste.
		if !u.IsCash && u.BankAccountID != 0 {
			delta := money.RoundDisplay(u.NewAmount - u.OldAmount)
			if err := db.Table("tenant_bank_accounts").
				Where("id = ?", u.BankAccountID).
				Update("balance", gorm.Expr("balance + ?", delta)).Error; err != nil {
				result.Errors++
				continue
			}
		}
		result.MovementsFixed++
	}
	v146LogResult(result)
	return nil
}

type v146SaleRow struct {
	ID    uint    `gorm:"column:id"`
	Total float64 `gorm:"column:total"`
}

type v146PaymentRow struct {
	ID     uint    `gorm:"column:id"`
	Method string  `gorm:"column:method"`
	Amount float64 `gorm:"column:amount"`
}

type v146MovementRow struct {
	ID            uint    `gorm:"column:id"`
	Amount        float64 `gorm:"column:amount"`
	BankAccountID uint    `gorm:"column:bank_account_id"`
}

type v146MovementUpdate struct {
	MovementID    uint
	IsCash        bool
	OldAmount     float64
	NewAmount     float64
	BankAccountID uint // solo relevante cuando !IsCash — ver ajuste de saldo cacheado en Run
}

// v146Analyze hace TODO el análisis de solo lectura (Diagnose y Run comparten esta función; Run
// solo agrega los UPDATE). Nunca escribe.
func v146Analyze(db *gorm.DB) (V146BackfillResult, []v146MovementUpdate, error) {
	mig := db.Migrator()
	if !mig.HasTable("tenant_sale_payments") || !mig.HasTable("tenant_cash_sessions") {
		return V146BackfillResult{}, nil, nil
	}

	var sales []v146SaleRow
	if err := db.Table("tenant_sales ts").
		Select(`
			ts.id AS id, ts.total AS total
		`).
		Joins("JOIN tenant_sale_payments tsp ON tsp.sale_id = ts.id").
		Joins("JOIN tenant_cash_sessions cs ON cs.id = ts.cash_session_id").
		Where("cs.status = ?", "open").
		Where("tsp.method != ?", paymentcondition.CodeCredit).
		Group("ts.id, ts.total").
		Having("COUNT(DISTINCT tsp.method) > 1").
		Having("SUM(CASE WHEN LOWER(TRIM(tsp.method)) IN ('cash','efectivo') THEN 1 ELSE 0 END) > 0").
		Having("SUM(tsp.amount) > ts.total + 0.01").
		Find(&sales).Error; err != nil {
		return V146BackfillResult{}, nil, err
	}

	result := V146BackfillResult{SalesAnalyzed: len(sales)}
	var updates []v146MovementUpdate

	for _, sale := range sales {
		var payments []v146PaymentRow
		if err := db.Table("tenant_sale_payments").
			Select("id, method, amount").
			Where("sale_id = ? AND method != ?", sale.ID, paymentcondition.CodeCredit).
			Order("id ASC").
			Find(&payments).Error; err != nil {
			result.Errors++
			continue
		}

		lines := make([]money.SalePaymentLine, 0, len(payments))
		for _, p := range payments {
			lines = append(lines, money.SalePaymentLine{ID: p.ID, Amount: p.Amount, IsCash: money.IsCashMethod(p.Method)})
		}
		var cashAvailable float64
		for _, l := range lines {
			if l.IsCash {
				cashAvailable += l.Amount
			}
		}
		var totalPaid float64
		for _, l := range lines {
			totalPaid += l.Amount
		}
		change := totalPaid - sale.Total
		if change > cashAvailable+PaymentToleranceV146 {
			// El vuelto supera el efectivo disponible (anómalo) — nunca se corrige a ciegas.
			result.SalesNeedsReview++
			continue
		}

		newAmounts := money.AllocateSalePaymentReportAmounts(sale.Total, lines)
		legacyAmounts := v146LegacyAllocate(sale.Total, lines)

		var cashMoves, bankMoves []v146MovementRow
		if err := db.Table("tenant_cash_movements").
			Select("id, amount").
			Where("sale_id = ? AND type = ?", sale.ID, "income").
			Find(&cashMoves).Error; err != nil {
			result.Errors++
			continue
		}
		if err := db.Table("tenant_bank_movements").
			Select("id, amount, bank_account_id").
			Where("sale_id = ? AND type = ?", sale.ID, "credit").
			Find(&bankMoves).Error; err != nil {
			result.Errors++
			continue
		}

		// Claves compuestas (clase, id): tenant_cash_movements y tenant_bank_movements son tablas
		// independientes, cada una con su propio autoincremento — un mismo valor numérico de ID
		// puede existir en ambas sin relación, así que no se puede usar el ID crudo como clave.
		type claimKey struct {
			isCash bool
			id     uint
		}
		claimed := make(map[claimKey]bool, len(cashMoves)+len(bankMoves))
		var saleUpdates []v146MovementUpdate
		for _, p := range payments {
			isCash := money.IsCashMethod(p.Method)
			candidates := bankMoves
			if isCash {
				candidates = cashMoves
			}
			legacy := money.RoundDisplay(legacyAmounts[p.ID])
			var match v146MovementRow
			matches := 0
			for _, m := range candidates {
				key := claimKey{isCash: isCash, id: m.ID}
				if claimed[key] {
					continue
				}
				if money.RoundDisplay(m.Amount) == legacy {
					match = m
					matches++
				}
			}
			if matches != 1 {
				// 0 candidatos (p.ej. método sin cuenta bancaria configurada, nunca generó
				// movimiento) o 2+ candidatos ambiguos — esta línea no se corrige.
				continue
			}
			claimed[claimKey{isCash: isCash, id: match.ID}] = true
			newAmt := money.RoundDisplay(newAmounts[p.ID])
			if newAmt == legacy {
				continue // ya coincide (nada que corregir), no cuenta como fix
			}
			saleUpdates = append(saleUpdates, v146MovementUpdate{
				MovementID: match.ID, IsCash: isCash, OldAmount: legacy, NewAmount: newAmt,
				BankAccountID: match.BankAccountID,
			})
		}
		updates = append(updates, saleUpdates...)
	}

	return result, updates, nil
}

// PaymentToleranceV146 mismo margen que money.PaymentTolerance, repetido acá para no acoplar el
// backfill a cambios futuros de esa constante interna.
const PaymentToleranceV146 = 0.01

// v146LegacyAllocate replica la fórmula ANTERIOR al fix (prorrateo proporcional entre TODAS las
// líneas, sin distinguir efectivo) — solo para correlacionar qué movimiento histórico generó cada
// línea de pago (ver comentario del tipo). No se usa para nada más; la fórmula vigente es
// money.AllocateSalePaymentReportAmounts.
func v146LegacyAllocate(saleTotal float64, payments []money.SalePaymentLine) map[uint]float64 {
	out := make(map[uint]float64, len(payments))
	if len(payments) == 0 {
		return out
	}
	payable := money.RoundDisplay(saleTotal)
	if payable < 0 {
		payable = 0
	}
	var sum float64
	for _, p := range payments {
		amt := money.RoundDisplay(p.Amount)
		if amt > 0 {
			sum += amt
		}
	}
	if sum <= payable+PaymentToleranceV146 {
		for _, p := range payments {
			out[p.ID] = money.RoundDisplay(p.Amount)
		}
		return out
	}
	if sum <= 0 {
		return out
	}
	var allocated float64
	for i, p := range payments {
		amt := money.RoundDisplay(p.Amount)
		if amt <= 0 {
			out[p.ID] = 0
			continue
		}
		var reportAmt float64
		if i == len(payments)-1 {
			reportAmt = money.RoundDisplay(payable - allocated)
			if reportAmt < 0 {
				reportAmt = 0
			}
		} else {
			reportAmt = money.RoundDisplay(payable * (amt / sum))
			allocated += reportAmt
		}
		out[p.ID] = reportAmt
	}
	return out
}

func v146LogResult(r V146BackfillResult) {
	if logger.L == nil {
		return
	}
	logger.L.Info("vuelto_mixed_payment_backfill_result",
		slog.Int("sales_analyzed", r.SalesAnalyzed),
		slog.Int("movements_fixed", r.MovementsFixed),
		slog.Int("sales_needs_review", r.SalesNeedsReview),
		slog.Int("errors", r.Errors),
	)
}
