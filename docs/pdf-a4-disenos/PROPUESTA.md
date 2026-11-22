# Propuesta: dos diseños de PDF A4 y selector por tenant

Fecha del análisis: 19-oct-2026. Estado: **solo análisis y propuesta, no se tocó código ni producción** (consulta a la BD en solo lectura con un usuario de solo lectura).

---

## 0. Resumen ejecutivo

- El PDF A4 de factura, boleta, notas de crédito/débito, nota de venta y cotización **se genera 100 % en el navegador/app (jsPDF)**, en un único archivo: `frontend_tenant/src/utils/receiptPdfA4.ts`. El backend Go **no genera PDFs** (solo arma el `print_data`), y el facturador PHP tiene su propio PDF (otro look, ver §2.4) que no cambió.
- El cambio de diseño es **un solo commit**: `07909ee` del 14-oct-2026 18:08 (-05:00), «PDF A4: rediseño de comprobantes y correcciones de pagos/detracción», en `frontend_tenant` (ya en `origin/main`). Reescribió `receiptPdfA4.ts` (498 líneas nuevas, 811 borradas). **El diseño anterior no quedó como archivo huérfano, pero está completo y recuperable en git** (`git show 07909ee^:src/utils/receiptPdfA4.ts`, 1 738 líneas). Ese mismo commit trae también 3 correcciones reales que **no deben perderse** al restaurar el anterior (§2.2).
- Hoy **los 674 tenants** (599 activos) ven el diseño 2, porque el diseño lo decide el frontend desplegado, no hay ningún campo `template/design/layout` en BD ni en código para comprobantes (verificado en código y en `information_schema` de los 674 esquemas).
- Propuesta: **«Clásico»** (`classic`, diseño 1) y **«Moderno»** (`modern`, diseño 2). Un solo modelo de datos (`PrintData`), un renderizador por diseño detrás de un registro, un único punto de despacho (`renderReceiptA4`), columna nueva `pdf_a4_design` en `tenant_company_configs` (migración v164 aditiva), selector con vista previa real en **Ajustes → Comprobantes**. Las guías de remisión, los tickets de 80/58 mm y el PDF del facturador **no se tocan**.
- Esfuerzo total estimado: **≈ 5 días de trabajo** (§9), en 5 fases, la primera sin ningún cambio visual.

---

## 1. Dónde se genera cada PDF

| Generador | Archivo | Qué produce | Lo usa |
|---|---|---|---|
| **jsPDF A4 (Tukifac)** | `frontend_tenant/src/utils/receiptPdfA4.ts` → `renderReceiptA4()` | Factura 01, boleta 03, NC 07, ND 08, nota de venta 00, cotización QT; guías (GRE) con su propia plantilla `renderDespatchA4()` | Toda la app de ventas (ver §3) |
| **jsPDF ticket 80/58 mm** | `frontend_tenant/src/utils/receiptPdf.ts` (rama `isTicket`) + `receiptTicketFooter.ts` | Ticket térmico | Impresión POS, WhatsApp, correo. **NO se toca** |
| **A4 de Tukichef** | `frontend_restaurant_tenant/src/utils/receiptPdfA4.ts` (505 líneas, otro layout, helvetica) | A4 de la app de restaurante | App de restaurante. No fue afectada por el rediseño (último cambio 2-oct, solo logo) |
| **Facturador (Greenter + wkhtmltopdf)** | `facturador_lycet/templates/pdf/invoice.html.twig` (437 líneas) vía `FiscalHtmlReport` / `FiscalPdfService` / `FiscalDocumentPdfResolver` | Tercer look («E L E C T R Ó N I C A», Tahoma, «Fecha Vencimiento»…). Se guarda en `pdf_url` y se adjunta en el correo fiscal del facturador | Correo del facturador, `GET /api/billing/…/pdf` (descarga de la app de restaurante `VentasPage`), Central → Documentos fiscales. Último cambio relevante: 28-sep («Muestra "-" cuando el cliente no tiene dirección») |
| **Backend Go** | — | **Ninguno.** `go.mod` no tiene librería de PDF; solo `BuildPrintData` (`internal/sales/service/print_data.go`), `quotations/service/print_data.go`, `ecommerce/service/print_data.go`, y `email_receipt.go` recibe el PDF ya hecho en base64 | — |

**Un solo punto de salida del A4 de Tukifac:** `generateReceiptPdf(data, 'a4')` (`receiptPdf.ts:224`) → línea 660 `await renderReceiptA4(doc, data)`. Todos los flujos (vista previa, descarga, impresión, correo, WhatsApp) pasan por ahí; por eso el despacho por diseño puede vivir en **un solo lugar**.

## 2. Historia: qué cambió y qué quedó

### 2.1 Cronología

