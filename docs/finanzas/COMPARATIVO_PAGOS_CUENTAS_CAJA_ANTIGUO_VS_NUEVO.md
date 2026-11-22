# Métodos de pago, cuentas bancarias y caja: sistema antiguo vs Tukifac (análisis y propuesta)

Fecha: 11-nov-2026. Solo lectura de ambos códigos; nada implementado. Fuentes: `C:\xampp\htdocs\facturador-tukifac` (`modules/Finance`, `Facturalo::savePayments`, `invoice.vue`) y `backend_principal/internal/cashbank` + `frontend_tenant`.

## 1. Cómo funciona el sistema antiguo

**Idea central:** el *método de pago* solo clasifica («cómo pagó»); el *destino* decide qué saldo sube («adónde fue el dinero»).

- Cada línea de pago de un comprobante lleva: método, **destino** (Caja general = la sesión de caja abierta del usuario, o una cuenta bancaria), referencia, fecha, monto, voucher adjunto.
- Todo movimiento de dinero se registra en un libro único polimórfico `global_payments` (destino Caja o BankAccount + el pago de origen).
- **El saldo no se guarda: se calcula** (`FinanceTrait`): saldo inicial + ingresos − egresos ± transferencias, convirtiendo cada pago a la moneda pedida con el TC del documento.
- Dos catálogos de métodos: `payment_method_types` (ventas/compras/ingresos; flags `is_cash`, `is_credit`, `number_days`) y `expense_method_types` (gastos: «Caja chica», tarjeta crédito, tarjeta débito). «Caja chica» fuerza destino Caja.
- Cuenta bancaria: banco (catálogo), descripción, número, CCI, **moneda**, saldo inicial, `show_in_documents` (imprimir en el PDF), establecimiento opcional.
- Transferencias entre Caja↔Banco↔Banco (dos filas en `transfer_account_payments`, montos origen/destino independientes → permite entre monedas).
- Condición de pago separada del método: contado / crédito / crédito en cuotas; los créditos se cobran después con pagos que eligen su propio destino.
- La moneda de la cuenta solo sirve para filtrar el reporte de balance y como etiqueta; al cobrar nunca se compara con la moneda del documento.

**Defectos del antiguo (no copiar):** `charge`/`has_card`/`status` sin efecto; el destino no depende del método (efectivo→banco, transferencia→caja sin aviso) y se ofrecen cuentas inactivas, de otra moneda o sucursal; validaciones casi solo en el navegador; el POST que vincula la venta con la caja es aparte y puede fallar en silencio; efectivo = método `'01'` o el texto «Efectivo» hardcodeado; cierre de caja calculado con totales de documentos (infla el efectivo con ventas a banco o crédito); balance por período pierde el saldo previo; dos implementaciones de saldo que no coinciden; transferencias sin validar saldo, sin transacción y sin anulación; sin conciliación; TC del día de emisión en vez del del cobro.

## 2. Cómo funciona Tukifac hoy

- El usuario elige **solo el método**; el método fija el destino de forma global (`tenant_payment_methods.destination_type` = caja | cuenta, + `bank_account_id`). Todo cobro exige una sesión de caja abierta del propio usuario.
- Efectivo → `TenantCashMovement` de la sesión; cuenta → `TenantBankMovement` + `balance` de la cuenta **guardado e incremental** (ya en soles tras la auditoría), con snapshot de moneda/TC/soles, reversiones atómicas en el mismo servidor, arqueo y cierre de sesión en soles.
- Sin transferencias, sin conciliación, sin catálogo de bancos, un solo método por compra, mismo catálogo de métodos para ingresos y egresos, moneda de cuenta solo etiqueta.

## 3. Brechas y riesgos de Tukifac (ordenados por gravedad)

