# Informe de compatibilidad USD — TukiFac Premium

**Fecha:** 2026-06-19  
**Alcance:** Ventas en dólares (USD) con tipo de operación `0101` (Venta interna)  
**Objetivo:** Validar soporte de punta a punta antes de habilitar USD en producción.

---

## Resumen ejecutivo

| Área | Estado | Listo producción USD |
|------|--------|----------------------|
| Nuevo Comprobante (registro + SUNAT) | ✅ Implementado Fase A | Sí |
| POS | ⚪ Sin cambios (solo PEN) | N/A |
| Impresión / PDF comprobante | ✅ Parcial | Sí (montos en moneda venta) |
| Retención IGV operativa | ✅ Con conversión TC | Sí (requiere TC para umbral) |
| Reportes de ventas | ⚠️ S/ hardcodeado | Revisar antes de mezclar monedas |
| Caja / movimientos | ⚠️ Asume soles en UI | Revisar |
| Notas crédito / débito | ⚠️ Hereda moneda origen | Parcial |
| Kardex / inventario | ⚪ Valorización en unidades | No afectado por moneda venta |
| Utilidades / financieros | ⚠️ Agregados sin conversión | No listo multi-moneda |

**Recomendación:** Habilitar USD en **Nuevo Comprobante** para clientes que facturan en dólares. Para **reportes consolidados en soles**, planificar Fase B (conversión o columnas PEN/USD).

---

## 1. Nuevo Comprobante (SalesRegisterPage)

| Funcionalidad | Estado |
|---------------|--------|
| Selector moneda PEN/USD | ✅ |
| Tipo operación solo `0101` | ✅ |
| TC automático por fecha emisión (apiperu.dev) | ✅ |
| TC manual si falla consulta | ✅ |
| Creación venta sin bloquear por TC | ✅ |
| Persistencia `currency`, `exchange_rate`, `operation_type_code` | ✅ |
| Totales e ítems en moneda seleccionada | ✅ |
| Envío Lycet `tipoMoneda` + `tipoOperacion` | ✅ |

---

## 2. Backend / Facturación

| Componente | Comportamiento USD |
|------------|-------------------|
| `tenant_sales.currency` | PEN o USD |
| `tenant_sales.exchange_rate` | Opcional; referencia fiscal |
| `billing_service` → `tipoMoneda` | Desde `sale.Currency` |
| `billing_service` → `tipoOperacion` | Desde `sale.OperationTypeCode` (default 0101) |
| Leyenda 1000 | Construida en USD si aplica |
| XML Lycet `tipoCambio` | No se envía (Greenter Invoice no lo modela) |

---

## 3. Retención IGV (operativa)

- Umbral S/ 700 evaluado con **equivalente en soles**: `total_usd × exchange_rate`.
- Si USD sin TC, la retención no aplica automáticamente (mensaje explícito).
- Retención **no va al XML** de factura (igual que antes).

---

## 4. POS

- Sin selector de moneda; siempre PEN.
- Sin impacto en flujo rápido.

---

## 5. Reportes de ventas

**Archivos:** `SalesReportPage.tsx`, `SalesByProductReportPage.tsx`

| Hallazgo | Riesgo |
|----------|--------|
| Totales y tarjetas muestran `S/` fijo | Medio — mezcla USD+PEN suma sin convertir |
| Backend agrega `total` sin normalizar moneda | Medio |

**Acción Fase B:** Agrupar por moneda o convertir a PEN con TC de cada venta.

---

## 6. Caja y bancos

**Archivos:** `CashReportPage.tsx`, `cashbank` backend

| Hallazgo | Riesgo |
|----------|--------|
| Movimientos registrados en monto de la venta (USD si venta USD) | Medio |
| UI reporte caja en `S/` | Alto si hay ventas USD en sesión |
| Cuadre de caja física en soles vs ventas USD | Operativo |

**Acción Fase B:** Etiquetar movimientos con moneda; no mezclar en un solo total PEN.

---

## 7. Notas de crédito / débito

| Hallazgo | Estado |
|----------|--------|
| NC copia `Currency` de venta origen | ✅ |
| Payload nota usa moneda origen | ✅ |
| UI emisión NC | ⚠️ Verificar símbolo en pantallas |

---

## 8. Kardex e inventario

| Hallazgo | Estado |
|----------|--------|
| Stock en unidades, no en moneda | ✅ Sin cambio |
| Costo promedio / valorización | ⚪ Catálogo en PEN habitual |

Las ventas USD no alteran cantidades de kardex.

---

## 9. Utilidades y reportes financieros

| Hallazgo | Riesgo |
|----------|--------|
| Compras report en S/ | Bajo (compras suelen PEN) |
| Dashboard agregados | Medio si incluyen ventas USD |

---

## 10. Checklist pre-producción USD

- [ ] Configurar `token_consulta` en ajustes centrales (apiperu.dev).
- [ ] Probar factura USD en ambiente SUNAT beta.
- [ ] Verificar leyenda 1000 en dólares en PDF Lycet.
- [ ] Capacitar usuarios: precios en USD cuando moneda = USD.
- [ ] Definir política reportes: filtrar por moneda o convertir.
- [ ] Monitorear sesiones de caja con ventas USD.

---

## 11. Fuera de alcance (no implementado)

- Exportación (`0200`), no domiciliados (`0401`), detracción (`1001`/`1004`), percepción (`2001`), anticipos, contingencia.

---

*Documento generado como parte de la Fase A — Moneda / Tipo de cambio / Tipo operación.*
