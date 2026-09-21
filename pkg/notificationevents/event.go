package notificationevents

// EventChanged nombre del evento SSE / Redis. Señal mínima a propósito (Fase 6): el payload NUNCA
// lleva contenido de la notificación (título, link, pedido) porque el hub es por TENANT, no por
// usuario — cualquier cliente conectado del tenant recibiría ese contenido sin que el backend haya
// verificado que ESE usuario tiene permiso para verlo. El cliente solo sabe "algo cambió" y debe
// volver a pedir /api/notifications/unread-count (autenticado, filtrado por usuario) como fuente de
// verdad real.
const EventChanged = "notification.changed"

// ChangedPayload evento push hacia frontends tenant — sin datos de negocio, ver comentario arriba.
type ChangedPayload struct {
	Event    string `json:"event"`
	TenantID uint   `json:"tenant_id"`
}

func NewChanged(tenantID uint) ChangedPayload {
	return ChangedPayload{Event: EventChanged, TenantID: tenantID}
}
