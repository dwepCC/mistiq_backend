# Matriz de compatibilidad — P0 Pagos detracción 1001

**Fecha:** 2026-06-19  
**Alcance:** flujo de cobro facturas `operation_type_code = 1001` (PEN, factura 01).

## Resumen del cambio

| Concepto | Antes | Después |
|----------|-------|---------|
| Validación pagos directos | `Σ pagos ≥ sale.total` | `Σ pagos directos ≥ net_payable_pen` |
| Línea detracción | No existía | Auto `detraccion_bn` por monto calculado |
| Tesorería detracción | N/A | Solo `tenant_sale_payments` (informativo) |
| Caja / banco | Todo el total podía ir a efectivo | Solo neto cobrable en métodos directos |

## Caso de referencia

| Concepto | Monto |
|----------|-------|
| Total factura | S/ 1,180.00 |
| Detracción (4% sobre gravado) | S/ 47.20 |
| Neto cobrable (pagos directos) | S/ 1,132.80 |

---

## Compatibilidad por módulo

| Módulo / flujo | ¿Modificado? | Estado | Notas |
|----------------|--------------|--------|-------|
| XML SUNAT / Lycet | No | ✅ Compatible | Emisión fiscal sin cambios |
| PDF oficial | No | ✅ Compatible | |
| Notas de crédito (NC) | No | ✅ Compatible | P0 billing previo intacto |
| Notas de débito (ND) | No | ✅ Compatible | |
| Reenvíos SUNAT | No | ✅ Compatible | |
| Emisión factura 1001 | No (fiscal) | ✅ Compatible | `tenant_sale_detraccion` igual |
| Registro venta 1001 | Sí | ✅ Corregido | Pagos normalizados en backend |
| Método `detraccion_bn` | Sí (nuevo) | ✅ | Migración v069 + seed nuevos tenants |
| `tenant_sale_payments` | Sí | ✅ | Incluye línea SPOT automática |
| `tenant_cash_movements` | Sí (exclusión) | ✅ | Detracción no genera movimiento |
| `tenant_bank_movements` | Sí (exclusión) | ✅ | Detracción no genera movimiento |
| Arqueo / cierre caja | Ajuste defensivo | ✅ | SPOT excluido de totales físicos/electrónicos |
| UI registro ventas | Sí | ✅ | Total / detracción / neto + línea SPOT |
| Historial billing (P1) | No | ✅ Compatible | Badge y detalle detracción intactos |
| Reportes ventas | Sí (P1) | ✅ | KPIs detracción/neto/SPOT, columnas Excel/PDF |
| Excel export | Sí (P1) | ✅ | Columnas detracción y neto cobrable |
| Dashboard | Sí (P1 lite) | ✅ | KPIs SPOT cuando hay facturas 1001 en el período |
| Detalle venta / billing | Sí (P1) | ✅ | `SalePaymentsBreakdown` + bloque `detraccion` en API |
| Caja analítica | Sí (P2) | ✅ | SPOT separado del arqueo en cierre de sesión y movimientos |
| Cuentas por cobrar | Sí (P3) | ✅ | Saldo directo = neto − cobros; API `/api/receivables` |
| Confirmación BN | Sí (P3) | ✅ | `bn_confirmation_status`: pending → confirmed/rejected |
| Seguimiento SUNAT | No implementado | ⏸ Fuera de alcance | |
| Estado de cuenta | Sí (P3) | ✅ | `GET /api/receivables/statement?contact_id=` |
| KPI financieros | No implementado | ⏸ Fuera de alcance | |

---

## Archivos tocados (P0 Pagos)

### Backend
- `pkg/paymentmethod/detraccion_bn.go` — constantes y helper
- `pkg/database/migrations.go` — seed + `EnsureDetractionPaymentMethod`
- `pkg/database/tenantmigrations/v069_detraction_payment_method.go`
- `pkg/database/tenantmigrations/registry.go`
- `internal/detraccion/service.go` — `Evaluate()`
- `internal/sales/service/sale_payment_detraccion.go` — normalización pagos
- `internal/sales/service/sale_service.go` — integración create + summary
- `internal/cashbank/service/cashbank_service.go` — RecordPayment / sesión caja
- `internal/cashbank/service/payment_method_report.go` — canal detraction
- `internal/cashbank/service/cashbank_report_service.go` — exclusión SPOT en cierre

### Frontend
- `frontend_tenant/src/utils/fiscalDetraction.ts`
- `frontend_tenant/src/pages/sales/SalesRegisterPage.tsx`

### Tests
- `internal/sales/service/sale_payment_detraccion_test.go`
- `internal/cashbank/service/payment_method_report_test.go`

---

## Pruebas ejecutadas

```bash
go test ./internal/sales/service/... ./internal/cashbank/service/... ./pkg/paymentmethod/... ./internal/detraccion/... ./pkg/sunat/detraccion/...
```

Ver salida en entrega del agente.

---

## Riesgos residuales (post-P3)

1. **Reportes legacy:** listados que lean `tenant_sale_payments` sin filtrar `detraccion_bn` pueden mostrar la línea SPOT como método más; el agregado `payment_totals` del listado de ventas ya la excluye.
2. **NC y cobros:** una nota de crédito anula la venta pero no revierte automáticamente cobros registrados; el saldo CxC se calcula como documento − pagos directos mientras la venta no esté anulada.
3. **KPI financieros avanzados:** aging, DSO y conciliación BN masiva quedan para fases posteriores.

---

## Archivos tocados (P3 CxC + BN)

### Backend
- `pkg/paymentmethod/credito.go`
- `pkg/database/tenantmigrations/v070_receivables_p3.go`
- `pkg/database/migrations.go` — campos BN + `EnsureCreditPaymentMethod`
- `internal/receivables/` — service, handler, routes
- `internal/sales/service/sale_payment_detraccion.go` — crédito parcial 1001
- `internal/sales/service/sale_service.go` — `status=credit`
- `internal/cashbank/service/cashbank_service.go` — destino `receivable`
- `internal/detraccion/service.go` — BN pending al persistir
- `routes/routes.go`

### Frontend
- `src/services/receivables.service.ts`
- `src/pages/receivables/ReceivablesPage.tsx`
- `src/components/receivables/CollectPaymentModal.tsx`
- `src/components/receivables/BnConfirmationPanel.tsx`
- `src/pages/billing/BillingPage.tsx`
- `src/components/Sidebar.tsx`, `AppRouter.tsx`

### Tests
- `internal/receivables/service/receivable_service_test.go`
