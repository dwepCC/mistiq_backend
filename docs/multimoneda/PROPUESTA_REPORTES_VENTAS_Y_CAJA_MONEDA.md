# Reportes de ventas y de caja: valor original por moneda + conversión final a soles (propuesta, 11-nov-2026)

Pedido: en el reporte de ventas guardar/mostrar en qué moneda fue cada venta; en el reporte de caja mostrar también en qué moneda fue cada ingreso; mostrar el valor **original** (dólares o soles) y, **al final, una fila con la conversión a soles de todo**. Solo análisis y propuesta; nada implementado.

## 1. Estado actual (verificado en el código)

**Reporte de ventas** (`/reports/sales`, decisión del 19-oct-2026: opción A «sin conversión»):
- Cada venta ya guarda `currency` + `exchange_rate`, y la tabla/PDF/Excel ya traen la columna **Moneda** y los montos con el símbolo de **cada** venta (`S/` o `US$`).
- Los totales van **separados por moneda** (`by_currency`) y los cobros por **método + moneda** (`payment_totals`); nunca se suman soles y dólares.
- **Falta**: una columna «Equivalente en soles» por venta y la **fila/tarjeta final con todo convertido a soles**. Esa decisión A queda ahora **superada por tu pedido** (se reintroduce el total en soles, pero *sin ocultar* el valor original).

**Reporte de caja** (`/reports/cash` y los reportes de sesión de caja):
- Los movimientos de caja/banco **no guardan moneda ni TC**; el reporte pone `S/` fijo en todas las filas y en los resúmenes. Una venta en USD cobrada en efectivo entra con su monto en dólares **como si fueran soles** (el saldo y el arqueo mezclan monedas).
- La moneda sí se puede **derivar** de la venta o compra de origen (`sale_id` / `purchase_id` del movimiento); los movimientos manuales (sin documento) son en soles.

## 2. Diseño propuesto

### 2.1 Reporte de ventas
Columnas: … | **Moneda** | **Total (original)** | **TC** | **Equivalente S/** | …
- *Total (original)*: el valor de la venta en su moneda (`US$ 100.00` o `S/ 350.00`) — lo que ya se muestra.
- *TC*: el de la venta (el que quedó guardado al emitirla).
- *Equivalente S/*: total × TC para ventas en USD; igual al total en soles para PEN. Las notas de crédito restan, las anuladas no suman (mismas reglas de hoy).

Pie del reporte (tarjetas, PDF y Excel):
```
Total en soles (S/)              : S/ 12 400.00
Total en dólares (US$)           : US$ 3 200.00      (≈ S/ 10 976.00 al TC de cada venta)
TOTAL CONSOLIDADO EN SOLES       : S/ 23 376.00
```
Los cobros por método siguen separados por moneda y se agrega su equivalente en soles al final.

### 2.2 Reporte de caja (movimientos y sesiones)
Columnas: … | **Moneda** | **Monto (original)** | **TC** | **Equivalente S/** | …
- Cada fila muestra el valor original con su moneda (derivada de la venta/compra) y su equivalente en soles al TC del documento.
- Resumen (ingresos, egresos, neto) **por moneda** y, al final, **fila «Total en soles»** sumando los equivalentes. El **saldo y el arqueo de caja pasan a calcularse en soles** con el equivalente (hoy suman montos en dólares como soles).
- Movimientos manuales y de bancos sin documento de origen: soles.

### 2.3 Datos: cómo se obtiene el equivalente
| Opción | Qué es | Pros | Contras |
|---|---|---|---|
| **A. Derivado (recomendada para el primer paso)** | `moneda` y `TC` se leen de la venta/compra vinculada (join por `sale_id`/`purchase_id`); `equivalente = monto × TC` | **Sin migración**, sirve ya para el histórico, riesgo bajo, es solo lectura/reportes | Si luego se edita el documento, el equivalente se mueve; hay que cuidar los movimientos sin documento |
| **B. Snapshot** | Guardar `currency`, `exchange_rate` y `amount_pen` en cada movimiento de caja/banco/pago (migración V182 + backfill) | Es el diseño final del programa de multimoneda: cada movimiento conserva su equivalente | Migración sobre dinero; mayor impacto |
Recomendación: **A ahora** (reportes de ventas y caja, ya) y **B dentro de la Fase 3 del programa** (caja y bancos). Los reportes no cambian de aspecto al pasar de A a B.

### 2.4 Ventas en USD sin TC guardado
La auditoría de producción encontró **2 de 14** ventas en USD con `exchange_rate = NULL` (tenants `euro` y `parabrisasnegociaciones`). Para esas filas: usar el TC SUNAT **de la fecha de emisión** (caché `exchangerate`) marcado con `*` («TC estimado») y avisarlo al pie; si tampoco hay TC, la fila muestra el valor original, queda **fuera** del consolidado y el pie dice «N ventas en USD sin tipo de cambio no incluidas».

### 2.5 Exportaciones (PDF / Excel)
Mismas columnas (Moneda, original, TC, equivalente S/) y mismo pie. En Excel, el equivalente va como número (para sumar) y el consolidado como fila final con fórmula.

## 3. Alcance y fases sugeridas
| Paso | Qué | Riesgo |
|---|---|---|
| 1 | **Reporte de ventas**: columnas TC y Equivalente S/, tarjeta/fila final consolidada (derivado de la propia venta; backend agrega `total_pen` por fila y `by_currency[].sum_pen`; PDF y Excel) | Bajo |
| 2 | **Reporte de caja (movimientos)**: moneda/TC/equivalente derivados por documento; resumen por moneda + total en soles | Medio |
| 3 | **Sesión de caja** (detalle, cierre, arqueo, PDF): saldo y cierre en soles con el equivalente; desglose por moneda | Medio-alto (dinero) |
| 4 | Snapshot en movimientos (V182) y backfill — Fase 3 del programa de multimoneda | Alto |

## 4. Decisiones que necesito
1. ¿El **consolidado en soles** se muestra **siempre** o solo cuando hay más de una moneda en el filtro? (recomendado: siempre que haya al menos una venta en USD; si todo es soles basta el total actual).
2. ¿Aceptas el **TC estimado de la fecha de emisión** para las ventas en USD sin TC guardado (marcado con `*`), o prefieres dejarlas fuera del consolidado?
3. ¿Empiezo por el **paso 1 (reporte de ventas)** y luego caja?
