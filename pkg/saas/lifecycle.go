package saas

import (
	"fmt"
	"log/slog"

	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/saas/docusage"
)

// RunHourlyJobs recordatorios y cola de notificaciones (no cambia bloqueo por timestamp).
func RunHourlyJobs() (reminders int, notifications int) {
	if database.CentralDB == nil || !database.IsCentralSchemaReady() {
		return 0, 0
	}
	cfg, _ := LoadSettings()
	now := NowLima()

	var subs []database.SaasSubscription
	database.CentralDB.Where("status NOT IN ?", []string{
		database.SaasSubCancelled, database.SaasSubExpired,
	}).Find(&subs)

	for i := range subs {
		sub := &subs[i]
		var tenant database.Tenant
		database.CentralDB.First(&tenant, sub.TenantID)
		if tenant.Status == database.TenantStatusBlocked {
			continue
		}
		effective := ResolveEffectiveStatus(sub, &tenant, now, cfg.GracePeriodDays)
		daysLeft := CalendarDaysUntilEnd(sub.EndDate, now)
		for _, d := range cfg.ReminderDays {
			if daysLeft == d && effective == database.SaasSubActive {
				QueueNotification(sub.TenantID, sub.ID, "in_app", fmt.Sprintf("reminder_%dd", d),
					map[string]interface{}{"days": d, "end_date": sub.EndDate.In(lima()).Format("2006-01-02")})
				reminders++
			}
		}
		_, _ = EnsureBillingCycle(sub)
		// Abre el período mensual de cuota al entrar el nuevo mes. La emisión también
		// lo crea por su cuenta; esto solo adelanta el trabajo para que el panel
		// muestre el cupo renovado aunque el tenant todavía no haya emitido nada.
		_, _, _ = docusage.EnsureQuotaPeriod(sub.TenantID)
	}
	notifications = ProcessNotificationQueue(100)
	return reminders, notifications
}

