# Propuesta: multimoneda en todo el sistema (soles y dólares por conversión con el tipo de cambio SUNAT)

Fecha: 11-nov-2026. Base: análisis a fondo del sistema antiguo (`C:\xampp\htdocs\facturador-tukifac`) y de Tukifac. **Solo análisis y propuesta: nada de esto está implementado** (lo ya hecho para órdenes de compra y compras en USD vive en la rama `feature/orden-compra`; en §6 se indica qué hay que ajustar de ello).

## 0. La idea en una frase
**Todo el sistema trabaja en soles como moneda base.** No hay cuentas ni cajas en dólares: cada documento (venta, compra, cotización, orden) puede estar en soles o dólares, **guarda su moneda y su tipo de cambio (TC) de SUNAT**, y todo lo que se consolida (caja, bancos, saldos, reportes, kardex, contabilidad) se convierte a soles con ese TC. Los productos pueden tener precio y costo en soles o en dólares, y al venderlos o comprarlos se convierten a la moneda del documento con el TC.

## 1. Cómo funciona el sistema antiguo (hechos verificados en el código)

| Tema | Qué hace | Dónde |
|---|---|---|
| Productos | Cada producto tiene **una moneda** (`items.currency_type_id`) que aplica a su precio de venta y a su precio de compra. Hay un interruptor `set_global_purchase_currency_items` ("usar la moneda del documento en compras") y `change_currency_item` (permite cambiar la moneda del ítem al facturar). | `Item.php`, `Configuration.php`, `purchases/partials/item.vue`, `documents/partials/item.vue` |
| Documento (venta, nota de venta, cotización, compra, orden) | Guarda `currency_type_id` + `exchange_rate_sale` (TC **venta**, por fecha de emisión, editable). Al agregar un producto cuya moneda difiere de la del documento, el precio se **convierte en el formulario**: PEN→USD divide, USD→PEN multiplica (`calculateRowItem`). El backend guarda lo que llega ya en la moneda del documento. | `resources/js/helpers/functions.js`, `mixins/functions.js` (`searchExchangeRateByDate` → `/services/exchange/{fecha}`) |
| Pagos | `*_payments.payment` es solo un monto **en la moneda del documento**; sin moneda ni TC propios. | `PurchasePayment.php`, `SaleNotePayment`, … |
| Caja | **Una sola caja en soles**: el saldo final recorre los documentos y convierte cada USD a soles con **el TC del documento** (`total × exchange_rate_sale`). Ventas, notas de venta, gastos y compras se tratan igual (`CalculeTotalOfCurency`). | `CashController.php` (≈ líneas 240-290, 676-880, 1165) |
| Bancos | La cuenta tiene moneda como etiqueta, pero ningún flujo valida ni separa saldos por moneda; los consolidados convierten con `calculateTotalCurrencyType` (USD→PEN multiplica, PEN→USD divide, con el TC **del documento**). | `FinanceTrait.php` |
| Reportes / Dashboard | Todo se normaliza a soles con el TC de cada documento: kardex valorizado, dashboard (ventas, compras, utilidad), reportes de ventas/compras, por pagar. Las **notas de crédito restan**. | `InventoryValuedKardex.php`, `DashboardUtility.php`, `ToPay.php` |
| TC | Tabla de tipos de cambio por fecha (SUNAT, venta) con consulta automática y registro manual. | `ExchangeRateController.php` |
| Diferencia de cambio | **No** se calcula. | — |

**Debilidades del antiguo que NO hay que copiar:** (a) la conversión de precios vive solo en el navegador (nada la valida en el servidor); (b) el saldo de caja convierte por documento pero el desglose por método de pago suma los pagos sin convertir (los dos totales pueden no coincidir); (c) se recalcula todo cada vez desde el documento en lugar de guardar el importe en soles cuando ocurre el movimiento.

## 2. Estado de Tukifac hoy (hechos verificados)

