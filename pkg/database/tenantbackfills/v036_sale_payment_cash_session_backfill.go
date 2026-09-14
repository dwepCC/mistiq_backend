package tenantbackfills

import (
	"fmt"
	"log/slog"
	"math"
	"strings"

	"gorm.io/gorm"

	"tukifac/pkg/logger"
)

// v036AmountTolerance margen para comparar montos (redondeo de centavos, mismo criterio que el
// resto del sistema usa para comparar saldos monetarios).
const v036AmountTolerance = 0.01

// V036SalePaymentCashSessionBackfill completa tenant_sale_payments.cash_session_id (agregado por
// V123SalePaymentSessionTraceability) en los pagos históricos anteriores a esa columna, cuando la
// relación con su movimiento financiero es INEQUÍVOCA.
//
// CONTEXTO (P0 — separación documento/pago/movimiento, §13/§14 de la especificación): antes de
// V123, un TenantSalePayment no registraba en qué Caja ocurrió — esa información solo existía,
// indirectamente, en el TenantCashMovement o TenantBankMovement que ese pago generó. Los pagos
// nuevos (posteriores a V123) ya nacen con cash_session_id poblado por sale_service/
// receivable_service; este backfill es exclusivamente para el histórico previo, y NUNCA toca
// TenantSale.CashSessionID (eso lo prohíbe expresamente la especificación: no hay evidencia para
// reconstruir la Caja de REGISTRO de ventas antiguas ya sobrescritas por Collect, e inventarla
// sería peor que dejarla como está).
//
// ALGORITMO DE CORRELACIÓN — por cada pago con cash_session_id NULL:
//
//  1. Se agrupa con cualquier OTRO pago pendiente que comparta EXACTAMENTE (sale_id, clase de
//     método, monto). "Clase de método" es solo cash vs. no-cash (ver v036IsCashMethod) — el mismo
//     criterio simple que ya usa ResolveCashSessionForPayments para decidir si un pago necesita
//     Caja; no se reutiliza el catálogo TenantPaymentMethod para no depender de que la
//     configuración actual (p.ej. a qué cuenta bancaria apunta "yape") sea la misma que existía
//     cuando se registró un pago histórico.
//  2. Si ese grupo tiene MÁS DE UN pago pendiente, ninguno se resuelve: aunque hubiera igual
//     cantidad de movimientos candidatos, no hay forma de saber CUÁL pago corresponde a CUÁL
//     movimiento sin inventar un criterio de orden (p.ej. por fecha de creación) — eso sería
//     exactamente la asociación arbitraria que la especificación prohíbe. Se cuentan como
//     "ambiguos".
//  3. Si el grupo tiene exactamente un pago, se buscan candidatos en la tabla que corresponde a su
//     clase: TenantCashMovement (type = income) para efectivo, TenantBankMovement (type = credit)
//     para cualquier otro método — filtrando por el mismo sale_id (vínculo tipado, no por
//     Reference/texto libre: ver nota más abajo) y el mismo monto (± v036AmountTolerance).
//  4. Exactamente 1 candidato → se asigna su cash_session_id al pago. 0 candidatos → "sin
//     candidato". 2+ candidatos → "ambiguo" (mismo motivo que el punto 2: no hay forma de saber
//     cuál de los movimientos generó este pago en particular).
//
// QUÉ NO SE HACE (y por qué no es posible hacerlo de forma inequívoca):
//
//   - No se usa TenantBankMovement/TenantCashMovement.Reference como alternativa cuando SaleID es
//     NULL. Reference es texto libre heredado (ver comentario del campo en pkg/database/
//     migrations.go): no tiene un formato garantizado igual a tenant_sales.number en todo el
//     histórico, y los movimientos manuales también lo usan con texto arbitrario. Igualarlo por
//     string sería inventar una relación, no confirmarla — esos pagos quedan "sin candidato".
//   - TenantBankMovement no guarda el sub-método original (yape/plin/tarjeta/transferencia): solo
//     type=credit/debit. La única cuenta bancaria (BankAccountID) podría cruzarse contra
//     TenantPaymentMethod.BankAccountID, pero esa tabla refleja la configuración ACTUAL de a qué
//     cuenta apunta cada método, no la que regía cuando el pago histórico se registró (un método
//     puede haberse reconfigurado a otra cuenta desde entonces) — usarla agregaría precisión
//     aparente sin evidencia real, así que la clase (cash / no-cash) es la única compatibilidad de
//     método que este backfill valida.
//
// NO DESTRUCTIVO / IDEMPOTENTE: solo hace UPDATE de tenant_sale_payments.cash_session_id, y solo
// en filas que siguen en NULL (la propia condición WHERE de cada UPDATE lo reafirma). Nunca
// modifica TenantCashMovement/TenantBankMovement ni TenantSale. Correrlo de nuevo no encuentra
// nada pendiente en los pagos ya resueltos (siguen bloqueados por el propio WHERE cash_session_id
// IS NULL) y reevalúa igual — con el mismo resultado — los que quedaron ambiguos o sin candidato,
// a menos que aparezcan movimientos nuevos que los conviertan en resolubles.
//
// MODO DIAGNÓSTICO: Diagnose corre EXACTAMENTE el mismo análisis de correlación que Run pero sin
// escribir nada — pensado para inspeccionar, tenant por tenant y de solo lectura, cuánto resolvería
// el backfill real antes de decidir ejecutarlo (mismo espíritu que auditTenantCodes en
// pkg/cmd/backfill_product_codes.go para V034ProductCodes). Run reutiliza el mismo análisis y solo
// agrega la escritura.
//
// INFORME: al no poder este backfill devolver un resultado tipado desde Run (la interfaz
// TenantBackfill solo expone Run(db) error, igual que el resto de este paquete, y así se invoca
// desde el panel central / cron), el resumen de esa ejecución real —analizados, resueltos,
// ambiguos, sin candidato, errores— se reporta con el mismo logger estructurado que ya usa
// pkg/database/engine/backfill.go para el ciclo de vida de cada backfill (tenant_backfill_start/
// success/failed): una línea "tenant_sale_payment_cash_session_backfill_result" por tenant. Diagnose,
// en cambio, si devuelve el resultado tipado directamente a quien lo invoque (pensado para un CLI
// de solo lectura, no para el panel/cron).
type V036SalePaymentCashSessionBackfill struct{}