- `07909ee` (14-oct 18:08) rediseño. Antes, `7e4f820` (14-oct 16:16) «no justificar la razón social del emisor» ya tocaba el encabezado del diseño 1.
- Comprobantes de ejemplo coherentes con esto: F002-00000618 (emitida 12-oct → diseño 1) y F002-00000669 (17-oct → diseño 2).
- Archivos del commit: `receiptPdfA4.ts`, `salePreviewPrintData.ts` (+9 líneas) y `paymentMethodLabels.ts` (+2). **No hay plantillas HTML/recursos del diseño 1**; el diseño 1 vive solo en el historial de git. `frontend_restaurant_tenant` y `facturador_lycet` no cambiaron con este rediseño.
- Rastros del diseño anterior que sí siguen en el árbol: el comentario «mismo estilo que el PDF del facturador» en `BOX_RADIUS`, `drawTransportDetailBox` (desactivada) y `inventoryTransferPdf.ts` (“estilo receiptPdfA4.ts”, usa el encabezado del diseño 1).

### 2.2 Lo que NO es diseño y debe sobrevivir en ambos diseños

El commit mezcló rediseño con correcciones de datos. Al restaurar Clásico hay que **portarlas**, no revertirlas:

1. Medio de pago de la detracción: usa el medio configurado (o 001) con su descripción del catálogo 59 en vez del código interno `detraccion_bn` (cambio en `salePreviewPrintData.ts` y `paymentMethodLabels.ts`).
2. «Condición de pago» ya **no lista la porción de detracción como pago del cliente** (`customerPayments()`); el diseño 1 antiguo sí la listaba.
3. Factura a crédito: cuotas «Cuota #n / Fecha / Monto» **sin columna de estado** (la tabla CUOTAS con ESTADO del diseño 1 antiguo mostraba «Pagada/Parcial/Vencida», información de cobranza que no corresponde a un comprobante).
4. Cabecera: omitir «Central telefónica:» / «Email:» vacíos (antes salía la etiqueta sola).

Conclusión: las reglas de **datos** (qué pagos, qué cuotas, qué detracción) se extraen a funciones comunes; cada diseño solo decide **cómo dibujarlas**.

### 2.3 Qué comparten hoy los dos diseños (no se duplica)

Casi todo el pie: `drawFooter` (GRACIAS POR SU PREFERENCIA / Tukifac! / «Comprobante emitido a través de…» / leyenda de representación impresa / URL de consulta), `drawLegendAndNotes` (Términos y condiciones, Observaciones), QR de billetera Yape/Plin (`renderPaymentWalletBlock`), plantilla de guía (`renderDespatchA4`), helpers de formato, `getNotaVentaPrintLayout` (ajustes por doc 00), `buildReceiptTotalLines`, `getCreditNoteReference`, `getPrintIssuerAddress`, logo por sucursal.

### 2.4 El PDF del facturador es un tercer diseño

No es ni el 1 ni el 2 (Tahoma, «E L E C T R Ó N I C A», tabla «Cantidad», «Resumen: hash»). Se genera en el VPS del facturador y se despliega aparte. **No forma parte del selector de esta propuesta** (decisión abierta D-6).

## 3. Mapa de vistas y puntos de salida

Leyenda: **A4** = pasa por `renderReceiptA4` → **cambia con el selector**. **TK** = ticket, no cambia. **FAC** = PDF del facturador, no cambia.

