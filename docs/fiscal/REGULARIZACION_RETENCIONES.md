# Regularización de las cinco retenciones fallidas (solo preparación; nada se reenvió ni se modificó)

Fecha: 10-nov-2026. Fuente: lectura de producción con usuario de solo lectura. **No se reenvió ninguna retención, no se tocó SUNAT y no se modificó ningún documento.**
Estado de las 5: `billing_status = error` en la venta, `status = error` en `tenant_retentions`, sin código SUNAT ni CDR (no llegaron a SUNAT). Son las únicas 5 de los 788 tenants; 0 percepciones.

## 1. Problema técnico común: formato de fecha (afecta a las 5)
- El payload ya envía la **fecha de emisión del documento** correctamente en RFC3339 con zona (p. ej. `2026-08-19T21:59:20-05:00`).
- Las fechas del **detalle** (`details[].fechaEmision`, `details[].fechaRetencion`, `details[].pagos[].fecha`) viajan como solo día (`2026-08-19`) y el facturador exige `Y-m-d\TH:i:sP` → rechazo inmediato: `Invalid datetime "<día>", expected one of the format "Y-m-d\TH:i:sP"`.
- Causa en el código: `fiscalDateOnly` (`retention_perception_helpers.go`) desde el commit `31cfa5a` (28-jun-2026). La corrección está en una entrega independiente (ver `IMPACTO_FECHAS_RETENCION_PERCEPCION.md`).
- Es un defecto de la plataforma, **no** del cliente: cualquier retención emitida desde el 28-jun habría fallado igual.

## 2. Problemas de datos (distintos del formato; pueden provocar rechazo aunque se corrija la fecha)
| Tenant | Doc | Proveedor (tipo doc / número) | Comprobante relacionado | Observación de datos |
|---|---|---|---|---|
| doriconta | R001-1 | **6 (RUC)** 10710794265 | tipo 01 `2312-00003123` | Proveedor válido. Serie `2312` no tiene el formato de serie SUNAT (4 caracteres tipo `F###`/`E###`): verificar el comprobante real. |
| industrialrafaz | R001-1 | **0 (sin documento) 000000** "Clientes varios" | tipo 12 `Fdh-00004478` | **Proveedor no identificado**: SUNAT exige RUC del proveedor en la retención. Además tipo 12 (ticket) y serie `Fdh`: probable dato de prueba. |
| industrialrafaz | R001-2 | **1 (DNI)** 45454545 | tipo 01 `e001-00002528` | **DNI en lugar de RUC**: la retención requiere RUC del proveedor. DNI de 8 dígitos repetidos: probable dato de prueba. |
| industrialrafaz | R001-3 | **6 (RUC)** 20614717697 "Grupo" | tipo 01 `FGFGFG-GFGFG` | RUC con formato válido; el comprobante `FGFGFG-GFGFG` **no es un número de comprobante real** (parece prueba): no se podrá regularizar tal cual. |
| col | R001-1 | **1 (DNI)** 75850744 | tipo 03 `F001-00000001` | **DNI en lugar de RUC** (persona natural sin RUC no es sujeto de retención por el comprobante de retención). Además una boleta (03) con serie `F001`: verificar. |

Importes (referencia): doriconta 26.33 (ret. 0.81) · industrialrafaz 208.55 / 1 517.56 / 33.88 (ret. 6.45 / 46.93 / 1.05) · col 38.80 (ret. 1.20); tasa 3 %, régimen 01.

## 3. Clasificación y propuesta (a decidir por cliente)
| Documento | Problema técnico (fecha) | Problema de identificación / datos | Propuesta |
|---|---|---|---|
| doriconta R001-1 | sí | verificar serie del comprobante (`2312`) | Confirmar con el cliente si es real; si lo es, reemitir tras el despliegue de la corrección. |
| industrialrafaz R001-1 | sí | **proveedor sin documento (000000)** | No reenviar: probablemente prueba; si es real, requiere RUC del proveedor. |
| industrialrafaz R001-2 | sí | **proveedor con DNI** | No reenviar sin RUC real del proveedor. |
| industrialrafaz R001-3 | sí | comprobante relacionado ficticio | Probable prueba: no reenviar; consultar al cliente. |
| col R001-1 | sí | **proveedor con DNI**, boleta con serie F001 | Consultar al cliente; probable prueba o error de carga. |

## 4. Cómo se regularizaría (SOLO descripción, no se ejecuta)
1. Desplegar la corrección de fechas (entrega fiscal independiente).
2. Por cada cliente que confirme que el documento **es real**: corregir los datos del proveedor/comprobante y reemitir con **nuevo correlativo** (no se reutiliza el R001-n que quedó en `error`; el número no fue aceptado por SUNAT y debe quedar anulado/en error documentado según la política del cliente) — la decisión de reutilizar el correlativo o emitir uno nuevo debe validarse con el contador del cliente.
3. Para los que sean pruebas: dejarlos como están (en `error`) o marcarlos como anulados por el cliente; no hay efecto tributario porque no llegaron a SUNAT.
4. Ninguna acción automática: cada reenvío requiere autorización del cliente y se ejecuta documento por documento.
