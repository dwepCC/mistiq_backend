# Impacto: corrección de fechas en retenciones y percepciones hacia el facturador

Fecha: 10-nov-2026. Entrega fiscal independiente (no toca sucursales/aislamiento). **No se envió nada al facturador ni a SUNAT, no se tocó ninguna BD, no se reenvió ninguna retención, no se escribieron migraciones. Sin commit ni push.**

## 1. Contrato real del facturador (verificado en código)

El facturador deserializa el JSON con JMS Serializer y los metadatos de `facturador_lycet/config/serializer/*.yml`. Todos los campos de fecha de retención/percepción están declarados `type: DateTime` **sin formato**, por lo que JMS usa su formato por defecto `\DateTime::ATOM` = `Y-m-d\TH:i:sP`.

| Campo (JSON) | Declaración | Formato exigido |
|---|---|---|
| `fechaEmision` (documento retención) | `config/serializer/Retention.Retention.yml:7-8` | `Y-m-d\TH:i:sP` |
| `fechaEmision` (documento percepción) | `config/serializer/Perception.Perception.yml:7-8` | idem |
| `details[].fechaEmision` | `Retention.RetentionDetail.yml:7-8`, `Perception.PerceptionDetail.yml:7-8` | idem |
| `details[].fechaRetencion` | `Retention.RetentionDetail.yml:15-16` | idem |
| `details[].fechaPercepcion` | `Perception.PerceptionDetail.yml:15-16` | idem |
| `details[].pagos[].fecha` y `details[].cobros[].fecha` | `Retention.Payment.yml:7-8` (los cobros de percepción reutilizan `Retention\Payment`) | idem |
| `details[].tipoCambio.fecha` | `Retention.Exchange.yml:9-10` | idem |

- Formato por defecto y parseo: `facturador_lycet/vendor/jms/serializer/src/Handler/DateHandler.php:91` (`$defaultFormat = \DateTime::ATOM`) y `:297-326` (`parseDateTime` usa `createFromFormat` con ese único formato y lanza `Invalid datetime "%s", expected one of the format "%s"` en `:322`). `config/packages/jms_serializer.yaml` no cambia el formato ni la zona (zona por defecto de JMS: UTC, solo aplica a formatos sin offset; con `P` manda el offset del texto).
- Solo las guías de remisión tienen un normalizador previo (`src/Service/DespatchDateNormalizer.php`, invocado en `src/Service/DocumentRequestParser.php:46,51`). **Retención y percepción no tienen ninguno**: lo que llega debe estar ya en formato ATOM.
- Qué hace Greenter con ellas: las plantillas XML solo usan la fecha, formateada con `date('Y-m-d')` sobre el `DateTime` deserializado (que conserva el offset del texto): `vendor/greenter/xml/src/Xml/Templates/retention.xml.twig:29` (IssueDate doc), `:30` (IssueTime del documento, `H:i:s`), `:71` (IssueDate detalle), `:78` (PaidDate), `:85` (SUNATRetentionDate), `:92` (fecha TC); `perception.xml.twig:29,30,71,78,85,92` análogos. Por tanto, **con offset -05:00 el día del XML es exactamente el día escrito en el texto**, sin conversión de zona. La hora solo se usa para `IssueTime` del documento.
- Verificación empírica (PHP 8.2 disponible en el entorno): el script `internal/billing/service/testdata/verify_facturador_dates.php` carga el payload con el mismo JMS + `config/serializer` del facturador + plantillas Greenter. Con fechas "solo día" reproduce **literalmente** el error de producción (`Invalid datetime "2026-09-04", expected one of the format "Y-m-d\TH:i:sP".`); con el payload corregido deserializa y el XML muestra los días correctos (p. ej. `04:59Z` del 5-sep => `2026-09-04`).

## 2. Causa raíz

`fiscalDateOnly()` (`internal/billing/service/retention_perception_helpers.go`) devolvía `AAAA-MM-DD` y se usaba para **todas** las fechas de detalle: `details[].fechaEmision`, `fechaRetencion`/`fechaPercepcion`, `pagos[]/cobros[].fecha` y `tipoCambio.fecha`. Origen: commit `31cfa5a` (28-jun-2026, "levatne de observaciones"), que **creó** el archivo ya con esa función (`git log -S fiscalDateOnly`); no hubo una versión anterior que enviara RFC3339: la función nunca cumplió el contrato. El `fechaEmision` del **documento** no pasaba por ahí (el cliente lo manda completo, p. ej. `2026-08-19T21:59:20-05:00`) y por eso el fallo siempre ocurre en el detalle. El frontend (`frontend_tenant/.../FiscalRetentionPerceptionModal.tsx:205,265,301`, `fiscalRetentionPerceptionPrefill.ts`) envía además `pagos[].fecha` y `tipo_cambio.fecha` como solo día.