| # | Vista / punto de salida | Archivo | Formato | Diseño que usa hoy |
|---|---|---|---|---|
| 1 | Lista de ventas: ver/imprimir y descargar A4 | `pages/sales/SalesPage.tsx:368,384` (`openReceiptPdfInNewTab`/`downloadReceiptPdf`, `'a4'`) | A4 | 2 |
| 2 | Lista de ventas: ticket (ver/descargar/`PrintDocButton webFormat="ticket"`) | `SalesPage.tsx:400,416,621` | TK | — |
| 3 | WhatsApp desde ventas (imagen PNG, A4 o ticket) | `SalesPage.tsx:424-441` → `utils/receiptPng.ts` → `generateReceiptPdf` | A4/TK (elige) | 2 (A4) |
| 4 | Modal de comprobante tras cobrar en POS | `pages/pos/POSPage.tsx:1555` → `ReceiptPrintModal` (botones Ticket / A4) | A4 + TK | 2 (A4) |
| 5 | Modal de comprobante del registro de venta | `SalesRegisterPage.tsx:3510` `ReceiptPrintModal`; vista previa pre-emisión `SaleReceiptPreviewModal` (marca de agua «PREVIEW», datos de `salePreviewPrintData.ts`) | A4 + TK | 2 |
| 6 | Descarga/WhatsApp/correo desde el modal | `ReceiptPrintModal.tsx` (`downloadReceiptPdf`, `shareReceiptPdf(…,'ticket')`) | A4 y TK | 2 (A4); WhatsApp del modal es solo ticket |
| 7 | Correo de comprobante de venta | `ReceiptEmailModal.tsx:57` genera **ticket**; `POST /api/sales/:id/email-receipt` recibe `pdf_base64` | TK | — |
| 8 | Facturación electrónica: lista, detalle, panel de doc. vinculado, auxiliares, guías | `BillingPage.tsx`, `LinkedFiscalDocPanel.tsx`, `FiscalAuxDocListSection.tsx`, `GuiasListSection.tsx` → `createLocalReceiptPdfObjectUrl` / `downloadLocalReceiptPdf(saleId,'a4')` | A4 | 2 (guías: plantilla propia, igual en ambos) |
| 9 | WhatsApp desde facturación | `BillingPage.tsx:620-638` | A4/TK | 2 (A4) |
| 10 | Notas de crédito y débito | mismo `GET /sales/:id` + `print_data`; confirmado en el comentario de `localReceiptPdf.ts` («no hay un flujo de impresión separado para notas») | A4 | 2 |
| 11 | Cotizaciones: ver, descargar, correo (A4 o ticket) | `pages/quotations/QuotationsPage.tsx:157-225`, `quotations/service/print_data.go` | A4 + TK | 2 (QT) |
| 12 | Pedidos web (tienda virtual) | `pages/ecommerce/PedidosWebPage.tsx:76` → `downloadReceiptPdf(...,'a4')`, `ecommerce/service/print_data.go` | A4 | 2 |
| 13 | Movimientos de caja (comprobante de venta) | `CashMovementTypeView.tsx:151` (`'ticket'`) | TK | — |
| 14 | Impresión directa a ticketera (Android/Tauri) | `PrintDocButton.tsx`, `receiptTicketPrint.ts` | TK | — |
| 15 | Vista previa «papel» HTML en Android WebView | `components/ReceiptPaperPreview.tsx` (emulación HTML, no es el PDF real) | A4/TK aprox. | Aproximación propia; ver D-5 |
| 16 | App de restaurante (Tukichef) | `frontend_restaurant_tenant/…/receiptPdfA4.ts`, `VentasPage.tsx` (A4 local y PDF del facturador) | A4 propio + FAC | Propio; no cambia |
| 17 | Descarga «PDF» del facturador | `GET /api/billing/…/pdf` → `GetInvoicePDFContent` → Lycet; Central `FiscalDocumentsPage` | FAC | Facturador |
| 18 | Correo fiscal del facturador (PDF+XML+CDR) | `facturador_lycet/src/Service/Fiscal/FiscalEmailProcessor.php` | FAC | Facturador |
| 19 | Enlaces públicos compartidos | **No existen** para el A4: el backend no sirve PDFs ni hay ruta pública de comprobantes (solo hay la emisión pública de GRE, que no es una vista del PDF) | — | — |
| 20 | Comprobantes de membresías/suscripciones | En el tenant `MembershipsPage` solo arma un texto de WhatsApp (recordatorio), sin PDF. Los comprobantes de pago de suscripción se suben como imagen/PDF por el usuario (`PaymentModal`). Central usa PDFs propios de equipos/etiquetas | — | No aplica |
| 21 | Reportes en PDF (caja, kardex, ventas…) | `exportPdf.ts`, `cashSessionReportPdf.ts`, etc. | A4 de reporte | Fuera de alcance (no son comprobantes) |
| 22 | Traslados de inventario | `inventoryTransferPdf.ts` | A4 | Fuera de alcance (usa el encabezado clásico por copia; ver D-7) |

**Hallazgo útil (latente, no causado por esta tarea):** los constructores de `PrintCompany` de cotizaciones y pedidos web (`quotations/service/print_data.go:83`, `ecommerce/service/print_data.go:56`) **no rellenan `logo_size_a4`**, así que esos A4 siempre usan el tamaño de logo «mediano» aunque el tenant haya elegido otro. El selector de diseño, si dependiera solo de ese camino, tendría el mismo problema → ver la decisión de arquitectura en §5.2 (el diseño se resuelve en el frontend con respaldo en la config cacheada).

## 4. Comparación campo por campo

Capturas: **Diseño 1** = F002-00000618 (12-oct), **Diseño 2** = F002-00000669 (17-oct). «Código» se refiere a `receiptPdfA4.ts` (versión `07909ee^` para el 1, HEAD para el 2).