func (V036SalePaymentCashSessionBackfill) Version() int { return 36 }
func (V036SalePaymentCashSessionBackfill) Name() string {
	return "sale_payment_cash_session_backfill"
}

// Repeatable: ver tenantbackfills.Repeatable — este backfill depende de que otras tablas/columnas
// (V123, tenant_cash_movements, tenant_bank_movements) ya existan para ese tenant, y esas se
// despliegan de forma incremental/asíncrona por el cron de migración de schema. Si se le aplicara
// el candado run-once genérico, una corrida que "tiene éxito" (sin error) mientras ese schema
// todavía está a medio desplegar quedaría bloqueada para siempre con un resultado parcial, sin
// reintento posible — justo el bug encontrado en producción el 2026-09-06 (~8300 pagos
// resolubles en ~190 tenants nunca se llegaron a escribir). Su propio WHERE cash_session_id IS
// NULL (ver v036Analyze) ya lo hace seguro de re-ejecutar sin costo ni riesgo cuando no hay nada
// pendiente.
func (V036SalePaymentCashSessionBackfill) Repeatable() bool { return true }

func (V036SalePaymentCashSessionBackfill) Description() string {
	return "Completa tenant_sale_payments.cash_session_id en pagos históricos anteriores a la " +
		"columna (V123), únicamente cuando se puede relacionar de forma inequívoca con el " +
		"TenantCashMovement/TenantBankMovement que generó (mismo sale_id, mismo monto, misma " +
		"clase efectivo/no-efectivo). Los pagos sin una relación 1 a 1 clara quedan en NULL y se " +
		"reportan como históricos ambiguos o sin candidato; nunca inventa una Caja."
}

// SalePaymentCashSessionBackfillResult contadores del análisis de una pasada — la usan tanto Run
// (tras aplicar los UPDATE) como Diagnose (modo preview, sin escribir).
type SalePaymentCashSessionBackfillResult struct {
	Analyzed    int
	Resolved    int
	Ambiguous   int
	NoCandidate int
	Errors      int
}

// Diagnose analiza los pagos con cash_session_id NULL y reporta cuántos resolvería el backfill
// real, SIN escribir nada — modo diagnóstico/preview (§8-11 de la especificación P0): para
// inspeccionar de forma segura y de solo lectura antes de decidir ejecutar el backfill sobre
// datos de producción.
func (b V036SalePaymentCashSessionBackfill) Diagnose(db *gorm.DB) (SalePaymentCashSessionBackfillResult, error) {
	result, _, err := v036Analyze(db)
	return result, err
}

func (b V036SalePaymentCashSessionBackfill) Run(db *gorm.DB) error {
	result, resolutions, err := v036Analyze(db)
	if err != nil {
		return err
	}
	for _, r := range resolutions {
		upd := db.Table("tenant_sale_payments").
			Where("id = ? AND cash_session_id IS NULL", r.PaymentID).
			Update("cash_session_id", r.CashSessionID)
		if upd.Error != nil {
			// La pasada de análisis ya lo había contado como "resuelto" (ver v036Analyze); si el
			// UPDATE en sí falla, se recategoriza como error en vez de reportar un resultado falso.
			result.Resolved--
			result.Errors++
			continue
		}
	}
	v036LogResult(result)
	return nil
}

// v036UnresolvedPayment fila de tenant_sale_payments con cash_session_id NULL.
type v036UnresolvedPayment struct {
	ID     uint
	SaleID uint
	Method string
	Amount float64
}

// v036MovementCandidate fila candidata de tenant_cash_movements o tenant_bank_movements.
type v036MovementCandidate struct {
	ID            uint
	CashSessionID *uint
}