Defectos asociados corregidos en el mismo origen:
1. Una fecha RFC3339 se truncaba a su día **en la zona del texto**, no en Lima (`...T04:59:00Z` daba el 5 en vez del 4-sep Lima).
2. `resolveDetailExchange`: `fiscalDateOnly("")` devolvía "hoy", así que el fallback `if fecha == ""` a la fecha del comprobante era código muerto (un tipo de cambio manual sin fecha usaba hoy). Ahora hereda la fecha del comprobante relacionado.
3. `CreateAndSendRetention/Perception`: si `fecha_emision` llegaba "solo día", `time.Parse(RFC3339)` fallaba en silencio y `tenant_sales.issue_date`/`tenant_retentions.fecha_emision` quedaban como `now`. Ahora la fecha se normaliza antes y se persiste la elegida.

## 3. Corrección aplicada (en el origen)

- `pkg/facturador/datetime.go` (nuevo código, lo existente no se tocó): `parseFiscalInstant`, `FiscalDateTimeFromInput` (devuelve `Y-m-d\TH:i:sP` en Lima), `FiscalCalendarDay` (día AAAA-MM-DD de Lima, para consultas por día), `NormalizeRetentionPerceptionPayloadDates` (mapa JSON) y normalizadores tipados.
- `retention_perception_helpers.go`: `fiscalDateOnly` eliminada; se reemplaza por `fiscalDateTime` / `fiscalCalendarDay`. `buildRetentionDetails`, `buildPerceptionDetails`, `mapPaymentsInput` y `resolveDetailExchange` emiten el formato exigido. La consulta del tipo de cambio sigue usando un **día** (`AAAA-MM-DD`, requerido por `exchangerate`), calculado en Lima. Se añadió el punto de inyección `exchangeRateLookup` (solo para tests). Una fecha basura ahora da un error claro (`detalle #N: fecha de emisión: fecha inválida: "..."`) antes de llegar al facturador.
- `billing_service.go` (`CreateAndSendRetention` ~L2237 y `CreateAndSendPerception` ~L2483): `fecha_emision` del documento también pasa por `fiscalDateTime` (idempotente: un valor ya `...-05:00` queda idéntico).
- Defensa al enviar (cubre payloads ya persistidos, sin modificarlos en BD): `enrichFiscalPayloadJSON` (`fiscal_payload_meta.go`) normaliza fechas para `documentKind` `retention`/`perception` (ruta real de emisión/re-emisión, `fiscal_document_emit.go:120,132`), y `pkg/facturador/client.go` normaliza antes de serializar en `SendRetention`, `GetRetentionXML/PDF`, `SendPerception`, `GetPerceptionXML/PDF` (mismo patrón que ya usa `normalizeDespatchPayloadDates`). Esto es necesario porque PDF/XML/reenvío leen `tenant_retentions.payload_json`, que en las 5 existentes contiene fechas "solo día".

### Decisión de zona horaria
- Zona: `America/Lima` (UTC-5 fijo, sin DST) con `time.LoadLocation` y fallback `FixedZone(-5h)` (reutiliza `limaLocation()` ya existente) por si el contenedor no tiene tzdata.
- "Solo día" (`2026-09-04`): se interpreta como ese día calendario de Lima y se emite a las **12:00:00-05:00** (mismo criterio que `FormatFiscalDateTime` para facturas/notas; el mediodía evita que cualquier conversión posterior mueva el día). No hay desplazamiento.
- Sin zona (`2026-09-04T10:30:00`, `2026-09-04 10:30:00`, `...T10:30`): hora local de Lima.
- RFC3339 con zona: se **conserva el instante** y se expresa en Lima (`2026-09-05T04:59:00Z` => `2026-09-04T23:59:00-05:00`; `23:30Z` => mismo día 18:30). Un `-05:00` queda idéntico.
- Vacío: "hoy" en Lima (igual que antes). Basura/fecha inexistente (`2026-02-30`, `04/09/2026`): error.