| Elemento | Diseño 1 «Clásico» | Diseño 2 «Moderno» |
|---|---|---|
| **Tipografía** | Serif (`times`) | Sans-serif (`helvetica`) |
| **Cabecera: disposición** | Logo a la izquierda, datos de empresa en bloque a su derecha (líneas largas **justificadas**), recuadro del documento a la derecha | Logo a la izquierda, datos de empresa **centrados** en su columna, recuadro a la derecha (se ve idéntico en estructura general; cambia la alineación y el recuadro) |
| **Nombre emisor** | Mayúsculas, 11.5 pt | Mayúsculas, 12 pt, centrado |
| **Teléfono / email** | Siempre imprime «Central telefónica:» y «Email:» (vacíos si no hay) | Solo si hay dato |
| **Recuadro del documento** | 62 mm, **esquinas redondeadas**, alto fijo 36 mm: «RUC: n» · **banner gris** con «FACTURA ELECTRÓNICA» · número | 64 mm, esquinas rectas, alto variable: «R.U.C. n» · tipo · número (3 líneas en negrita 12 pt), **sin banner gris** |
| **Bloque del cliente** | **Sin recuadro**. Líneas «FECHA DE EMISIÓN :», «CLIENTE :», «RUC/DNI :», «DIRECCIÓN :» en mayúsculas; «Fecha Vencimiento» alineada a la derecha | **Dos recuadros** lado a lado (56 % / 44 %): izquierda «Razón Social» (o «Cliente» si no es RUC) / RUC / Dirección; derecha «Fecha Emisión» / «Hora Emisión» / «Fecha de Vcto» / «Moneda» |
| **Hora de emisión** | No se muestra | Sí (`issue_time`, 8 caracteres) |
| **Moneda / tipo de cambio** | **No se muestra** (solo el símbolo en los totales) | «Moneda: Soles/Dólares» y, si USD, «Tipo de cambio» |
| **Cliente genérico** | «CLIENTE VARIOS» | «CLIENTES VARIOS» |
| **O/C y guías** | Filas sueltas «O/C», «GUÍA REM.» / «GUÍA TRANSP.» bajo el cliente | Dentro del recuadro derecho («O/C», «Guía de Remisión», «Guía Transp.») |
| **NC/ND** | Filas sueltas: TIPO DE NOTA, TIPO DOC. REF., DOCUMENTO REF., MOTIVO | Recuadro aparte de ancho completo: «Tipo de nota», «Doc. que modifica» (tipo + número juntos), «Motivo» |
| **Observaciones fiscales / anticipos** | Filas «OBS.» y «ANTICIPO» | En el mismo recuadro aparte («Observaciones», «Anticipo») |
| **Tabla de ítems** | CANT. · UNIDAD · CÓDIGO · DESCRIPCIÓN · P.UNIT · **DTO. (siempre)** · TOTAL | CÓDIGO · CANT. · U.M. · DESCRIPCIÓN · P.UNIT · [DTO. **solo si hay descuentos**] · TOTAL |
| **Bloque inferior** | Dos zonas: izquierda «SON: …» y recuadro redondeado «CONDICIÓN DE PAGO / PAGOS»; derecha totales como texto (sin celdas) y **QR + «Código Hash» a la derecha** debajo de los totales | **Un recuadro de 3 columnas**: izquierda Vendedor + datos de detracción; centro «SON: …», leyenda de detracción, **QR + «Código Hash»**; derecha totales en celdas (etiqueta \| importe) |
| **Totales** | OP. GRAVADAS / IGV / TOTAL A PAGAR (+ RET. IGV, NETO A COBRAR, ANTICIPO) | Mismas líneas y mismos importes (`buildReceiptTotalLines`), en celdas |
| **Código Hash** | Debajo del QR (7 pt) si hay hash. En la captura 618 no aparece: ese comprobante no tenía `sunat_hash` en el `print_data` | Debajo del QR, centrado, si hay hash |
| **Condición de pago** | Recuadro redondeado a media página: «CONDICIÓN DE PAGO: …», «PAGOS:», viñetas por medio de pago y «VUELTO». *(Antes de la corrección: listaba también la detracción como pago)* | Texto simple (sin recuadro) bajo cuentas bancarias: «CONDICIÓN DE PAGO: …» + viñetas; crédito: «• Cuota #n / Fecha / Monto» |
| **Nota de venta / no electrónico** | Caja punteada «MÉTODO DE PAGO: …» (una línea o varias si mixto/vuelto) | Misma «CONDICIÓN DE PAGO» que el resto (si el ajuste de nota de venta la muestra) |
| **Cotización (QT)** | Caja punteada «MÉTODO DE PAGO (REFERENCIA)» | Texto «MÉTODO DE PAGO (REFERENCIA):» + viñetas |
| **Detracción** | Recuadro «Información Adicional» (columna izquierda): LEYENDA, TIPO DE DETRACCIÓN, P. DETRACCIÓN, MONTO, N° CONSTANCIA, MEDIO DE PAGO (`código - descripción`), N° CTA. BANCO NAC. | Celda izquierda del recuadro: «B/S sujeto a detracción», «Porcentaje detracción», «Monto detracción» (**redondeado al entero**, a pedido del negocio), «Método de pago» (solo descripción), «N. Cta. detracciones», «N° Constancia»; la leyenda va en la celda central |
| **Retención IGV** | RET. IGV (3%) + NETO A COBRAR en la columna de totales | Igual, en celdas |
| **Cuentas bancarias** | «CUENTAS BANCARIAS:» bajo el recuadro de pago (columna izquierda) | «CUENTAS BANCARIAS:» bajo el recuadro de 3 columnas (se angosta si hay QR de billetera) |
| **Vendedor** | «Vendedor:» tras las cuentas bancarias | Primera fila de la celda izquierda del recuadro |
| **Cuotas de crédito** | Tabla «CUOTAS»: N° · VENCIMIENTO · MONTO · **ESTADO** *(corregida después: sin estado)* | En «Condición de pago», una línea por cuota, sin estado |
| **Anticipos** | Fila «ANTICIPO» + línea de ítem de deducción + «*** Pago Anticipado ***» | Igual, fila en recuadro aparte |
| **Vuelto** | En «PAGOS» del recuadro | En viñetas de «Condición de pago» |
| **Billetera Yape/Plin (QR)** | Pegado bajo el QR SUNAT (misma columna) | A la derecha, bajo el recuadro de 3 columnas; el texto de la izquierda se angosta |
| **Términos y condiciones / Observaciones** | Igual (`drawLegendAndNotes`) | Igual |
| **Pie** | Igual en ambos | Igual en ambos |
| **Detalle de transporte de carga (1004)** | Desactivado (comentado) | Desactivado (comentado) |
| **Guías de remisión** | Plantilla propia `renderDespatchA4` | Misma plantilla (no cambió) |
| **Tamaño de logo A4, logo por sucursal, ajustes de nota de venta** | Respetados | Respetados |

