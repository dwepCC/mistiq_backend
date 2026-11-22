# Auditoría completa del multimoneda (PEN/USD) — 11-nov-2026

Alcance: ventas, cotizaciones, compras, órdenes de compra, caja, bancos, CxC/CxP, reportes, dashboard, contabilidad y migraciones
V180–V182. Método: 3 revisiones de código independientes (solo lectura) + pruebas en vivo por API sobre MariaDB local
(también con `sql_mode` estricto `ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,...`) + suites automáticas.

## Qué se probó en vivo (todo correcto tras los arreglos)

| Ciclo | Resultado |
|---|---|
| Venta USD en efectivo (TC del día completado por el servidor) → anulación | caja sube `monto × TC`; tras anular vuelve **exacta** al valor previo |
| Venta USD por Yape | saldo de la cuenta (en soles) sube `monto × TC` (antes subía el monto en dólares); la reversión copia moneda/TC/soles del original y deja el saldo en el valor previo |
| Venta USD a crédito → cobro parcial → sobrecobro → cobro del saldo → anulación | CxC en USD correcta, sobrecobro rechazado (mensaje con US$), caja = suma de cobros × TC, anulación deja la caja en el valor previo |
| Compra USD → anulación | costo del producto queda en soles (US$5 × 3.4 = S/17) |
| Reportes/KPI (22 endpoints: ventas, por producto, utilidades, dashboard, caja, sesión, banco, CxC, CxP, compras, OC, cotizaciones) | 200 en `sql_mode` estricto |
| V182 reconstruido desde cero sobre el tenant demo y vuelto a correr | mismos totales (incluye USD) y 0 cambios en la 2ª corrida |
| Control del TC (sin permiso de precios) | TC fuera de ±5 % del oficial del día se rechaza (test de servicio) |

## Hallazgos corregidos en esta auditoría

1. **Saldo de cuentas bancarias** sumaba dólares como soles (`balance + amount`). Ahora usa el equivalente en soles (`bankDeltaPEN`).
2. **Reversiones de caja/banco** recalculaban el snapshot con el TC actual del documento; ahora copian moneda/TC/soles del original.
3. **Libro mayor**: caja, bancos y cobros de CxC se sumaban sin convertir → `COALESCE(amount_pen, amount)` / `TotalPENSQL`.
4. **Utilidades detalladas** restaba costo en soles a un precio en dólares → el precio de ventas USD se lleva a soles con el TC de la venta.
5. **Ventas por producto**, **productos de la sesión de caja**, **KPI de caja del dashboard**, **ventas por método de pago del dashboard**, **estado de cuenta bancario**: ahora en soles.
6. **TC editable sin control** (cualquier cajero podía fijar TC=10 y cobrar un precio mucho menor): sin `sales.override_price` el TC debe estar a ±5 % del oficial del día (ventas y cotizaciones).
7. **Notas de crédito/débito en USD** perdían el TC (contabilidad las valoraba a 1.00): se copia el TC del original; la nota independiente toma el TC del día.
8. **Anticipos**: solo se pueden deducir en la misma moneda de la venta.
9. **Edición de cotización PEN→USD con los mismos números** esquivaba la validación de precios: si cambia la moneda o el TC se vuelven a validar todas las líneas.
10. **Tukichef**: el servidor fuerza PEN (antes aceptaba `currency` del body).
11. **V182**: el UPDATE masivo ahora va por tramos de 5000 ids (evita bloquear cobros en tenants grandes).
12. **Error 1055 con `ONLY_FULL_GROUP_BY`** en `GET /api/sales` y `GET /api/purchases` (agrupación por expresión con alias igual a la columna): se agrupa por alias propio (`cur_code`, `method`). Habría roto ambos listados en MySQL 8 estricto si el motor no reconociera la expresión repetida.
13. Frontend: no se puede guardar un documento en USD sin TC válido; tope de S/ 700 de boleta se compara en soles; listados/detalle de ventas y facturación muestran la moneda del documento; Ingresos/Egresos de caja muestran soles + original; mensaje de sobrecobro con US$.

## Riesgos / límites que NO se resolvieron (decisión o fuera de alcance)

- **Saldos bancarios históricos**: los cobros USD por banco ya registrados en producción sumaron dólares como soles. La corrección de código aplica de ahora en adelante; reconciliar saldos pasados es un ajuste de datos aparte (no hecho).
- **MySQL 8 real no validado**: no hay instancia MySQL 8 local (solo MariaDB 10.4, probado también en modo estricto). Las sentencias de V180–V182 se revisaron por lectura. Probar `migrate-fleet` en 1–2 tenants pequeños primero.
- **Combos en USD**: el precio del combo se redondea a 2 decimales en el servidor y se marca autorizado (no se valida contra catálogo; ya era así en soles).
- **Cotización → venta**: usa el TC de la cotización (backend) pero el formulario puede pisarlo con el TC del día; decisión de diseño pendiente.
- **Resumen diario a SUNAT** (`billing_service.go` ~1195): envía `Moneda: "PEN"` fija; boletas en USD saldrían con el importe en dólares. Flujo manual fuera del uso normal; no se tocó (no probable contra SUNAT).
- **Comprobante impreso con extras/combos en USD** (`receiptBonificacion.ts`): el desglose del precio base mezcla soles de los extras con dólares.
- Dashboard "Comprobantes recientes", `PendingRefundsPanel`, `GetSessionDocuments.ActiveTotal`, página legacy `/cashbank/cash`: siguen con importes sin convertir / "S/" fijo (secundarios).
- KPI de compras del dashboard incluye compras anuladas (previo al multimoneda).
- Moneda por producto, y POS/Tukichef/tienda en USD: no implementados (solo soles).
- Un USD sin TC guardado se trata como soles al valor facial en el dashboard, pero con el TC del día en el reporte de ventas (2 casos conocidos en producción).
- Despliegue: backend primero con `migrate-fleet` completo y verificado **por tenant** antes de reiniciar (un tenant sin V182 no puede registrar movimientos), en horario de baja carga.
