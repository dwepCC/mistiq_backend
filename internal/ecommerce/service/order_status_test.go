package service

import "testing"

// TestFindOrderTransition_TablaAprobada verifica exactamente la tabla de transiciones del
// Contrato ecommerce v2 §5 (docs/ECOMMERCE-EVOLUTION-CONTRACT.md) — cada fila permitida con su
// permiso, y algunas saltos que NUNCA deben ser válidos (el flujo de despacho corregido: no se
// puede despachar sin antes estar LISTO_PARA_DESPACHO, punto 8 aprobado).
func TestFindOrderTransition_TablaAprobada(t *testing.T) {
	cases := []struct {
		from, to, wantPermission string
		wantOK                   bool
	}{
		{OrderStatusPendiente, OrderStatusConfirmado, PermOrdersManage, true},
		{OrderStatusPendiente, OrderStatusRechazado, PermOrdersManage, true},
		{OrderStatusConfirmado, OrderStatusRechazado, PermOrdersManage, true},
		{OrderStatusPendiente, OrderStatusCancelado, PermOrdersManage, true},
		{OrderStatusConfirmado, OrderStatusCancelado, PermOrdersManage, true},
		{OrderStatusEnPreparacion, OrderStatusCancelado, PermOrdersManage, true},
		{OrderStatusEmpaquetado, OrderStatusCancelado, PermOrdersManage, true},
		{OrderStatusListoParaDespacho, OrderStatusCancelado, PermOrdersManage, true},
		{OrderStatusConfirmado, OrderStatusEnPreparacion, PermOrdersPrepare, true},
		{OrderStatusEnPreparacion, OrderStatusEmpaquetado, PermOrdersPrepare, true},
		{OrderStatusEmpaquetado, OrderStatusListoParaDespacho, PermOrdersPrepare, true},
		{OrderStatusEntregado, OrderStatusDevuelto, PermOrdersReturn, true},

		// Flujo de despacho corregido (punto 8): NUNCA se puede saltar directo a DESPACHADO sin
		// pasar por LISTO_PARA_DESPACHO — esta es la inconsistencia real que v1 del contrato tenía
		// en su diagrama de flujo (sección 10) y que quedó corregida en v2.
		{OrderStatusEmpaquetado, OrderStatusDespachado, "", false},
		{OrderStatusConfirmado, OrderStatusDespachado, "", false},
		{OrderStatusPendiente, OrderStatusDespachado, "", false},

		// Auditoría de Fase 11 (Deuda #8): LISTO_PARA_DESPACHO->DESPACHADO y DESPACHADO->ENTREGADO
		// se eliminaron de esta tabla — esas transiciones existen EXCLUSIVAMENTE a través de
		// EcommerceService.CreateDispatch y MarkDispatchDelivered (Fase 8/9), nunca del endpoint
		// genérico UpdateOrderStatusAPI, para que nadie pueda saltarse la creación/sincronización
		// del Dispatch. Ver comentario de orderTransitions.
		{OrderStatusListoParaDespacho, OrderStatusDespachado, "", false},
		{OrderStatusDespachado, OrderStatusEntregado, "", false},
		{OrderStatusDespachado, OrderStatusDespachado, "", false},

		// No se puede saltar etapas de preparación.
		{OrderStatusConfirmado, OrderStatusEmpaquetado, "", false},
		{OrderStatusPendiente, OrderStatusEnPreparacion, "", false},

		// DEVUELTO solo desde ENTREGADO, nunca desde otro estado.
		{OrderStatusDespachado, OrderStatusDevuelto, "", false},
		{OrderStatusConfirmado, OrderStatusDevuelto, "", false},

		// Cancelar después de DESPACHADO no es una transición registrada (Contrato v2 §5: "cualquiera
		// antes de DESPACHADO").
		{OrderStatusDespachado, OrderStatusCancelado, "", false},
		{OrderStatusEntregado, OrderStatusCancelado, "", false},
	}
	for _, tc := range cases {
		got, ok := FindOrderTransition(tc.from, tc.to)
		if ok != tc.wantOK {
			t.Errorf("FindOrderTransition(%s, %s): ok=%v, quería %v", tc.from, tc.to, ok, tc.wantOK)
			continue
		}
		if ok && got.Permission != tc.wantPermission {
			t.Errorf("FindOrderTransition(%s, %s): permiso=%s, quería %s", tc.from, tc.to, got.Permission, tc.wantPermission)
		}
	}
}

// TestFindOrderTransition_EstadosTerminalesNoEntranAPreparacion Fase 7 (Contrato v2 §5): un
// pedido cancelado, rechazado, ya despachado o ya entregado nunca puede "retroceder" o "saltar" a
// una etapa de preparación — la tabla de transiciones ya lo garantiza por construcción (no existen
// esas filas), esto lo prueba explícitamente para que una futura edición de la tabla no lo rompa
// sin que un test lo note.
func TestFindOrderTransition_EstadosTerminalesNoEntranAPreparacion(t *testing.T) {
	terminalesOFueraDeFlujo := []string{
		OrderStatusCancelado, OrderStatusRechazado, OrderStatusDespachado, OrderStatusEntregado, OrderStatusDevuelto,
	}
	etapasPreparacion := []string{OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho}
	for _, from := range terminalesOFueraDeFlujo {
		for _, to := range etapasPreparacion {
			if _, ok := FindOrderTransition(from, to); ok {
				t.Errorf("FindOrderTransition(%s, %s) no debía existir — un pedido en %s nunca entra a preparación", from, to, from)
			}
		}
	}
}

func TestRequiresReasonNotes(t *testing.T) {
	if !requiresReasonNotes(OrderStatusCancelado) {
		t.Error("CANCELADO debe exigir motivo")
	}
	if !requiresReasonNotes(OrderStatusRechazado) {
		t.Error("RECHAZADO debe exigir motivo")
	}
	if requiresReasonNotes(OrderStatusConfirmado) {
		t.Error("CONFIRMADO no debe exigir motivo")
	}
}