// v036Resolution asignación que Run aplicaría (o que Diagnose solo reporta) para un pago puntual.
type v036Resolution struct {
	PaymentID     uint
	CashSessionID uint
}

// v036BucketKey agrupa pagos pendientes por la mínima combinación que permite distinguir uno de
// otro sin más contexto que el propio pago: misma venta, misma clase de método, mismo monto.
type v036BucketKey struct {
	SaleID uint
	Cash   bool
	Amount float64
}

// v036Analyze ejecuta el algoritmo de correlación completo (documentado en el comentario del tipo)
// de forma puramente de LECTURA: nunca escribe. Devuelve el resumen de contadores y, por separado,
// la lista de asignaciones que resultaron inequívocas — Run las aplica; Diagnose las descarta y
// solo informa el resumen.
func v036Analyze(db *gorm.DB) (SalePaymentCashSessionBackfillResult, []v036Resolution, error) {
	mig := db.Migrator()
	if !mig.HasTable("tenant_sale_payments") {
		return SalePaymentCashSessionBackfillResult{}, nil, nil
	}

	var pending []v036UnresolvedPayment
	if err := db.Table("tenant_sale_payments").
		Select("id, sale_id, method, amount").
		Where("cash_session_id IS NULL").
		Find(&pending).Error; err != nil {
		return SalePaymentCashSessionBackfillResult{}, nil, fmt.Errorf("listar pagos sin cash_session_id: %w", err)
	}

	result := SalePaymentCashSessionBackfillResult{Analyzed: len(pending)}
	if len(pending) == 0 {
		return result, nil, nil
	}

	hasCash := mig.HasTable("tenant_cash_movements")
	hasBank := mig.HasTable("tenant_bank_movements")

	buckets := make(map[v036BucketKey][]v036UnresolvedPayment, len(pending))
	for _, p := range pending {
		key := v036BucketKey{
			SaleID: p.SaleID,
			Cash:   v036IsCashMethod(p.Method),
			Amount: v036RoundAmount(p.Amount),
		}
		buckets[key] = append(buckets[key], p)
	}

	var resolutions []v036Resolution
	for key, group := range buckets {
		if len(group) != 1 {
			// Más de un pago pendiente con el mismo (sale_id, clase, monto): no hay evidencia
			// para saber cuál corresponde a cuál movimiento. Ver punto 2 del algoritmo arriba.
			result.Ambiguous += len(group)
			continue
		}
		payment := group[0]

		var candidates []v036MovementCandidate
		var err error
		switch {
		case key.Cash && hasCash:
			err = db.Table("tenant_cash_movements").
				Select("id, cash_session_id").
				Where("sale_id = ? AND type = ? AND ABS(amount - ?) < ?",
					key.SaleID, "income", key.Amount, v036AmountTolerance).
				Find(&candidates).Error
		case !key.Cash && hasBank:
			err = db.Table("tenant_bank_movements").
				Select("id, cash_session_id").
				Where("sale_id = ? AND type = ? AND ABS(amount - ?) < ?",
					key.SaleID, "credit", key.Amount, v036AmountTolerance).
				Find(&candidates).Error
		default:
			// La tabla de movimientos de esta clase ni siquiera existe en este tenant.
			result.NoCandidate++
			continue
		}
		if err != nil {
			result.Errors++
			continue
		}

		switch len(candidates) {
		case 0:
			result.NoCandidate++
		case 1:
			cand := candidates[0]
			if cand.CashSessionID == nil || *cand.CashSessionID == 0 {
				// El propio movimiento candidato no tiene sesión que propagar (dato incompleto
				// también en el origen) — no hay nada seguro que asignar.
				result.NoCandidate++
				continue
			}
			result.Resolved++
			resolutions = append(resolutions, v036Resolution{PaymentID: payment.ID, CashSessionID: *cand.CashSessionID})
		default:
			// 2+ candidatos para el mismo (sale_id, clase, monto): mismo motivo que el punto 2,
			// aplicado del lado de los movimientos en vez de los pagos.
			result.Ambiguous++
		}
	}

	return result, resolutions, nil
}

// v036IsCashMethod mismo criterio simple ya usado como fallback en
// CashBankService.ResolveCashSessionForPayments — no depende del catálogo TenantPaymentMethod
// (ver nota de por qué, arriba) para no asumir que la configuración actual regía en el histórico.
func v036IsCashMethod(method string) bool {
	m := strings.ToLower(strings.TrimSpace(method))
	return m == "cash" || m == "efectivo"
}

func v036RoundAmount(amount float64) float64 {
	return math.Round(amount*100) / 100
}

func v036LogResult(r SalePaymentCashSessionBackfillResult) {
	if logger.L == nil {
		return
	}
	logger.L.Info("tenant_sale_payment_cash_session_backfill_result",
		slog.Int("analyzed", r.Analyzed),
		slog.Int("resolved", r.Resolved),
		slog.Int("ambiguous", r.Ambiguous),
		slog.Int("no_candidate", r.NoCandidate),
		slog.Int("errors", r.Errors),
	)
}
