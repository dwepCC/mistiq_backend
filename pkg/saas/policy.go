package saas

import (
	"encoding/json"
	"errors"
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPaymentBlocked = errors.New("cuenta bloqueada: no puede enviar más comprobantes. Contacte a soporte o ventas")
	ErrProvisionalUsed = errors.New("ya se usó la reactivación provisional en este ciclo de facturación")
)

// ChargeReconnectionFee solo si el tenant fue suspendido por mora (no en gracia ni renovación anticipada).
func ChargeReconnectionFee(tenant *database.Tenant, sub *database.SaasSubscription) bool {
	if tenant == nil {
		return false
	}
	if tenant.Status == database.TenantStatusSuspended {
		return true
	}
	if sub != nil && sub.Status == database.SaasSubSuspended {
		return true
	}
	return false
}

// BillingCycleAmountDue monto a pagar por un ciclo (plan + reconexión solo si aplica).
func BillingCycleAmountDue(cycle *database.SaasBillingCycle, tenant *database.Tenant, sub *database.SaasSubscription) float64 {
	if cycle == nil {
		return 0
	}
	amt := cycle.Amount
	if ChargeReconnectionFee(tenant, sub) {
		amt += cycle.ReconnectionFee
	}
	return amt
}

// CanTenantSubmitPayment valida strikes y bloqueo.
func CanTenantSubmitPayment(tenant *database.Tenant) error {
	if tenant == nil {
		return errors.New("tenant no encontrado")
	}
	cfg, _ := LoadSettings()
	maxStrike := EffectiveStrikeMax(cfg)
	if tenant.Status == database.TenantStatusBlocked || tenant.PaymentBlocked || tenant.StrikeCount >= maxStrike {
		return ErrPaymentBlocked
	}
	return nil
}

// ApplyStrikeOnReject incrementa strike y aplica suspended/blocked.
func ApplyStrikeOnReject(tx *gorm.DB, tenantID uint, subID *uint, reviewerID *uint, reason string) (strikeCount int, blocked bool, err error) {
	var tenant database.Tenant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&tenant, tenantID).Error; err != nil {
		return 0, false, err
	}
	cfg, _ := LoadSettings()
	maxStrike := EffectiveStrikeMax(cfg)
	strikeCount = tenant.StrikeCount + 1
	updates := map[string]interface{}{"strike_count": strikeCount}
	blocked = false

	// Si la suscripción sigue dentro de su ventana natural por calendario (grace_period_days
	// configurado en la central), un comprobante rechazado no debe tumbarla — para eso existe
	// la tolerancia. El strike se cuenta igual (control de abuso: acumular varios rechazos
	// eventualmente bloquea, ver abajo), solo no se fuerza `suspended` mientras el calendario
	// todavía la sostenga. Fuera de gracia, el comportamiento es el de siempre: suspende.
	forceSuspend := true
	if subID != nil {
		var sub database.SaasSubscription
		if err := tx.First(&sub, *subID).Error; err == nil {
			daysAfter := CalendarDaysAfterEnd(sub.EndDate, NowLima())
			withinGrace := daysAfter <= 0 || (cfg.GracePeriodDays > 0 && daysAfter <= cfg.GracePeriodDays)
			forceSuspend = !withinGrace
		}
	}

	if strikeCount >= maxStrike {
		// Reincidencia (varios comprobantes inválidos): señal de abuso, no de mora. Bloquea
		// igual aunque el calendario todavía ayude — es una medida distinta a "está vencido".
		updates["status"] = database.TenantStatusBlocked
		updates["payment_blocked"] = true
		blocked = true
		if subID != nil {
			_ = tx.Model(&database.SaasSubscription{}).Where("id = ?", *subID).Updates(map[string]interface{}{
				"status": database.SaasSubSuspended, "provisional_until": nil,
			}).Error
		}
		LogEventTx(tx, tenantID, subID, EventTenantBlocked, "admin", reviewerID, reason,
			fmt.Sprintf(`{"strike_count":%d}`, strikeCount))
	} else if forceSuspend {
		updates["status"] = database.TenantStatusSuspended
		if subID != nil {
			_ = tx.Model(&database.SaasSubscription{}).Where("id = ?", *subID).Updates(map[string]interface{}{
				"status": database.SaasSubSuspended, "provisional_until": nil,
			}).Error
		}
		LogEventTx(tx, tenantID, subID, EventPaymentRejected, "admin", reviewerID, reason,
			fmt.Sprintf(`{"strike_count":%d}`, strikeCount))
	} else {
		// Dentro de gracia: no se toca tenant.status ni sub.status (el cálculo en vivo de
		// ResolveEffectiveStatus ya los resuelve como active/grace_period desde EndDate). Solo
		// se retira el provisional que este pago pudo haber otorgado — no aplica más, ya se
		// rechazó — para no dejarlo con una fecha de "provisional hasta" fantasma.
		if subID != nil {
			_ = tx.Model(&database.SaasSubscription{}).Where("id = ?", *subID).Update("provisional_until", nil).Error
		}
		LogEventTx(tx, tenantID, subID, EventPaymentRejected, "admin", reviewerID,
			reason+" (dentro de gracia: suscripción se mantiene activa)",
			fmt.Sprintf(`{"strike_count":%d}`, strikeCount))
	}
	if err := tx.Model(&tenant).Updates(updates).Error; err != nil {
		return 0, false, err
	}
	return strikeCount, blocked, nil
}

// ClearStrikesOnApprove resetea strikes al aprobar pago válido.
func ClearStrikesOnApprove(tx *gorm.DB, tenantID uint, subID *uint, reviewerID *uint) error {
	var tenant database.Tenant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&tenant, tenantID).Error; err != nil {
		return err
	}
	wasBlocked := tenant.Status == database.TenantStatusBlocked || tenant.PaymentBlocked
	updates := map[string]interface{}{
		"strike_count":     0,
		"payment_blocked":  false,
		"status":           database.TenantStatusActive,
	}
	if err := tx.Model(&tenant).Updates(updates).Error; err != nil {
		return err
	}
	if wasBlocked {
		LogEventTx(tx, tenantID, subID, EventTenantUnblocked, "admin", reviewerID, "pago aprobado", "")
	}
	return nil
}

// LogEventTx auditoría dentro de transacción.
func LogEventTx(tx *gorm.DB, tenantID uint, subID *uint, eventType, actorType string, actorID *uint, reason, meta string) {
	_ = tx.Create(&database.SaasSubscriptionEvent{
		TenantID: tenantID, SubscriptionID: subID, EventType: eventType,
		ActorType: actorType, ActorID: actorID, Reason: reason, MetadataJSON: meta,
	}).Error
}

// MetaJSON helper.
func MetaJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