## 4. Qué otros documentos/módulos usan el patrón (y si cambian)

`fiscalDateOnly` solo se usaba dentro de `retention_perception_helpers.go` (11 usos: detalle de retención, de percepción, pagos/cobros, tipo de cambio). Ningún otro módulo la usaba.

| Documento / flujo | Cómo arma la fecha | ¿Cambia? |
|---|---|---|
| Facturas/boletas (`billing_service.go:199,326-328`) | `FormatFiscalDateTime` (RFC3339 -05:00, mediodía) | No |
| Cuotas de crédito (`billing_credit_terms.go:48`) | `FormatFiscalDateTime` | No |
| Notas de crédito/débito (`fiscal_document_emit.go:264`, `credit_note_issue_date.go`) | `FormatFiscalDateTime` | No |
| Guías de remisión (`billing_service.go:1884`, `despatch_payload.go`, `normalizeDespatchPayloadDates`) | `FormatFiscalDateTimeExact` / `NormalizeFiscalDateTimeString` (+ normalizador PHP) | No |
| Resumen diario (`billing_service.go:1225-1226`) | `RFC3339` / `...-07:00` con TZ Lima del contenedor | No |
| Bajas y reversiones (`billing_service.go:1483-1484, 2669-2670`) | `time.RFC3339` | No |
| Retención / percepción: detalle, pagos/cobros, TC | solo día (BUG) | **Sí: ahora RFC3339 -05:00** |
| Retención / percepción: fecha del documento | verbatim del cliente | Equivalente para `-05:00`; ahora se normaliza (y `Z`/sin zona se convierten a Lima) |
| Reenvío/PDF/XML de retenciones y percepciones persistidas | payload tal cual de BD | Ahora normalizado al enviar |

Nota sobre `NormalizeFiscalDateTimeString` (GRE): sigue sin tocarse; interpreta "solo día" a las 00:00 (a diferencia de las 12:00 de la nueva función) porque es la decisión previa para guías.

## 5. Qué se persiste (sin cambios de significado en histórico)
- `tenant_retentions.payload_json` / `tenant_perceptions.payload_json`: el payload nuevo guardará RFC3339 -05:00 en todas las fechas. **Las filas existentes no se modifican** (sin migraciones ni escrituras); se normalizan solo en memoria al enviar.
- `tenant_retentions.fecha_emision` y `tenant_sales.issue_date`: se guardan desde `fechaEmision` ya normalizado. Para entradas `...-05:00` (todas las del frontend actual) el valor es idéntico al de antes; el único cambio de comportamiento es el caso "solo día", que antes caía silenciosamente a `now` y ahora conserva el día elegido.

## 6. Pruebas y resultados reales
Nuevas:
- `pkg/facturador/datetime_fiscal_test.go`: formato (día simple, RFC3339 en Lima/UTC/+02:00, `23:30Z` mismo día, `04:59Z` día anterior, `05:00Z`, sin zona, `T10:30`, fracciones, límites de mes/bisiesto/fin e inicio de año con cruce por zona), idempotencia, vacío/basura/fechas inexistentes, `FiscalCalendarDay`, normalización de mapa heredado y de structs vía servidor HTTP local (6 endpoints retención/percepción).
- `internal/billing/service/retention_perception_dates_test.go`: regresión de retención y percepción (varios detalles, pagos/cobros, fechas heredadas del documento) con aserción de esquema (regex `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}-05:00$` sobre el JSON serializado con `json.Marshal`, igual que `client.go`) y de que el **día** coincide con la entrada; error claro para basura; normalización de payloads legados vía `enrichFiscalPayloadJSON` (y que facturas no se alteran).
- `..._dates_usd_test.go`: retención y percepción completas en USD (TC automático con consulta por `AAAA-MM-DD`, TC manual con/sin fecha, cruce de año).
- `..._dates_php_test.go` + `testdata/verify_facturador_dates.php`: carga los payloads con la lógica real del facturador (JMS + serializer config + plantillas Greenter) y verifica los días del XML; incluye control negativo que reproduce el error de producción. Se omite sola si no hay `php` o `facturador_lycet/vendor`.

Regresión contra el código viejo (`git stash` de los 5 archivos modificados, tests nuevos presentes): `TestRegression_Retention…`, `TestRegression_Perception…`, `TestBuildRetentionDetails_invalid…` y `TestEnrichFiscalPayloadJSON_normalizes…` **FALLAN** (p. ej. `campo "fechaEmision" = "2026-09-01" no cumple … (el facturador respondería: Invalid datetime "2026-09-01")`, y con el viejo código `04:59Z` daba el día 5 en vez del 4). Con el código nuevo pasan.