// RunLimaDailyEvaluation evalúa bloqueo/suspensión por día calendario (00:05 America/Lima).
// No usa timestamp exacto: vence hoy sigue activo hasta fin de día.
func RunLimaDailyEvaluation() (statusUpdates int, suspended int, overdueCycles int) {
	if database.CentralDB == nil || !database.IsCentralSchemaReady() {
		return 0, 0, 0
	}
	cfg, _ := LoadSettings()
	now := NowLima()

	var subs []database.SaasSubscription
	database.CentralDB.Where("status NOT IN ?", []string{
		database.SaasSubCancelled, database.SaasSubExpired,
	}).Find(&subs)

	for i := range subs {
		sub := &subs[i]
		var tenant database.Tenant
		if database.CentralDB.First(&tenant, sub.TenantID).Error != nil {
			continue
		}
		strikeMax := EffectiveStrikeMax(cfg)
		if tenant.Status == database.TenantStatusBlocked || tenant.StrikeCount >= strikeMax {
			continue
		}

		// Expirar provisional vencido
		if sub.ProvisionalUntil != nil && !sub.ProvisionalUntil.After(now) &&
			(sub.Status == database.SaasSubProvisionalActive || sub.Status == database.SaasSubProvisional) {
			database.CentralDB.Model(sub).Updates(map[string]interface{}{
				"status": database.SaasSubSuspended, "provisional_until": nil,
			})
			if tenant.Status == database.TenantStatusActive {
				database.CentralDB.Model(&tenant).Update("status", database.TenantStatusSuspended)
			}
			statusUpdates++
		}

		effective := ResolveEffectiveStatus(sub, &tenant, now, cfg.GracePeriodDays)
		if sub.Status != effective && effective != database.SaasSubProvisionalActive {
			updates := map[string]interface{}{"status": effective}
			if effective == database.SaasSubGracePeriod && sub.GraceEndsAt == nil {
				g := CalendarDateLima(sub.EndDate).AddDate(0, 0, cfg.GracePeriodDays)
				updates["grace_ends_at"] = EndOfDayLima(g)
			}
			database.CentralDB.Model(sub).Updates(updates)
			statusUpdates++
		}

		daysAfter := CalendarDaysAfterEnd(sub.EndDate, now)

		// Un cobro impago se da por vencido al agotarse su ventana de pago, sin esperar a que
		// termine el período contratado. Antes esto vivía dentro de `if daysAfter > 0`, así que
		// un plan anual daba doce meses de uso antes de que el sistema registrara el impago.
		// Marcar el ciclo no suspende a nadie: lo hace visible en el panel central para que
		// ventas decida (las renovaciones sí siguen suspendiéndose solas al vencer + gracia).
		payWindow := EffectivePaymentWindowDays(cfg)
		overdueLimit := CalendarDateLima(now).AddDate(0, 0, -payWindow)
		res := database.CentralDB.Model(&database.SaasBillingCycle{}).
			Where("tenant_id = ? AND status = ?", sub.TenantID, database.SaasInvoicePending).
			Where("due_date < ?", overdueLimit).
			Update("status", database.SaasInvoiceOverdue)
		if res.RowsAffected > 0 {
			overdueCycles += int(res.RowsAffected)
		}

		// Auto-suspend solo tras grace (día calendario), nunca el mismo día de vencimiento.
		// Si pasó la gracia y autoSuspend está habilitado: marcar como suspended.
		// Si no, marcar como expired (informativo) solo si AutoSuspend deshabilitado.
		if daysAfter > cfg.GracePeriodDays && sub.Status != database.SaasSubSuspended &&
			sub.Status != database.SaasSubCancelled && sub.Status != database.SaasSubExpired {
			if cfg.AutoSuspendEnabled && tenant.Status != database.TenantStatusSuspended {
				database.CentralDB.Model(sub).Updates(map[string]interface{}{
					"status": database.SaasSubSuspended, "provisional_until": nil,
				})
				database.CentralDB.Model(&tenant).Update("status", database.TenantStatusSuspended)
				sid := sub.ID
				LogEvent(sub.TenantID, &sid, EventSuspended, "system", nil,
					"auto_suspend daily 00:05 Lima", MetaJSON(map[string]interface{}{"days_after_end": daysAfter}))
				suspended++
				InvalidateTenantCache(sub.TenantID)
			} else {
				database.CentralDB.Model(sub).Update("status", database.SaasSubExpired)
			}
			statusUpdates++
		}
	}

	expiredPkgs := docusage.ExpirePackagesForEndedCycles()

	logger.L.Info("saas_lima_daily_evaluation",
		slog.Int("status_updates", statusUpdates),
		slog.Int("suspended", suspended),
		slog.Int("overdue_cycles", overdueCycles),
		slog.Int("expired_document_packages", expiredPkgs),
		slog.String("timezone", LimaTimezone),
	)
	return statusUpdates, suspended, overdueCycles
}

// RunDailyJobs compat: hourly + evaluación diaria si corresponde.
func RunDailyJobs() (reminders int, statusUpdates int, suspended int) {
	r, _ := RunHourlyJobs()
	su, s, _ := RunLimaDailyEvaluation()
	return r, su, s
}

// UnblockTenant desbloqueo manual por soporte/ventas.
func UnblockTenant(tenantID uint, adminID uint, reason string) error {
	var subID *uint
	var sub database.SaasSubscription
	if database.CentralDB.Where("tenant_id = ?", tenantID).Order("created_at desc").First(&sub).Error == nil {
		subID = &sub.ID
	}
	err := database.CentralDB.Model(&database.Tenant{}).Where("id = ?", tenantID).Updates(map[string]interface{}{
		"strike_count": 0, "payment_blocked": false, "status": database.TenantStatusSuspended,
	}).Error
	if err != nil {
		return err
	}
	LogEvent(tenantID, subID, EventTenantUnblocked, "admin", &adminID, reason, "")
	InvalidateTenantCache(tenantID)
	return nil
}