| # | Brecha / riesgo | Dónde |
|---|---|---|
| 1 | **Movimiento perdido en silencio**: método de banco sin cuenta resoluble → el pago queda en `tenant_sale_payments` pero sin movimiento ni saldo (devuelve `nil`); Tukichef además ignora el error (`_ =`) | `cashbank_service.go:670`, `restaurant_service.go:1576,1805` |
| 2 | Doble fuente de verdad del destino: FK `bank_account_id` vs texto legado `payment_method` | `resolveAccountForPaymentMethod` |
| 3 | «Efectivo» se detecta por nombre en reportes pero por `destination_type` al registrar: un método personalizado con destino caja crea movimiento de caja que el arqueo no cuenta | `payment_method_report.go:91` vs `recordDirectedPayment` |
| 4 | `bankDeltaPEN` con `AmountPEN` nulo (USD sin TC) suma dólares como soles | `financial_reversal.go` |
| 5 | `AddBankMovement` no valida que la cuenta exista/esté activa ni va en transacción | `cashbank_service.go:1211` |
| 6 | Saldo solo incremental, sin saldo inicial como movimiento ni forma de detectar desvíos | — |
| 7 | **No se puede elegir destino por pago** (con 2 cuentas BCP/BBVA todas las transferencias van a una) | método→cuenta global |
| 8 | **No hay transferencias** ni depósito de caja a banco ni entre cuentas | — |
| 9 | Moneda de cuenta decorativa (la UI permite USD/EUR pero el saldo va en soles) — caso real: «CTA BBVA DOLARES» de `euro` | `BankPage.tsx:441` |
| 10 | Métodos sin distinción ingreso/egreso, sin «requiere referencia», sin comisión | — |
| 11 | Pago de compra con un solo método; cobro de CxC sin referencia/vuelto; pagos digitales exigen sesión de caja | — |
| 12 | Sin catálogo de bancos, sin «mostrar en comprobantes» por cuenta (el CCI se imprime siempre) | — |

## 4. Propuesta (mejorar sin empeorar)

**Principios:** (a) conservar lo mejor de Tukifac: saldo guardado (rápido), todo en el servidor y transaccional, snapshot de moneda, reversión por movimiento; (b) tomar del antiguo lo valioso: *destino por pago*, *transferencias*, *cuenta con moneda real*, *banco enlazado*, *mostrar en comprobantes*; (c) no copiar sus defectos (destino libre sin reglas, validación en el navegador, efectivo por nombre, saldo calculado al vuelo).

### Fase A — Integridad (bajo riesgo, sin cambios visibles)
1. Un método con destino banco sin cuenta resoluble **falla con error claro** (no devuelve `nil`); Tukichef deja de ignorar el error.
2. Una sola fuente de verdad: el FK `bank_account_id`; el texto legado queda solo como respaldo de lectura y se migra.
3. «Efectivo» se define por el destino del método (`destination_type = cash`), no por el nombre, en arqueo y reportes.
4. `bankDeltaPEN` con TC desconocido: error o marca explícita, nunca dólares como soles.
5. `AddBankMovement` valida cuenta existente/activa dentro de una transacción.
6. **Verificador de cuadre** (solo lectura): compara `balance` de cada cuenta contra saldo inicial + suma de sus movimientos, para detectar desvíos antes de que el cliente los vea.

### Fase B — Destino por pago (lo más pedido)
- `payments[]` acepta opcionalmente `destination` (caja o id de cuenta). Si no viene, se usa el destino por defecto del método (comportamiento actual: nada se rompe).
- La pantalla muestra «Destino» **preseleccionado** con el del método y solo ofrece destinos válidos (a diferencia del antiguo): cuentas activas de la sucursal; Caja solo para métodos que admiten efectivo; filtro por moneda compatible.
- Validado en el servidor.

### Fase C — Cuentas con moneda real + transferencias
- El saldo de la cuenta se mueve **en la moneda de la cuenta** (lógica del antiguo, pero guardada): misma moneda → monto tal cual; USD→cuenta PEN: × TC; PEN→cuenta USD: ÷ TC del día. El snapshot en soles se mantiene para reportes; los consolidados convierten con el TC.
- Transferencias Caja↔Banco y Banco↔Banco: una operación atómica con dos movimientos enlazados (`transfer_id`), validación de saldo y de cuentas distintas, TC si cambian de moneda, **anulable** (reversión enlazada). Incluye «depositar la recaudación al banco» al cerrar caja.
- Saldo inicial como movimiento de apertura tipado.

