# Propuesta: Orden de Compra + compras en múltiples monedas (PEN / USD)

Fecha: 11-nov-2026. Base: análisis de `C:\xampp\htdocs\facturador-tukifac` (sistema antiguo, Laravel) y del estado actual de Tukifac. **Solo análisis, nada implementado.**

## 1. Qué hace el sistema antiguo (hechos verificados en el código)

**Orden de compra** (`modules/Purchase`, tablas `purchase_orders` / `purchase_order_items`):
- Cabecera: proveedor (snapshot JSON), sucursal, fecha emisión, fecha vencimiento, **moneda**, **tipo de cambio (`exchange_rate_sale`)**, condición de pago, totales SUNAT (gravada/exonerada/inafecta/IGV/total), adjunto (imagen/PDF), prefijo `OC`, estado (`01` registrada, `11` anulada). Numeración `OC-<id>`.
- Ítems con los mismos campos tributarios que una compra. **No mueve stock, caja ni cuentas por pagar.**
- Origen opcional: cotización de compra u oportunidad de venta (Tukifac no tiene cotización de compra; no se replica).
- Salidas: PDF A4, impresión, envío por correo, adjunto, anular.
- Se convierte en compra con `purchases.purchase_order_id`. La compra copia moneda, ítems y totales; el TC se vuelve a pedir. Una OC ya usada deja de aparecer en el selector (relación 1 OC → 1 compra, sin recepción parcial).
- Una OC con compra aceptada ya no se puede anular ni editar.

**Multimoneda en compras** (PEN y USD, `cat_currency_types`):
- La compra guarda `currency_type_id` + `exchange_rate_sale`. El TC se consulta por fecha de emisión (`/services/exchange/{fecha}`, TC **venta**), con fallback 1.
- Al cambiar de moneda, cada ítem se reconvierte (`calculateRowItem`: PEN→USD divide, USD→PEN multiplica).
- Los reportes convierten con `CASE WHEN currency='PEN' THEN total ELSE total*TC`; `PurchaseItem` expone `*_pen` calculados.

## 2. Estado actual de Tukifac (hechos verificados)

- Compras: `TenantPurchase` tiene `Currency` pero **no tiene tipo de cambio**; la UI fija `'PEN'` (`PurchaseRegisterPage.tsx`) y el servicio acepta cualquier cadena sin validar.
- Todo lo posterior usa el total tal cual, sin mirar la moneda: egreso de caja/banco (`RecordExpensePayment`), cuenta por pagar, costo del movimiento de stock y precio de compra del producto. **Si hoy llegara una compra USD por API se contabilizaría como soles.** (Riesgo latente, no hay UI que lo dispare.)
- Ventas y cotizaciones **sí** son multimoneda: `Currency` + `ExchangeRate *float64`, paquete `pkg/salecurrency` (PEN/USD, TC obligatorio solo donde corresponde), módulo `exchangerate` con caché (venta/compra por fecha, fuentes apiperu/manual).
- Cotizaciones (`internal/quotations`) es el molde: serie, correlativo, líneas con unidad de venta/presentación, descuento global, impresión, conversión con estado. Se reutiliza su estructura, no su semántica (la cotización es del cliente; la OC es **al proveedor**).
- Módulo y permisos: rutas de compras ya usan `RequireModule("purchases")` + `purchases.view|create|delete`. Los planes que incluyen compras obtienen la OC **sin tocar planes**.

## 3. Diseño propuesto (decisiones del dueño en §5)