| Módulo | Estado |
|---|---|
| **Productos** | **Sin moneda**: `sale_price`, `purchase_price`, presentaciones, unidades de venta y precio por sucursal son implícitamente soles. |
| **Ventas y cotizaciones** | Tienen `Currency` + `ExchangeRate` (selector PEN/USD y TC SUNAT automático). **Defecto grave encontrado:** al cambiar a USD el carrito **no convierte los precios**: un producto de S/ 100 pasa a "US$ 100" (solo cambia la etiqueta). `ExchangeRate` se usa para umbrales fiscales (retención, detracción) y SIRE, no para convertir. |
| Autorización de precios | `ValidateAuthorizedPrices` compara el precio de la línea con el de catálogo (soles): con precios convertidos habría que compararlos ya convertidos. |
| **Compras / orden de compra** | (rama `feature/orden-compra`) ya con moneda + TC, costo de stock en soles, CxP, contabilidad, dashboard y reporte con TC. |
| **Pagos, caja y bancos** | Los movimientos **no tienen moneda**: una venta o compra en USD mueve la caja con el monto en dólares como si fueran soles (arqueo y saldos de caja/banco mezclan monedas). Las cuentas bancarias tienen `Currency` sin uso funcional. |
| **CxC / CxP** | Saldos en la moneda del documento; CxP ya consolida en soles con el TC (rama). |
| Inventario / kardex | Costos en soles (las compras en USD ya convierten). Sin conversión en la venta (costo vs. precio en USD). |
| Reportes / dashboard | Compras convertidas (rama); ventas del dashboard sin convertir; el reporte de ventas se decidió "por moneda" (sin conversión). |
| **POS / Tukichef / tienda virtual** | Solo soles. |
| Fiscal / SIRE / contabilidad | Usan el TC cuando existe; facturas en USD ya soportadas por SUNAT. |
| TC | Módulo `exchangerate` con caché central (SUNAT venta/compra, respaldo del día anterior, edición manual). |

## 3. Principios de diseño
1. **Moneda base única: PEN.** Dólares solo existen como moneda **del documento** y como moneda **de precio/costo** de un producto.
2. **Snapshot en cada documento y en cada movimiento:** se guardan moneda, TC y el **importe en soles al momento** (no se recalcula con TCs futuros). Es la mejora clave sobre el antiguo.
3. **Una sola función de conversión** en el servidor (`pkg/fx`): reglas de redondeo y de qué TC aplicar en un solo lugar; el frontend solo previsualiza.
4. **El servidor es la autoridad**: valida moneda, TC y la coherencia precio-catálogo/TC; nada depende del navegador.
5. **Sin cuentas en dólares**: las cuentas bancarias y la caja son en soles; pagar/cobrar una operación en USD mueve el **equivalente en soles**.
6. **Compatibilidad**: todo lo existente es PEN; no se reescribe historia.

## 4. Diseño propuesto por capa

### 4.1 Catálogo
- `tenant_products.currency` (moneda del **precio de venta**, `PEN` por defecto) y `purchase_currency` (moneda del **costo**, `PEN` por defecto). Presentaciones, unidades de venta y precio por sucursal **heredan** la moneda del precio del producto (sin columnas nuevas en esas tablas). *(El antiguo tiene una sola moneda por producto; separar venta/costo permite importar en USD y vender en soles sin trucos.)*
- UI: selector de moneda junto al precio y al costo en el formulario de producto, la importación Excel y la lista (símbolo en la columna).
- El valor guardado **no cambia con el TC**: un producto en USD conserva su precio en USD; lo que varía es su equivalente.

### 4.2 Documentos (venta, cotización, orden, compra)
- Campos existentes `Currency` + `ExchangeRate` (ya en venta, cotización, compra, orden). TC = SUNAT por fecha de emisión (módulo `exchangerate`), editable con validación de rango contra el oficial (aviso si difiere > X %).
- **Conversión al armar el documento** (`fx.Convert`): precio de línea = precio de catálogo en su moneda → moneda del documento. Al cambiar la moneda del documento se **reconvierten las líneas** (como el antiguo y como ya hace el formulario de compra). Líneas con precio manual se reconvierten igual.
- **Autorización de precios en servidor**: se compara la línea con el precio de catálogo **convertido con el TC del documento** (con tolerancia de redondeo).
- Guardar además `total_pen` (equivalente en soles al TC del documento) para sumar en SQL sin recalcular.

### 4.3 Pagos, caja y bancos (la parte que más cambia)
- Los pagos se registran **en la moneda del documento** (como el antiguo) y el movimiento de caja/banco guarda: `amount` (moneda del documento), `currency`, `exchange_rate` y **`amount_pen`**. El saldo físico y los arqueos suman **`amount_pen`**.
- Pagar una CxP/CxC en un día posterior: el efecto en caja se valúa con el **TC del documento** (igual que el antiguo; simple y sin diferencia de cambio). Alternativa a decidir: TC del día del pago + diferencia de cambio (ver decisiones).
- Cuentas bancarias: solo en soles (se oculta/valida la moneda; auditoría previa de cuentas USD existentes). Se eliminan los avisos de "cuenta en otra moneda" que añadí en la rama.

