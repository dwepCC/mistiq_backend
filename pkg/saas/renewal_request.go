package saas

import (
	"errors"
	"fmt"
	"time"

	"tukifac/pkg/database"
)

// SubmitRenewalRequestInput — el tenant elige un plan y, opcionalmente, adjunta comprobante en
// el mismo paso. A diferencia de SubmitPayment (que exige un billing_cycle_id ya emitido por el
// admin/cron), esta solicitud no necesita que exista ningún cobro pendiente: cubre tanto al
// tenant que nunca tuvo suscripción como al que la tiene vencida y quiere elegir otra.
type SubmitRenewalRequestInput struct {
	TenantID uint
	PlanID   uint
	// PeriodMonths: cuántos meses está pagando/pidiendo el tenant. <=0 se corrige a 1 — igual
	// de importante para renovaciones normales que para pagos adelantados (un tenant con
	// suscripción todavía activa que ya quiere pagar 3 meses más, sin esperar a vencer).
	PeriodMonths  int
	Amount        float64
	PaymentMethod string
	PaymentDate   *time.Time
	Reference     string
	// ReceiptURL opcional: el tenant puede pedir el plan sin comprobante todavía (queda
	// pending_review sin acceso provisional) o adjuntarlo de una vez (mismo trato que un pago
	// normal: provisional si la suscripción actual está suspendida/vencida, ver SubmitPayment).
	ReceiptURL  string
	Notes       string
	SubmittedBy *uint
}

// SubmitRenewalRequest valida el plan y delega en SubmitPayment (misma transacción, mismas
// reglas de strikes/bloqueo vía CanTenantSubmitPayment): un solo camino de escritura para pagos,
// evita que esta ruta nueva diverja en el futuro de las reglas de la ruta existente.
//
// El monto SIEMPRE se calcula acá, nunca se confía en in.Amount — antes, como el frontend
// siempre mandaba un amount > 0, era en la práctica el cliente quien fijaba el precio final
// (un request armado a mano podía mandar cualquier monto). PeriodMonths además debe ser uno de
// los 4 ciclos fijos habilitados del plan (ver saas.FixedPlanCycleMonths): el "ciclo libre" (2,
// 5, cualquier cantidad de meses) sigue existiendo, pero solo desde el panel central
// (SubscriptionService.Create / ExtendSubscription), no por este camino de autoservicio.
func SubmitRenewalRequest(in SubmitRenewalRequestInput) (*database.SaasPayment, error) {
	if in.TenantID == 0 {
		return nil, errors.New("tenant_id requerido")
	}
	if in.PlanID == 0 {
		return nil, errors.New("plan_id requerido")
	}
	var plan database.SaasPlan
	if err := database.CentralDB.Where("id = ? AND active = ?", in.PlanID, true).First(&plan).Error; err != nil {
		return nil, errors.New("plan no encontrado o inactivo")
	}
	months := in.PeriodMonths
	if months <= 0 {
		months = 1
	}
	views := BuildPlanCycleViews(plan, LoadPlanCycles(plan.ID))
	cycle := FindEnabledPlanCycle(views, months)
	if cycle == nil {
		return nil, fmt.Errorf("ciclo de %d meses no disponible para este plan", months)
	}
	return SubmitPayment(SubmitPaymentInput{
		TenantID:      in.TenantID,
		Amount:        cycle.NetAmount,
		PeriodMonths:  months,
		PaymentMethod: in.PaymentMethod,
		PaymentDate:   in.PaymentDate,
		Reference:     in.Reference,
		ReceiptURL:    in.ReceiptURL,
		Notes:         in.Notes,
		SubmittedBy:   in.SubmittedBy,
		PlanID:        in.PlanID,
	})
}
