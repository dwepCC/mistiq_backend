package database

import "time"

// Estados de suscripción SaaS (source of truth: BD central).
const (
	SaasSubActive            = "active"
	SaasSubGracePeriod       = "grace_period"
	SaasSubOverdue           = "overdue"
	SaasSubSuspended         = "suspended"
	SaasSubCancelled         = "cancelled"
	SaasSubExpired           = "expired"
	SaasSubTrial             = "trial"
	SaasSubProvisional       = "provisional"        // legacy
	SaasSubProvisionalActive = "provisional_active" // reactivación temporal (máx 12h)
)

// Estado tenant por strikes / bloqueo de pagos.
const (
	TenantStatusActive    = "active"
	TenantStatusSuspended = "suspended"
	TenantStatusBlocked   = "blocked"
)

// Ciclos de facturación.
const (
	SaasCycleMonthly    = "monthly"
	SaasCycleSemiannual = "semiannual"
	SaasCycleAnnual     = "annual"
)

// Estados de ciclo de cobro / factura SaaS.
const (
	SaasInvoicePending = "pending"
	SaasInvoicePaid    = "paid"
	SaasInvoiceOverdue = "overdue"
	// SaasInvoicePendingReview: el tenant ya subió un comprobante para este ciclo y está
	// esperando que un admin lo apruebe o rechace. Mientras esté en este estado el ciclo NO
	// debe ser tocado por el cron de vencidos (RunLimaDailyEvaluation solo barre 'pending') ni
	// mostrar el botón de pago en los paneles tenant — ver saas.SubmitPayment/RejectPayment.
	SaasInvoicePendingReview = "pending_review"
	// SaasInvoiceRejected: NO es "pago rechazado" — es la cancelación administrativa manual de
	// una factura completa (ver manual_invoice.go). Un pago individual rechazado no usa este
	// valor; el ciclo vuelve a 'pending'/'overdue' (ver RejectPayment).
	SaasInvoiceRejected = "rejected"
)

// Estados de pago SaaS.
const (
	SaasPayPendingReview = "pending_review"
	SaasPayApproved      = "approved"
	SaasPayRejected      = "rejected"
	SaasPayPending       = "pending" // legacy admin-created
	// SaasPayReversed: pago que SÍ se había aprobado pero se anuló después (ver
	// saas.RevertApprovedPayment) — deshace la extensión de suscripción/ciclo que esa
	// aprobación había producido. Distinto de "rejected" (nunca llegó a aprobarse).
	SaasPayReversed = "reversed"
)

// SaasPlatformSettings configuración global del SaaS (fila única ID=1).
type SaasPlatformSettings struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UpdatedAt time.Time `json:"updated_at"`

	ReminderDaysJSON string `gorm:"type:text" json:"reminder_days_json"` // ej. [7,5,3,1]
	GracePeriodDays  int    `gorm:"default:3" json:"grace_period_days"`
	// PaymentWindowDays: días para pagar un cobro ya emitido. Aplica a quien está usando el
	// servicio sin haberlo pagado todavía (alta nueva); las renovaciones se rigen por la
	// fecha de vencimiento del período contratado.
	PaymentWindowDays              int     `gorm:"default:3" json:"payment_window_days"`
	ReconnectionFee                float64 `gorm:"default:50" json:"reconnection_fee"`
	AutoSuspendEnabled             bool    `gorm:"default:true" json:"auto_suspend_enabled"`
	ProvisionalReactivationEnabled bool    `gorm:"default:true" json:"provisional_reactivation_enabled"`
	ProvisionalHours               int     `gorm:"default:12" json:"provisional_hours"`
	StrikeMax                      int     `gorm:"default:2" json:"strike_max"`
	CronEvalHour                   int     `gorm:"default:0" json:"cron_eval_hour"`   // America/Lima
	CronEvalMinute                 int     `gorm:"default:5" json:"cron_eval_minute"` // America/Lima

	PaymentMethodsJSON string `gorm:"type:text" json:"payment_methods_json"`
	BankAccountsJSON   string `gorm:"type:text" json:"bank_accounts_json"`
	YapeQRURL          string `gorm:"size:500" json:"yape_qr_url"`
	PlinQRURL          string `gorm:"size:500" json:"plin_qr_url"`
	PortalURL          string `gorm:"size:500" json:"portal_url"` // override opcional; vacío = /subscription tenant

	SupportWhatsApp string `gorm:"size:50" json:"support_whatsapp"`
	SupportEmail    string `gorm:"size:255" json:"support_email"`
	SupportPhone    string `gorm:"size:50" json:"support_phone"`

	// OperationsKeyHash bcrypt — requerida para eliminar tenants por completo (panel central).
	OperationsKeyHash string `gorm:"size:255" json:"-"`

	EmailTemplatesJSON    string `gorm:"type:text" json:"email_templates_json"`
	WhatsAppTemplatesJSON string `gorm:"type:text" json:"whatsapp_templates_json"`
}