### 3.1 Orden de compra (Fase 1) — sin efecto contable
- Tablas nuevas `tenant_purchase_orders` y `tenant_purchase_order_items` (migración **versionada V180**; no hay AutoMigrate en producción) y columna `purchase_order_id` nullable en `tenant_purchases`.
- Cabecera: `branch_id` (entra en `BranchScopedModels`), proveedor (`contact_id`), usuario, **serie y correlativo propios** (ver §3.1.1), fecha emisión, **fecha de entrega esperada**, condición de pago (texto de referencia, como `PaymentMethodsJSON` en cotización: no mueve caja), moneda PEN/USD, `exchange_rate`, subtotal/IGV/total, descuento global, observaciones/condiciones, adjunto opcional, estado.
- Estados: `draft` → `sent` (enviada al proveedor) → `received` (convertida a compra) / `cancelled`. Editable mientras no esté `received`/`cancelled`.
- Ítems: producto + presentación + unidad de venta (igual que la compra), cantidad, costo unitario, afectación IGV, `price_includes_igv`. Reutiliza el cálculo de `PurchaseService` para que OC y compra den los mismos totales.
- Acciones: crear, editar, listar/filtrar, ver, anular, **PDF/imprimir**, enviar por correo, **convertir a compra** (precarga proveedor, ítems, moneda; el TC se vuelve a leer a la fecha de la compra y es editable; la OC pasa a `received` y guarda `purchase_id`).
- Permisos: se reutilizan `purchases.view|create|delete` (sin migración de permisos ni backfill; el catálogo fino puede venir después si lo piden). No participa el POS de PIN de Tukichef, así que no requiere puente `restaurantperm`.
- Aislamiento por sucursal: la OC pertenece a una sucursal y respeta el scope GORM; en "Todas las sucursales" es solo lectura.
- Frontend (`frontend_tenant`): nueva entrada "Órdenes de compra" en el menú Compras, página de lista y de formulario basada en `PurchaseRegisterPage`/Cotizaciones; botón "Generar compra" en la OC.
- Como la OC no mueve dinero, puede ser **multimoneda desde el primer día** (moneda + TC + totales en moneda original, y equivalente en PEN en listado/PDF).

#### 3.1.1 Series de la orden de compra (decisión del dueño: igual que los demás documentos)
Tukifac ya gestiona series por sucursal en `tenant_document_series` con un catálogo de tipos (`pkg/docseries/document_types.go`). La OC se suma como un tipo más, siguiendo exactamente a Cotización:
- Nuevo tipo en el catálogo: `id: orden_compra`, `Category: "orden_compra"`, etiqueta "Orden de compra", prefijo sugerido `OC`, `Electronic:false`, `SunatNumbering:false`, `FormSelectable:true` → aparece en Empresa ▸ Series y se puede crear/activar/marcar por defecto por sucursal.
- La migración V180 (la misma de las tablas) crea la serie `OC01` por sucursal que no la tenga (patrón de `v073_quotations.go`) y `tenant_provision_seed.go` la siembra en tenants nuevos.
- La OC guarda `series_id`, `series`, `correlative`, `number` (como `TenantQuotation`) y valida con `docseries.ValidateForBranch` que la serie sea de categoría `orden_compra` y de la sucursal.
- `series_usage_service.go`: caso `orden_compra` para impedir borrar/cambiar una serie ya usada.
- Correlativo asignado dentro de la transacción de creación (mismo mecanismo que cotizaciones, sin huecos por concurrencia).
- Numeración visible `OC01-000001` (no `OC-<id>` como el antiguo, que dependía del autoincrement).

### 3.2 Multimoneda real en Compras (Fase 2) — es el cambio delicado
Regla de fondo: **el documento conserva su moneda original y su TC; la contabilidad interna (inventario, costo, caja) sigue en soles.**
1. Validar moneda (`salecurrency.NormalizeCurrency`) y **exigir TC > 0 si es USD**; columna `exchange_rate` en `tenant_purchases` (V181). Cierra el riesgo latente de la API.
2. Costo de stock y precio de compra del producto: convertir a PEN con el TC de la compra (`unit_cost_pen = unit_cost * TC`), guardando además el costo original en el ítem.
3. **Caja/banco y pagos (modelo del sistema antiguo, verificado — ver §3.2.1):** el pago a proveedor se guarda **en la moneda del documento** (sin moneda ni TC propios en el pago); la cuenta bancaria tiene su moneda como atributo y la caja es única. Los saldos consolidados se convierten con el TC **del documento**. Se replica ese modelo.
4. Cuenta por pagar: saldo y pagos en la moneda del documento (`OriginalAmount`/`PaidAmount` ya son del documento); para totales mezclados se convierte con el TC del documento (equivalente PEN). Sin diferencia de cambio en v1 (el antiguo tampoco la calcula).
5. UI de compra: selector de moneda, TC autollenado por fecha (servicio `exchangerate` existente, editable) y, al cambiar de moneda, reconvertir costos como hace el antiguo.
6. Compras antiguas: `Currency='PEN'`, `exchange_rate` NULL — sin backfill.