### 4.1 ¿Alguno omite información obligatoria SUNAT?

Revisión funcional, no asesoría legal; conviene que el contador del negocio la confirme:

- Ambos conservan emisor (RUC, razón social/nombre, dirección), tipo y número, cliente y su documento, ítems con cantidad/unidad/descripción/importe, base gravada, IGV, total, monto en letras (“SON”), QR y hash.
- **Clásico no muestra moneda explícita ni hora ni tipo de cambio.** En comprobantes en dólares solo la etiqueta «US$» en los totales indica la moneda, y el tipo de cambio no aparece. No es un hueco para facturas en soles, pero es **el único punto donde Clásico queda por debajo de Moderno**. Propuesta: añadir al Clásico, de forma aditiva, una línea «MONEDA» y «HORA» (y «T.C.» en USD) **sin alterar** su aspecto (ver D-3).
- **Clásico imprime siempre la columna DTO.** Con 0.00 en todas las filas es ruido, no un faltante.
- Ninguno de los dos agrega nada que el otro oculte en cuanto a detracción/retención/anticipo: los mismos campos existen en ambos, con distinta distribución y etiquetas (tabla de arriba). Diferencia a tener en cuenta: Moderno redondea el monto de detracción al entero (decisión del negocio, `f0ce761`); Clásico, tras portar las correcciones, debe hacer lo mismo para que ambos coincidan con el comprobante emitido.
- `tenant_sale_fiscal_profiles` (O/C, guías, observaciones, anticipos, retención, detracción) llega a ambos por el mismo `print_data.fiscal`; ningún diseño lee la tabla directamente.

## 5. Arquitectura propuesta

### 5.1 Nombres y claves

| Diseño | Nombre en el selector | `slug` | Por qué |
|---|---|---|---|
| 1 (anterior) | **Clásico** | `classic` | Serif, banner gris y datos sin recuadros: la imagen «formal» de factura tradicional. Descriptivo y estable aunque salgan más diseños |
| 2 (actual) | **Moderno** | `modern` | Sans-serif, recuadros, totales en celdas y datos del comprobante (hora, moneda) en caja propia |

Los slugs son en minúscula, `[a-z0-9_]`, máx. 30 caracteres, **nunca se renombran** (se guardan en BD). Diseños futuros: `compact`, `minimal`, etc.

### 5.2 Estructura de código (frontend, solo A4)

```
frontend_tenant/src/utils/receiptA4/
  index.ts            // renderReceiptA4(doc, data): despacha por diseño (+ fallback)
  registry.ts         // A4_DESIGNS: { slug, label, description, render, fontFamily }[]  + DEFAULT_A4_DESIGN
  common.ts           // constantes, setFont(doc,size,style,family), formateadores, drawFooter,
                      // drawLegendAndNotes, billetera, helpers de datos (customerPayments,
                      // postTotalRows, detraccion rows, cuotas…) — SIN dibujo específico
  classic.ts          // drawHeader/Customer/Items/Post… del diseño 1 (restaurado)
  modern.ts           // drawHeader/Customer/Items/PostBox… del diseño 2 (el actual)
  despatch.ts         // renderDespatchA4 (compartido, sin cambios)
```

- `receiptPdf.ts` sigue llamando `renderReceiptA4(doc, data)`; **el archivo `receiptPdfA4.ts` se conserva como reexport** para no romper imports (hoy solo `receiptPdf.ts` lo importa; `inventoryTransferPdf.ts` solo lo cita en un comentario).
- Contrato de un diseño: `(doc: jsPDF, data: PrintData) => Promise<void>`. Cada diseño recibe el mismo `PrintData` y no consulta nada más.
- Guía de remisión: se atiende **antes** del despacho (igual que hoy: `isGuiaSunatCode && data.despatch`), así que **no depende del diseño**.
- La fuente deja de ser una constante de módulo: `setFont` recibe la familia del diseño (`times`/`helvetica`).
- Ticket 80/58 mm: intacto; el despacho solo existe en la rama A4 de `generateReceiptPdf`.

### 5.3 De dónde sale el diseño elegido

Orden de resolución en el frontend: `data.company.pdf_a4_design` → config de empresa cacheada (`lib/companyConfig/store.ts`, que ya usa el logo) → `DEFAULT_A4_DESIGN`. Razones:

1. `salePreviewPrintData.ts` (vista previa en POS) arma el `PrintData` en el navegador desde la config cacheada.
2. Los constructores de cotizaciones y pedidos web hoy olvidan `logo_size_a4`; con el respaldo en la config no hace falta que los 3 constructores backend estén perfectos para que el diseño sea el correcto.
3. Igualmente se agrega `PdfA4Design` a `PrintCompany` en los 3 constructores backend (`sales`, `quotations`, `ecommerce`) mediante un helper común, y de paso se corrige el `logo_size_a4` faltante.