### Fase D — Catálogos y configuración
- Catálogo de bancos y vínculo cuenta→banco; «mostrar en comprobantes» y sucursal por cuenta.
- Un solo catálogo de métodos con «aplica a: ingresos / egresos / ambos» (en lugar de dos tablas), y flags opcionales: requiere referencia, comisión (para tarjetas/pasarelas).
- Pago mixto en compras; referencia y vuelto en cobros de CxC.

### Orden recomendado y riesgo
A (1–2 días, sin riesgo funcional) → B (visible, retrocompatible) → C (toca saldos: exige migración y pruebas en MySQL; hacerla después del despliegue de la multimoneda y con el script de saldos resuelto) → D.

### Decisión que cambia respecto a lo anterior
Se había decidido «solo cuentas en soles». El caso real de `euro` («CTA BBVA DOLARES») y el comportamiento del antiguo (cuenta con moneda) indican pasar a **cuentas con moneda real** (Fase C). Las cuentas existentes quedan en PEN; cada cliente decide cuáles marcar como USD (sin migrar saldos automáticamente).

## 5. Reglas del antiguo que se preservan
Pagos mixtos con suma ≤ total; crédito/cuotas con cobros posteriores que eligen su destino; vuelto solo en efectivo y saldo por el neto; caja = sesión por usuario con apertura/arqueo/cierre; exclusión de documentos anulados sin borrar historial; notas de crédito restan; transferencias con trazabilidad; un libro único de movimientos para reportes; comprobantes que imprimen solo las cuentas marcadas; voucher por pago (pendiente en Tukifac).

## 6. Estado de implementación (11-nov-2026)

**Fase A (hecha):** método a banco sin cuenta resoluble → error claro (producción: 0 de 796 tenants afectados, revisado en solo lectura); Tukichef registra en log el error del pago en vez de ignorarlo; saldo de la cuenta nunca suma dólares como soles (USD sin TC se rechaza al cobrar; reversiones y devoluciones por nota de crédito usan el equivalente en soles); movimientos manuales de banco validan cuenta existente/activa y tipo; solo el método Efectivo puede enviarse a caja (producción: 2 tenants con un método personalizado a caja, no se tocan); verificador `GET /api/cashbank/bank-accounts/reconcile` (saldo guardado vs movimientos, con «saldo inicial implícito»).

**Fase B (hecha):** cada pago acepta `destination_account_id` (ventas, POS y cobros de CxC); sin él se usa la cuenta del método. El servidor valida: el efectivo no admite cuenta, la cuenta debe existir, estar activa y llevarse en soles. La pantalla muestra «Destino» solo en métodos a cuenta y cuando hay más de una cuenta posible; preseleccionada con la cuenta del método.

**Pendiente:** Fase C (cuentas con moneda real y transferencias) y Fase D (catálogos). Nota para el despliegue: la anulación de una venta histórica en USD cobrada a banco ANTES de la corrección restará el equivalente en soles de una cuenta a la que se sumaron dólares; aplicar primero el ajuste de saldos (`AJUSTE_SALDOS_BANCARIOS_USD.sql`) en esos tenants.

**Fase C (hecha, 11-nov-2026):**
- **Cuentas con moneda real.** El saldo de una cuenta se mueve en la moneda de la cuenta (`tenant_bank_movements.account_amount`, calculado por el hook al crear el movimiento): cuenta en soles → equivalente en soles; cuenta en dólares → el importe si el documento es USD, o el importe en soles ÷ TC oficial del día si es PEN; movimiento manual en una cuenta USD → ya viene en dólares. Falta de TC → se rechaza el cobro (no se suman soles como dólares). La moneda de una cuenta solo se elige al crearla (PEN/USD; EUR ya no se admite) y no se puede cambiar después. Las reversiones copian el importe del original (el saldo vuelve exacto).
- **Transferencias** (`POST/GET /api/cashbank/transfers`, `POST /transfers/:id/void`): Caja↔Cuenta y Cuenta↔Cuenta, atómicas (dos movimientos enlazados por `transfer_id`), validan saldo (cuenta y efectivo de la sesión), origen≠destino, cuentas activas y sesión de caja abierta; entre monedas usan el TC indicado u oficial del día; anulables con movimientos inversos. Pantalla: botón «Transferir», panel «Transferencias recientes» con anulación, y estado de cuenta en la moneda de la cuenta.
- **Migración V183** (`tenant_account_transfers`, `account_amount`, `transfer_id`; backfill por tramos). **IMPORTANTE para el despliegue:** el backend nuevo falla en cualquier cobro a cuenta bancaria en un tenant sin V183 (columna `account_amount` inexistente) → ejecutar `migrate-fleet` completo y verificar por tenant ANTES de reiniciar el backend.
- Pendiente: Fase D (catálogo de bancos, mostrar cuenta en comprobantes, métodos ingreso/egreso, pago mixto en compras) y el script de ajuste de saldos de los 3 tenants (euro: decidir si «CTA BBVA DOLARES» pasa a ser cuenta en USD con la nueva lógica).