#### 3.2.1 Cómo maneja el sistema antiguo las cuentas y pagos en dólares (hechos verificados)
- `bank_accounts.currency_type_id`: cada cuenta bancaria tiene moneda (PEN/USD) y se muestra siempre como "Banco - USD - Nombre". La **caja es una sola** (`CAJA GENERAL`), sin moneda.
- `purchase_payments` guarda solo `payment` (monto) + método + referencia: **sin moneda ni TC**; el monto se entiende en la moneda de la compra. El destino (caja o cuenta) va en `global_payments`.
- **No valida** que la moneda de la cuenta coincida con la de la compra: es responsabilidad del usuario.
- Saldo de una cuenta = saldo inicial + ingresos − egresos + transferencias, sumando los montos tal cual (sin convertir por cuenta).
- Reportes consolidados (`FinanceTrait::calculateTotalCurrencyType`): convierten cada pago a la moneda pedida con el TC **de la compra** (USD→PEN multiplica, PEN→USD divide).
- Por pagar (`ToPay`): total, moneda y TC del documento; el pendiente se calcula en la moneda del documento.
- Consecuencia: una compra USD pagada en efectivo resta "dólares" de la caja única; el antiguo no separa por moneda.

**Aplicación en Tukifac:** `TenantBankAccount` ya tiene `Currency`. En Fase 2, al pagar una compra USD, el selector de destino mostrará la moneda de cada cuenta y **advertirá** (sin bloquear, como el antiguo) si no coincide con la del documento. El egreso se guarda en la moneda del documento con el TC del documento para la conversión de reportes.

### 3.3 Reportes (Fase 3)
Totales por moneda (mismo criterio ya decidido para el reporte de ventas: sin mezclar USD con PEN) + columna equivalente en PEN; CxP y reporte de compras; dashboard.

## 4. Orden y riesgo
| Fase | Qué | Riesgo | Depende de |
|---|---|---|---|
| 1 | Orden de compra (PEN/USD en el documento) | Bajo: tablas nuevas, sin efecto en stock/caja | V180 |
| 2 | Compras multimoneda (costo, caja, CxP, UI) | Medio-alto: toca dinero e inventario | Fase 1 + decisiones §5 |
| 3 | Reportes por moneda | Medio | Fase 2 |
Cada fase: commit separado en `main`, tests (Go + vitest), migración versionada, deploy backend primero.

## 5. Decisiones (11-nov-2026, tomadas por el dueño)
1. **Recepción**: 1 OC → 1 compra (recomendación aceptada). Recepción parcial fuera de v1.
2. **Cuentas en dólares**: como el sistema antiguo (§3.2.1): pago en la moneda del documento, cuenta con moneda, caja única, conversión en reportes con el TC del documento.
3. **TC de compra**: criterio del antiguo (TC **venta**, consulta por fecha de emisión, editable).
4. **Series**: la orden de compra tiene series como los demás documentos (§3.1.1).

## 6. Estado (11-nov-2026)
- **Fase 1 implementada y probada en la rama `feature/orden-compra`** (ver `FASE1_ORDEN_COMPRA_REPORTE.md`); no está en `main`.
- **Revisión de caja/bancos para la Fase 2 (hecha, solo lectura):**
  - El enrutamiento de un pago (`recordDirectedPayment`) es por método de pago → caja o cuenta; **ni el movimiento de caja ni el de banco guardan moneda ni tipo de cambio**, y el monto se guarda tal cual. `TenantBankAccount.Currency` existe pero ningún flujo la valida ni convierte.
  - Las ventas en USD ya funcionan igual hoy (guardan `Currency` + `ExchangeRate` en el documento y el cobro entra en la moneda del documento). Es exactamente el modelo del sistema antiguo, así que **la Fase 2 para caja/banco no cambia el motor de pagos**: basta con que la compra guarde moneda + TC y el egreso quede en la moneda del documento, como las ventas.
  - Lo que sí hay que convertir a soles en la compra USD: el costo del movimiento de stock y `purchase_price` del producto (inventario y kardex son en soles), y los totales mezclados de reportes/CxP/dashboard (con el TC del documento).
  - Riesgo a vigilar: los saldos por cuenta suman montos sin mirar moneda; una compra USD pagada a una cuenta PEN mezclaría monedas en el saldo. Por eso la advertencia (no bloqueo) al elegir destino con moneda distinta, ya propuesta en §3.2.1.
- **Pendiente:** correo de la orden (1.2), Fase 2 (compras multimoneda) y Fase 3 (reportes), y la aprobación del dueño para pasar la rama a `main`.