Slug desconocido o vacío → `DEFAULT_A4_DESIGN`.

### 5.4 Si el diseño elegido falla

`generateReceiptPdf` crea el `jsPDF` y llama al diseño. Si lanza una excepción (dato inesperado, logo corrupto…), se **registra el error en consola, se crea un `jsPDF` nuevo (el anterior quedó dibujado a medias) y se renderiza con el diseño por defecto**. Mismo principio que ya usa el logo («sin logo»). No se bloquea la impresión del comprobante por un problema de estilo. Prueba específica en §8.

### 5.5 Qué NO cambia

Guías, tickets, PDF del facturador, correo fiscal del facturador, A4 de Tukichef, reportes. Tampoco cambia el modelo de datos del comprobante ni lo enviado a SUNAT: el diseño es **solo presentación**.

## 6. Dónde se guarda la elección

### 6.1 Campo

`tenant_company_configs.pdf_a4_design VARCHAR(30) NOT NULL DEFAULT 'modern'` (modelo `TenantCompanyConfig`, `pkg/database/migrations.go:861`, junto a `LogoSizeA4`). Es el mismo patrón que `logo_size_a4` (v147), exactamente aplicable: columna con default, `json:"pdf_a4_design"`, parche `PdfA4Design *string` en `CompanyConfigPatch` (`company_service.go:61`), normalización en `ApplyConfigPatch` y en `GetConfig`, y campo en `PrintCompany`.

Nombre elegido: `pdf_a4_design` porque deja claro «PDF» + «A4» (no ticket) + «design», y no choca con `color_theme` (tema de la app).

### 6.2 Migración

`pkg/database/tenantmigrations/v164_company_pdf_a4_design.go` — **versionada y solo aditiva**:

- Si la tabla no existe → `nil`. Si la columna existe → `nil` (idempotente). Si no, `AddColumn`. Misma forma que `v147_company_logo_size_by_format.go` y `v161_ecommerce_hero_style.go`.
- Sin backfill: el `DEFAULT 'modern'` rellena las filas existentes en MySQL al añadir la columna.
- Número: la última aplicada en producción es la v162 y la **v163 (detracción, ubigeo de transporte) está pendiente de deploy**; esta sería la **v164** y debe desplegarse después de v163 o en la misma tanda. Si v163 se publica primero, no hay conflicto de numeración.
- Impacto: tabla de una sola fila por tenant, `ADD COLUMN` con default en MySQL 8 es instantáneo/ligero; 674 esquemas con migrate-fleet (ya se endureció en `605952f`, sin escaneo de deriva). No se requiere ninguna ventana de mantenimiento.

### 6.3 Qué diseño queda por defecto

Consulta de solo lectura en producción (19-oct-2026):

- 674 tenants no eliminados: 391 + 208 activos (general/gastronómico), 71 suspendidos, 4 inactivos.
- 350 + 185 de los activos tienen SUNAT conectado (los que emiten comprobantes electrónicos).
- Los 674 esquemas tienen `logo_size_a4` (v147 aplicada en toda la flota) y **ninguno tiene columna de diseño**, como se esperaba.
- Hoy los 674 ven Moderno; no se puede medir cuántos prefieren Clásico.

**Recomendación:** `DEFAULT 'modern'` para todos (existentes y nuevos): al desplegar **no cambia nada visible** y quien prefiera el anterior lo cambia en Ajustes. Es lo que la mayoría ve desde el 14-oct. Alternativa en D-1.

## 7. Pantalla de configuración

- **Lugar:** Ajustes → Empresa → pestaña **Comprobantes** (`components/settings/ErpSettingsTab.tsx`, hoy: `LogoSizeSettings`, `ReceiptWalletSettings`, `SaleNoteBrandingSettings`). Nuevo componente `A4DesignSettings` al **principio** de esa pestaña (el diseño condiciona al logo y a lo demás). Permiso: el mismo que ya edita la config de empresa (`PUT /api/company/config` con `edit`, `internal/company/routes.go:28`).
- **Contenido:** una tarjeta por diseño (nombre, descripción de una línea, miniatura), selección única, botón «Guardar», aviso: «Se aplica a PDF A4 de facturas, boletas, notas y cotizaciones. No afecta tickets (80/58 mm) ni PDF ya enviados.»
- **Vista previa:** generada **con el motor real** (no imágenes estáticas) usando un `PrintData` de ejemplo con el nombre, RUC, logo y datos reales del tenant → bytes de `jsPDF` → miniatura. Así cada diseño nuevo que se registre aparece con su vista previa sin trabajo extra, y refleja el logo y los términos del tenant. Se generan perezosamente (solo al abrir la sección) y se cachean en memoria. Botón «Ver en grande» reutiliza `PdfBlobViewer`.
- **Impresión en Android WebView:** el PDF no se incrusta ahí; para esa plataforma se usa una miniatura estática (PNG empaquetado en la app) y «Ver en grande» abre el visor existente.
- **No hay endpoint nuevo:** reutiliza `GET/PUT /api/company/config` → **no se requiere el puente `restaurantperm`** (la lección del 11-sep aplica a endpoints compartidos nuevos; aquí se amplía un endpoint ya existente con un campo más, y los usuarios PIN de Tukichef no editan la configuración).