**Fase D (hecha, 11-nov-2026):**
- **Catálogo de bancos** (`tenant_banks`, `GET/POST/PUT /api/cashbank/banks`): 21 entidades sembradas (bancos, cajas, Yape, Plin); la cuenta se enlaza con `bank_id` (el nombre queda en `bank_name` para impresión y datos antiguos); las cuentas existentes se enlazan por coincidencia exacta o alias (BCP, BBVA…). En la pantalla de cuentas: selector de banco + «Agregar banco al catálogo».
- **Métodos de pago**: `applies_to` (cobros / pagos / ambos) y `requires_reference`. Se imponen **en el servidor**: un método solo de pagos no sirve para cobrar y viceversa; un método que exige referencia rechaza el cobro sin N° de operación (ventas, POS y cobros de CxC). Las pantallas filtran los métodos según el sentido (ventas/POS/CxC = cobros; compras/CxP = pagos).
- **Compras**: el método de pago sale ahora del catálogo (antes era una lista fija).
- **Cobros de CxC**: guardan el N° de operación (`reference`).
- **«Mostrar en comprobantes»** ya existía a nivel de empresa (`ReceiptBankAccountIDs`, pantalla de recibos/billetera): no se duplicó por cuenta.
- **No hecho:** pago mixto (varios métodos) en compras — hoy una compra al contado usa un método; cambiarlo toca los reportes que clasifican la compra por su método y se deja para un cambio aparte con su propio análisis. Vuelto en cobros de CxC no aplica (el cobro no puede superar el saldo).
- **Migración V184** (`tenant_banks`, `bank_id`, `applies_to`, `requires_reference`): ejecutar `migrate-fleet` completo ANTES de reiniciar el backend.

**Pago mixto en compras (hecho, 11-nov-2026):** una compra puede pagarse con 2 o más métodos (`payments: [{method, amount, reference}]`, la suma debe igualar el total; con una sola línea equivale a `payment_method`). Se registra como cuenta por pagar **saldada** con un `tenant_purchase_payments` por línea (marcados con la nota «Pago al registrar la compra») y un egreso de caja/banco por línea, es decir, con el MISMO modelo que un pago a proveedor: los reportes de caja/movimientos, el arqueo y la anulación (`Void` revierte efectivo por `purchase_id` y banco por referencia) no necesitaron cambios. No se guardó un método «mixto» en `tenant_purchases.payment_method` a propósito: varios reportes clasifican la compra por ese campo y mostrarían el total completo bajo un método falso. La lista y el detalle de compras traen `payments_upfront` y rotulan «Mixto (A + B)». Sin migración nueva.