func (SaasPlatformSettings) TableName() string { return "saas_platform_settings" }

// SaasBillingCycle obligación de pago generada por período.
type SaasBillingCycle struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	TenantID       uint      `gorm:"not null;index" json:"tenant_id"`
	SubscriptionID uint      `gorm:"not null;uniqueIndex:idx_billing_cycle_sub_period,priority:1" json:"subscription_id"`
	PlanID         uint      `gorm:"not null" json:"plan_id"`
	PeriodStart    time.Time `gorm:"not null" json:"period_start"`
	PeriodEnd      time.Time `gorm:"not null;uniqueIndex:idx_billing_cycle_sub_period,priority:2" json:"period_end"`
	DueDate        time.Time `gorm:"not null;index" json:"due_date"`
	// Amount es el importe A COBRAR, ya con el descuento aplicado: toda la lógica de deuda,
	// conciliación de pagos y mora sigue mirando este campo. Los tres siguientes son la
	// memoria de cómo se calculó, para poder mostrarlo y auditarlo.
	Amount          float64 `gorm:"not null" json:"amount"`
	GrossAmount     float64 `gorm:"default:0" json:"gross_amount"`   // precio del plan × meses, sin descuento
	MonthsCovered   int     `gorm:"default:0" json:"months_covered"` // meses que cubre el cobro
	DiscountType    string  `gorm:"size:10" json:"discount_type"`    // "" | percent | fixed
	DiscountValue   float64 `gorm:"default:0" json:"discount_value"`
	ReconnectionFee float64 `gorm:"default:0" json:"reconnection_fee"`
	Currency        string  `gorm:"size:10;default:'PEN'" json:"currency"`
	Status          string  `gorm:"size:30;index;default:'pending'" json:"status"`
	ProvisionalUsed bool    `gorm:"default:false" json:"provisional_used"`
	// Cuota documentos electrónicos (snapshot del plan al crear ciclo).
	IsUnlimitedDocuments bool       `gorm:"default:false" json:"is_unlimited_documents"`
	DocumentsLimit       int        `gorm:"default:0" json:"documents_limit"`
	DocumentsUsed        int        `gorm:"default:0" json:"documents_used"`
	PaidAt               *time.Time `json:"paid_at,omitempty"`
	PaymentID            *uint      `gorm:"index" json:"payment_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func (SaasBillingCycle) TableName() string { return "saas_billing_cycles" }

// SaasNotificationLog auditoría de notificaciones.
type SaasNotificationLog struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	TenantID       uint       `gorm:"not null;index" json:"tenant_id"`
	SubscriptionID *uint      `gorm:"index" json:"subscription_id,omitempty"`
	Channel        string     `gorm:"size:30;index" json:"channel"` // email, whatsapp, in_app, push
	TemplateKey    string     `gorm:"size:80" json:"template_key"`
	PayloadJSON    string     `gorm:"type:text" json:"payload_json"`
	Status         string     `gorm:"size:20;index;default:'queued'" json:"status"` // queued, sent, failed
	ScheduledAt    time.Time  `gorm:"index" json:"scheduled_at"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
	ErrorMessage   string     `gorm:"size:500" json:"error_message,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (SaasNotificationLog) TableName() string { return "saas_notification_logs" }

// SaasSubscriptionEvent auditoría suspensión/reactivación/cambios.
type SaasSubscriptionEvent struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	TenantID       uint      `gorm:"not null;index" json:"tenant_id"`
	SubscriptionID *uint     `gorm:"index" json:"subscription_id,omitempty"`
	EventType      string    `gorm:"size:50;index" json:"event_type"`
	ActorType      string    `gorm:"size:20" json:"actor_type"` // system, admin, tenant
	ActorID        *uint     `json:"actor_id,omitempty"`
	Reason         string    `gorm:"type:text" json:"reason"`
	MetadataJSON   string    `gorm:"type:text" json:"metadata_json"`
	CreatedAt      time.Time `json:"created_at"`
}

func (SaasSubscriptionEvent) TableName() string { return "saas_subscription_events" }