## 8. Mapa de impacto, fases, riesgos y pruebas

### 8.1 Archivos

| Repo | Archivo | Cambio |
|---|---|---|
| `backend_principal` | `pkg/database/migrations.go` (`TenantCompanyConfig`) | +`PdfA4Design` |
| | `pkg/database/tenantmigrations/v164_company_pdf_a4_design.go` (+ registro en la lista de migraciones y test) | nueva, aditiva |
| | `internal/company/service/company_service.go` | parche + normalización (`^[a-z0-9_]{1,30}$`, vacío → `modern`) |
| | `internal/sales/service/print_data.go` (`PrintCompany`, ~líneas 244 y 420) | +`PdfA4Design` |
| | `internal/quotations/service/print_data.go`, `internal/ecommerce/service/print_data.go` | +`PdfA4Design` y +`LogoSizeA4`/`LogoSizeTicket` que faltan |
| | `docs/api-tenant.md` | documentar el campo |
| `frontend_tenant` | `src/utils/receiptPdfA4.ts` → carpeta `src/utils/receiptA4/` (§5.2) | refactor + `classic.ts` restaurado |
| | `src/utils/receiptPdf.ts` | fallback de diseño al fallar |
| | `src/types/printData.ts`, `src/services/company.service.ts` | `pdf_a4_design` |
| | `src/utils/salePreviewPrintData.ts` | tomar el diseño de la config cacheada |
| | `src/components/company/A4DesignSettings.tsx` (nuevo) + `ErpSettingsTab.tsx` | selector con vista previa |
| `frontend_restaurant_tenant` | — | sin cambios (decisión D-4) |
| `facturador_lycet` | — | **sin cambios, sin despliegue** |

### 8.2 Fases

| Fase | Contenido | Cambio visible | Esfuerzo |
|---|---|---|---|
| **F0** | Refactor: partir `receiptPdfA4.ts` en `common.ts` + `modern.ts` + `despatch.ts` + `index.ts`, sin tocar el dibujo. Banco de pruebas con `PrintData` de ejemplo (todos los tipos) y comparación de texto/imagen antes vs. después | Ninguno | 1 día |
| **F1** | `classic.ts`: restaurar desde `07909ee^`, portar las 4 correcciones de §2.2, mover helpers de datos a `common.ts`, añadir MONEDA/HORA/T.C. si se aprueba D-3 | Ninguno (aún sin selector) | 1 día |
| **F2** | Backend: campo, migración v164, parche, 3 `PrintCompany`, tests Go | Ninguno | 0,5 día |
| **F3** | Frontend: tipos, registro con `DEFAULT_A4_DESIGN`, despacho + fallback, `A4DesignSettings` con vista previa, `salePreviewPrintData` | Aparece el selector | 1,5 día |
| **F4** | QA (matriz §8.4), ajustes finos, deploy en orden: backend/migración → frontend | — | 1 día |
| | | **Total** | **≈ 5 días** |

Orden de despliegue: **primero backend + `migrate-fleet` v164**, luego frontend. Si el frontend sale antes, la config no trae el campo y todos caen en `modern` (sin romper nada, porque el selector guarda con `PUT` que aún no conoce el campo → el selector debe ocultarse si `GET` no devuelve `pdf_a4_design`). **El facturador no se despliega.**

### 8.3 Riesgos y mitigaciones

| Riesgo | Mitigación |
|---|---|
| **PDFs ya enviados por correo/WhatsApp no cambian** (son archivos), pero **un comprobante reimpreso después de cambiar el diseño saldrá con el diseño nuevo**: el PDF se genera al vuelo desde los datos | Es el comportamiento actual (igual que `logo_size_a4`). Aviso en la pantalla. Si se quisiera fijar el diseño por comprobante, ver D-2 |
| Restaurar Clásico revierte por accidente las 4 correcciones de §2.2 | Helpers de datos comunes (`common.ts`) + pruebas de detracción como medio de pago y de cuotas en ambos diseños |
| El refactor de F0 cambia píxeles sin querer | Banco de pruebas comparativo antes/después; F0 se mergea solo si Moderno queda idéntico |
| Falla de un diseño | Fallback al diseño por defecto (§5.4) y prueba que fuerza una excepción |
| Paginación (bloque de totales que salta de hoja, términos largos) vuelve a romperse en Clásico: el código antiguo la resolvía con `estimateA4PostTableBlockHeight` y parches (`b7daadc`, `14d86be`, `3864053`) | Restaurar el archivo entero (incluye esos parches) y probar con 1, 8 y 40 ítems |
| El slug guardado deja de existir (diseño retirado) | Normalización en backend y frontend → default |
| Migración en 674 esquemas | Aditiva y con default; mismo patrón v147/v161; migrate-fleet ya endurecido |
| v163 pendiente de deploy | La v164 se numera después; mismos pasos de deploy, sin dependencia funcional |
| ReceiptPaperPreview (Android) mostrará un A4 aproximado igual para ambos | D-5 |
| Despliegue separado del facturador | No participa; sin riesgo |

