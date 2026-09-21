package service

// Estados de TenantEcommerceOrder.Status — Contrato ecommerce v2 §3.1
// (docs/ECOMMERCE-EVOLUTION-CONTRACT.md). Ciclo de vida del PEDIDO, independiente de si ya se
// convirtió a venta (ver ConvertToSale) y del estado de pago (PaymentStatus, inerte por ahora).
const (
	OrderStatusPendiente         = "PENDIENTE"
	OrderStatusConfirmado        = "CONFIRMADO"
	OrderStatusEnPreparacion     = "EN_PREPARACION"
	OrderStatusEmpaquetado       = "EMPAQUETADO"
	OrderStatusListoParaDespacho = "LISTO_PARA_DESPACHO"
	OrderStatusDespachado        = "DESPACHADO"
	OrderStatusEntregado         = "ENTREGADO"
	OrderStatusCancelado         = "CANCELADO"
	OrderStatusRechazado         = "RECHAZADO"
	OrderStatusDevuelto          = "DEVUELTO"
)

// Estados de TenantEcommerceDispatch.Status — Contrato v2 §5/§8, Fase 8. Ciclo de vida del
// DESPACHO, deliberadamente en constantes SEPARADAS de OrderStatus* aunque algún valor literal
// coincida ("DESPACHADO" existe en ambos dominios) — nunca se reutiliza la misma constante Go para
// los dos, así una futura divergencia entre los dos ciclos de vida (p. ej. si el pedido y el
// despacho llegaran a necesitar palabras distintas) no obliga a tocar el otro dominio.
// DispatchStatusPendiente queda definido para una evolución futura (Fase 9, un flujo en dos pasos);
// ningún endpoint de Fase 8 lo asigna — CreateDispatch siempre crea el despacho ya en
// DispatchStatusDespachado (decisión confirmada explícitamente).
const (
	DispatchStatusPendiente  = "PENDIENTE_DESPACHO"
	DispatchStatusDespachado = "DESPACHADO"
	DispatchStatusEnTransito = "EN_TRANSITO"
	DispatchStatusEntregado  = "ENTREGADO"
	DispatchStatusDevuelto   = "DEVUELTO"
)

// Permisos de pedidos web — Contrato v2 §7. Reemplazan el uso indiferenciado de "ecommerce.orders"
// (deprecado, se mantiene solo por compatibilidad — ver v138_ecommerce_orders_rbac_v2.go).
const (
	PermOrdersView     = "ecommerce.orders_view"
	PermOrdersPrepare  = "ecommerce.orders_prepare"
	PermOrdersManage   = "ecommerce.orders_manage"
	PermOrdersConvert  = "ecommerce.orders_convert"
	PermOrdersDispatch = "ecommerce.orders_dispatch"
	PermOrdersReturn   = "ecommerce.orders_return"
)

var validOrderStatuses = map[string]bool{
	OrderStatusPendiente: true, OrderStatusConfirmado: true, OrderStatusEnPreparacion: true,
	OrderStatusEmpaquetado: true, OrderStatusListoParaDespacho: true, OrderStatusDespachado: true,
	OrderStatusEntregado: true, OrderStatusCancelado: true, OrderStatusRechazado: true,
	OrderStatusDevuelto: true,
}

// OrderTransition una transición válida del estado del pedido y el permiso que exige.
type OrderTransition struct {
	From       string
	To         string
	Permission string
}

// orderTransitions tabla completa — Contrato v2 §5. La transición LISTO_PARA_DESPACHO->DESPACHADO
// hoy solo cambia Status (TenantEcommerceDispatch todavía no existe, es Fase 8); cuando esa fase
// cree el dominio de despacho, esta misma transición pasará a crear también el registro asociado
// sin cambiar el permiso que ya la protege.
var orderTransitions = []OrderTransition{
	{OrderStatusPendiente, OrderStatusConfirmado, PermOrdersManage},
	{OrderStatusPendiente, OrderStatusRechazado, PermOrdersManage},
	{OrderStatusConfirmado, OrderStatusRechazado, PermOrdersManage},
	{OrderStatusPendiente, OrderStatusCancelado, PermOrdersManage},
	{OrderStatusConfirmado, OrderStatusCancelado, PermOrdersManage},
	{OrderStatusEnPreparacion, OrderStatusCancelado, PermOrdersManage},
	{OrderStatusEmpaquetado, OrderStatusCancelado, PermOrdersManage},
	{OrderStatusListoParaDespacho, OrderStatusCancelado, PermOrdersManage},
	{OrderStatusConfirmado, OrderStatusEnPreparacion, PermOrdersPrepare},
	{OrderStatusEnPreparacion, OrderStatusEmpaquetado, PermOrdersPrepare},
	{OrderStatusEmpaquetado, OrderStatusListoParaDespacho, PermOrdersPrepare},
	{OrderStatusListoParaDespacho, OrderStatusDespachado, PermOrdersDispatch},
	{OrderStatusDespachado, OrderStatusDespachado, PermOrdersDispatch}, // actualizar tracking, sin cambio de estado (§5)
	{OrderStatusDespachado, OrderStatusEntregado, PermOrdersDispatch},
	{OrderStatusEntregado, OrderStatusDevuelto, PermOrdersReturn},
}

// FindOrderTransition busca la transición registrada de "from" a "to". ok=false si no es una
// transición válida del pedido (incluye el caso from==to fuera de DESPACHADO, que no tiene
// sentido para ningún otro estado).
func FindOrderTransition(from, to string) (OrderTransition, bool) {
	for _, t := range orderTransitions {
		if t.From == from && t.To == to {
			return t, true
		}
	}
	return OrderTransition{}, false
}

// requiresReasonNotes transiciones donde el motivo (Notes) es obligatorio — Contrato v2 §5.
func requiresReasonNotes(to string) bool {
	return to == OrderStatusCancelado || to == OrderStatusRechazado
}