**Auditoría y pruebas visuales (11-nov-2026):** revisión independiente del código de las fases A–D + recorrido real por pantalla (nota de venta en USD con destino a otra cuenta, POS con destino, cobro de CxC en USD con N° de operación, transferencia caja→banco, compra con pago mixto y su anulación, reportes de ventas y de caja) con la BD local en `sql_mode` estricto. Corregido tras la revisión: las patas de una transferencia ya no se pueden revertir sueltas; la anulación de una transferencia es condicional (la doble anulación simultánea no duplica) y las transferencias bloquean la fila de la cuenta/sesión al validar saldo; los inversos conservan el equivalente en soles; los movimientos manuales en cuenta USD exigen TC del día y los reportes muestran el equivalente en soles de esas patas; `AddMovement` es transaccional; la devolución por NC a cuenta USD sin TC se rechaza; las líneas del pago mixto deben ser métodos de efectivo/cuenta; el selector de destino admite cuentas en USD; el modal de transferencia se reinicia al abrir y evita doble envío; los KPI de compras del dashboard excluyen anuladas.
**Observaciones abiertas:** (1) los totales «Ingresos/Egresos» del reporte de caja incluyen las patas de transferencias y sus anulaciones (el arqueo es correcto, pero esos totales no distinguen movimientos internos); (2) el ticket imprime «$» (no «US$») en documentos en dólares; (3) `requires_reference` no se valida en pagos a proveedor, Tukichef ni compra con método único; (4) cobros de CxC concurrentes pueden exceder el saldo (anterior a estas fases); (5) la compra con pago mixto aparece en el reporte de sesión como «pago_proveedor» y no como «compra» (totales correctos).

**Observaciones 1, 2 y 4 resueltas (11-nov-2026):** (1) el reporte de movimientos de caja excluye las transferencias internas de «Total ingresos/egresos» y las muestra aparte («Incluye transferencias internas: +S/ X / −S/ Y»); el neto y el saldo físico las siguen incluyendo (el efectivo realmente se mueve); (2) los tickets, PDF A4, cotizaciones y la impresión térmica rotulan «US$» en vez de «$»; (4) el cobro de CxC bloquea la venta (`FOR UPDATE`) y vuelve a leer el saldo dentro de la transacción: dos cobros simultáneos que juntos superan el saldo ya no pasan ambos (probado 3 veces con peticiones paralelas reales). Siguen abiertas la 3 (referencia obligatoria en CxP/Tukichef/compra con método único) y la 5 (etiqueta «pago_proveedor» de la compra mixta en el reporte de sesión).

**Segunda auditoría (3 revisores independientes: flujos afectados, barrido de importes, riesgos de despliegue) — 11-nov-2026.**
Corregido: Tukichef ya no traga los errores nuevos del cobro (rollback con mensaje claro; solo se tolera la falta de sesión de caja); migraciones V180–V184 reescritas con estructuras mínimas (solo `ADD COLUMN`/`CREATE INDEX`/`CREATE TABLE`: verificado emitiendo el DDL real sobre un esquema antiguo en MariaDB, sin ningún `MODIFY`); la conexión de migración usa `lock_wait_timeout=20`/`innodb_lock_wait_timeout=20` (antes un ALTER esperaba un año el lock de metadatos); la verificación física de esquema cubre V180/V183/V184; el script de saldos lleva tope de id y orden seguro; dashboard de Tukichef suma en soles; el KPI de caja del dashboard excluye transferencias; reporte de notas de crédito/débito en soles; avisos de moneda del pago a proveedor corregidos; la pantalla de caja de Tukichef muestra el importe en la moneda de la cuenta.
**Abiertas (ver informe en el chat):** detalle de la sesión de caja (pantalla/PDF) rotula «S/» importes de documentos en USD; sin compuerta por versión de esquema (un backend nuevo contra un tenant sin migrar falla en cada cobro: el deploy debe garantizar «0 tenants pendientes» antes de reiniciar); el deploy puede salir en verde con tenants omitidos por backoff/lock; V182–V184 no se validaron en MySQL 8 real (solo MariaDB 10.4); decisión pendiente de la cuenta «CTA BBVA DOLARES» de euro y ejecución del script de saldos; `requires_reference` parcial; pago mixto de compras etiquetado «pago_proveedor» en el reporte de sesión.

**Detalle de la sesión de caja en soles (resuelto, 11-nov-2026):** `GetSessionReport` convierte a soles TODAS las filas y totales de documentos en USD con el TC del documento (o el del día de emisión): crédito generado, CxP generada, ventas, cobros de CxC, compras, pagos a proveedor, movimientos de caja (snapshot `amount_pen`) y devoluciones. Las filas traen además `currency` y `original_amount` (la pantalla muestra «US$ x al TC del documento»). Así las tarjetas «Total ingresos caja / Total egresos / Saldo final» coinciden con el panel de moneda y con el arqueo. El PDF/Excel usan los mismos campos.