### 4.4 Inventario y costos
- Kardex y valorización siempre en soles: `UnitCost` = costo del documento × TC (ya implementado en compras). Un producto con costo en USD aporta costo en soles con el TC de la compra; su `purchase_price` de catálogo conserva su moneda.
- Utilidad: costo (soles, kardex) vs. venta en soles (`total_pen` / líneas × TC) → sin mezclar monedas.

### 4.5 Reportes, dashboard, contabilidad
- Todo suma `*_pen`. Se ofrece además el desglose **por moneda** (ya hecho en el reporte de compras) y el consolidado en soles. Notas de crédito restan. Exportaciones PDF/Excel con columnas Moneda, TC, importe y equivalente en soles.
- Contabilidad/SIRE ya usan TC; pasan a leer `total_pen`.

### 4.6 Tipo de cambio (reglas)
- Fuente: `exchangerate` (SUNAT), caché central, respaldo del día anterior, edición manual auditada.
- Fecha: la de emisión del documento; el TC queda congelado en él.
- **A decidir:** TC **venta** para todo (como el antiguo y Tukifac hoy) o la práctica contable usual (**compras → TC venta; ventas → TC compra**). Conviene confirmarlo con el contador.

### 4.7 POS, Tukichef y tienda virtual
- Venden en **soles**: un producto con precio en USD se convierte con el **TC del día** al mostrarse y al cobrarse (redondeo a centavos), con cache del TC diario. Sin selector de moneda en estos canales (fase posterior si se pide).

## 5. Plan por fases (cada una con su migración versionada, tests y commit separado)
| Fase | Contenido | Riesgo |
|---|---|---|
| **0. Auditoría en producción (solo lectura)** | Cuántos documentos USD hay por módulo, cuántas cuentas bancarias en USD, productos con precios que parecen dólares; sirve para decidir el backfill. | Nulo |
| **1. Base** | `pkg/fx` (conversión, redondeo), columnas de moneda en producto (`currency`, `purchase_currency`), UI de catálogo e importación. | Bajo |
| **2. Ventas y cotizaciones** | Conversión de líneas, reconversión al cambiar moneda, autorización de precios convertida, `total_pen`. **Corrige el defecto actual** (USD que solo relabela soles). | **Alto** (núcleo de facturación) |
| **3. Caja y bancos** | `currency`, `exchange_rate`, `amount_pen` en movimientos de caja/banco/pagos; arqueo y saldos en soles; backfill de los movimientos históricos de documentos USD; ajuste de compras (§6). | **Alto** (dinero) |
| **4. CxC/CxP, reportes, dashboard, kardex/utilidad** | Sumas en `*_pen` en todo el sistema, desgloses por moneda, exportaciones. | Medio |
| **5. POS/Tukichef/tienda** | Conversión con TC del día para productos en USD. | Medio |
| **6. (Opcional) diferencia de cambio** | Cobros/pagos con TC del día y asiento de diferencia. | Medio |
**Recomendación inmediata (independiente del programa):** el defecto de ventas en USD debería corregirse primero (o bloquear USD en ventas hasta tenerlo), porque hoy puede emitir comprobantes con importes erróneos en dólares.

## 6. Qué hay que ajustar de lo ya hecho (rama `feature/orden-compra`)
- **Pagos de compras en USD**: hoy el egreso de caja/banco se guarda en dólares; con este modelo debe guardarse el **equivalente en soles** (`amount_pen`) → se resuelve en la Fase 3.
- **Avisos de "cuenta en otra moneda"** del modal de pago de CxP: dejan de aplicar (no habrá cuentas en USD) y se reemplazan por la información "se descontará S/ X (TC Y)".
- El resto (TC en orden/compra, costo de kardex en soles, CxP/contabilidad/dashboard/reporte con TC) **se mantiene**. La orden de compra no mueve dinero y no cambia.

## 7. Decisiones que necesito de ti
1. **Moneda por producto**: ¿una sola (como el antiguo) o dos (precio de venta y costo, recomendado)?
2. **TC**: ¿venta para todo (como hoy) o compra para ventas y venta para compras? (confirmar con el contador).
3. **Cobros/pagos posteriores**: ¿se valúan con el **TC del documento** (como el antiguo, recomendado para v1) o con el del día + diferencia de cambio?
4. **Cuentas bancarias**: ¿alguna cuenta existente está en USD? Si sí, ¿se migran a soles o se mantienen solo informativas?
5. **POS/Tukichef/tienda**: ¿solo soles con conversión (recomendado) o también venta en dólares?
6. **Defecto de ventas en USD**: ¿lo corrijo ya como hotfix (Fase 2 recortada) o espero el programa completo?
7. **Orden de trabajo**: ¿primero pasar `feature/orden-compra` a `main` y luego este programa, o este programa sobre la misma rama?