### 8.4 Plan de pruebas (por tipo de documento × diseño)

Para **cada uno de `classic` y `modern`** generar y revisar visualmente el PDF A4 de:

1. Factura contado, 1 ítem; factura con 8 y 40 ítems (varias hojas, tabla continuada y bloque de totales/QR sin cortarse).
2. Factura con descuentos (columna DTO.) y sin descuentos.
3. Factura a crédito con cuotas (sin columna de estado).
4. Factura con detracción 1004 (medio de pago «Depósito en cuenta», monto redondeado, N° constancia, leyenda) y con retención IGV 3 % (neto a cobrar).
5. Factura con anticipo (línea de deducción, «Pago Anticipado»).
6. Factura en USD (moneda y tipo de cambio visibles según el diseño elegido en D-3).
7. Boleta a «clientes varios», boleta con nombre comercial destacado.
8. Nota de crédito y nota de débito (tipo de nota, documento que modifica, motivo).
9. Nota de venta (00) con `nvLayout` variando: sin cliente, sin razón social, sin desglose de IGV, sin condición de pago.
10. Cotización (QT) con método de pago de referencia.
11. Pago mixto + vuelto; billetera Yape/Plin activa en A4.
12. Con y sin logo, los 3 tamaños de logo; logo por sucursal; términos largos que saltan de hoja; sin teléfono/email.
13. Guía de remisión: idéntica en ambos diseños (no cambia).
14. Ticket 80 mm y 58 mm: idénticos antes/después (regresión).
15. Selector: guardar, recargar, cambiar de usuario/dispositivo, tenant nuevo (default), slug inválido en BD (fallback), campo ausente (backend viejo), vista previa y descarga coinciden con el diseño elegido.
16. Entradas: lista de ventas, POS, facturación, notas, cotizaciones, pedidos web, WhatsApp (imagen) y correo de cotización A4; confirmar que correo de venta (ticket) y PDF del facturador no cambian.
17. Fallo forzado de un diseño → sale el default, sin error al usuario.

Pruebas automáticas: normalizador de slug (Go y TS), registro/despacho con slug desconocido, migración v164 sobre esquema con y sin columna (idempotencia).

## 9. Decisiones abiertas (para el usuario)

| # | Pregunta | Recomendación |
|---|---|---|
| **D-1** | ¿Todos los tenants (existentes y nuevos) arrancan en **Moderno**? (la alternativa es dejar en Clásico a quien no haya generado comprobantes tras el 14-oct, lo que requiere un backfill y una regla extra) | Sí, Moderno para todos |
| **D-2** | ¿El diseño se aplica **siempre con la configuración actual del tenant** (un comprobante reimpreso mañana sale con el diseño vigente), o debe **quedar fijo por comprobante** al emitirlo? | Configuración actual (igual que el tamaño de logo); fijar por comprobante exigiría guardar el diseño en cada venta |
| **D-3** | ¿Se añaden **Moneda, Hora y T.C. (en USD)** al diseño Clásico al restaurarlo, o se restaura exactamente como estaba? | Añadirlos de forma discreta (no hay riesgo y cierra la única diferencia de contenido) |
| **D-4** | ¿El selector alcanza también al **A4 de la app de restaurante (Tukichef)**, que tiene su propio `receiptPdfA4.ts`? | No ahora; fase posterior si lo piden |
| **D-5** | ¿Se actualiza la **vista previa «papel» en HTML de Android** (`ReceiptPaperPreview`) para que imite cada diseño, o se deja como aproximación única? | Dejar como aproximación; su propio texto dice «no reemplaza al PDF real» |
| **D-6** | ¿El **PDF del facturador** (correo fiscal y descarga «PDF» en la app de restaurante y en Central) debe ofrecer también elegir diseño, o queda como está? | Queda como está; es un diseño aparte con despliegue propio |
| **D-7** | ¿Los nombres **Clásico / Moderno** (`classic` / `modern`) están bien, o prefieres otros (p. ej. «Tradicional»/«Estructurado»)? El slug no se podrá cambiar después | Clásico / Moderno |
| **D-8** | ¿Quieres que los **traslados de inventario** (`inventoryTransferPdf.ts`, que imita el encabezado clásico) sigan fijos? | Sí, fuera de alcance |

## 10. Cómo se verificó (para reproducir)

- Historia: `git log` / `git show --stat 07909ee` en `frontend_tenant`; archivo anterior con `git show 07909ee^:src/utils/receiptPdfA4.ts`.
- Búsqueda de PDF en backend: sin librería PDF en `go.mod`, sin `gofpdf/maroto/wkhtml/chromedp` en el código Go; `internal/sales/service/email_receipt.go` solo decodifica `pdf_base64`.
- Facturador: `templates/pdf/invoice.html.twig`, `Report/FiscalHtmlReport.php`, `Service/Fiscal/FiscalDocumentPdfResolver.php`, `FiscalEmailProcessor.php`.
- Producción (solo lectura): conteo de `tukifac_saas.tenants` por estado/rubro y búsqueda en `information_schema.columns` de `logo_size_a4` y de posibles columnas `pdf_a4_design|a4_design|receipt_design` en `saas_tenant_%`. No se modificó nada.