Resultados finales (comandos ejecutados en `backend_principal`): `go build ./...` OK; `go vet ./internal/billing/service ./pkg/facturador` OK; `go test ./... -count=1`: 95 paquetes `ok`, 0 fallos. `TestContract_FacturadorAcceptsRetentionPerceptionPayloads` PASS (PHP 8.2.12 local).

## 7. Riesgos
- **Facturador desplegado distinto al del repo**: el contrato (ATOM) viene de `jms/serializer 3.32.7` + `config/serializer` de este repo; el error de producción coincide exactamente con él. Si el VPS del facturador corre otra versión, validar con un `/retention/xml` de prueba (no emite a SUNAT) tras desplegar. Si en algún momento se quisiera tolerancia en el propio facturador, el sitio sería un normalizador análogo a `DespatchDateNormalizer` en `DocumentRequestParser::getObject` (no implementado aquí: es otro despliegue).
- **Orden de despliegue**: este cambio es solo backend; no requiere cambios de frontend ni de facturador. Las retenciones ya encoladas en el facturador con fechas "solo día" (snapshot propio) no se corrigen solas: hay que re-encolar desde el backend (ver §8).
- **Zona del contenedor**: las rutas de resumen/bajas usan `time.Now()` en la zona del proceso (el Dockerfile fija `TZ=America/Lima`). No se modificó; el código nuevo no depende de la zona del proceso (usa Lima explícito).
- **Datos legados con `Z` a medianoche UTC** (p. ej. fechas "solo día" leídas con zona UTC): se convertirían al día anterior en Lima. En producción las fechas de negocio se guardan al mediodía de Lima (ventas/compras), por lo que no aplica, pero conviene tenerlo presente si algún origen entrega `T00:00:00Z`.
- Un `tipo de cambio` manual sin fecha ahora hereda la fecha del comprobante relacionado (antes: hoy, por el código muerto). Es la intención original del código.
- Un detalle con `fecha_emision` vacío sigue equivaliendo a "hoy" (comportamiento previo, no alterado; dato dudoso fiscalmente, pendiente de decisión de negocio).

## 8. Regularización de las 5 retenciones existentes (SOLO descripción; no se ejecuta)
Contexto de datos y propuesta por cliente: `docs/fiscal/REGULARIZACION_RETENCIONES.md` (varias tienen además datos inválidos de proveedor: DNI/sin documento, comprobante ficticio).
1. Desplegar esta corrección del backend (migrar flota no es necesario: no hay migraciones).
2. Por cada cliente que confirme que el documento es real y con su autorización: corregir/confirmar datos del proveedor y comprobante relacionado.
3. Técnicamente, tras el deploy el **mismo registro** puede re-encolarse: `emitRetentionDocument` (`fiscal_document_emit.go:111-122`) lee `payload_json` (con `serie`, `correlativo` e importes ya guardados) y `enrichFiscalPayloadJSON` ahora normaliza las fechas, así que el reenvío usaría **el mismo número de serie y correlativo** (R001-n). Como el facturador rechazó antes de llegar a SUNAT, SUNAT nunca recibió esos números. Pero si hay que corregir datos del proveedor/comprobante, `payload_json` ya los contiene congelados, por lo que corresponde **emitir un documento nuevo con nuevo correlativo** (la política de reutilizar o no el correlativo debe validarse con el contador del cliente).
4. Los que sean pruebas se dejan en `error` o se anulan según el cliente. Nada se reenvía de forma automática; cada reenvío es documento por documento, con autorización por cliente, y tras reiniciar el worker del facturador si se editó `fiscal_documents` por SQL (procedimiento habitual).

## 9. Archivos
Modificados: `pkg/facturador/datetime.go`, `pkg/facturador/client.go`, `internal/billing/service/retention_perception_helpers.go`, `internal/billing/service/billing_service.go`, `internal/billing/service/fiscal_payload_meta.go`.
Nuevos: `pkg/facturador/datetime_fiscal_test.go`, `internal/billing/service/retention_perception_dates_test.go`, `..._dates_usd_test.go`, `..._dates_php_test.go`, `internal/billing/service/testdata/verify_facturador_dates.php`, este informe.
