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
		{OrderStatusListoParaDespacho, OrderStatusDespachado, PermOrdersDispatch, true},
		{OrderStatusDespachado, OrderStatusEntregado, PermOrdersDispatch, true},
		{OrderStatusEntregado, OrderStatusDevuelto, PermOrdersReturn, true},

		// Flujo de despacho corregido (punto 8): NUNCA se puede saltar directo a DESPACHADO sin
		// pasar por LISTO_PARA_DESPACHO — esta es la inconsistencia real que v1 del contrato tenía
		// en su diagrama de flujo (sección 10) y que quedó corregida en v2.
		{OrderStatusEmpaquetado, OrderStatusDespachado, "", false},
		{OrderStatusConfirmado, OrderStatusDespachado, "", false},
		{OrderStatusPendiente, OrderStatusDespachado, "", false},

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
