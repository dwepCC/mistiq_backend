# Foto congelada de los documentos (PDF sin alteraciones)

**Objetivo:** que el PDF de un documento ya emitido (nota de venta, boleta, factura, notas de crédito/débito, guías, cotizaciones y
compras) salga siempre igual, aunque después se edite el cliente, el proveedor, la empresa, la sucursal o los productos.

## 1. Auditoría: de dónde sale cada dato del PDF

Todo PDF de venta pasa por `BuildPrintData` (`internal/sales/service/print_data.go`); la cotización por `BuildPrintDataForQuotation`; la
compra se arma en el navegador (`utils/purchasePdf.ts`) a partir de `GET /api/purchases/:id`.

| Dato del PDF | Dónde vive | ¿Se altera al editar el origen? |
|---|---|---|
| Ítems (código, descripción, unidad, cantidad, precios, descuentos, IGV) | filas del documento (`tenant_sale_items`, `tenant_purchase_items`, `tenant_quotation_items`) | **No**: ya es copia. Editar un producto no cambia las ventas anteriores. |
| Totales, moneda, tipo de cambio, fechas, serie/número, notas | fila del documento | No |
| Pagos, cuotas de crédito, detracción/retención, datos de traslado de la guía, exportación | tablas propias del documento (`tenant_sale_payments`, perfil fiscal, `tenant_despatches.payload_json`, …) | No |
| **Cliente / proveedor** (nombre, documento, dirección, correo) | `tenant_contacts`, leído en vivo | **Sí** |
| **Empresa** (razón social, nombre comercial, RUC, dirección, teléfono, correo, web, notas adicionales, términos) | `tenant_company_configs`, en vivo | **Sí** |
| **Sucursal** (nombre, dirección, teléfono) | `tenant_branches`, en vivo | **Sí** |
| **Logo** | archivo de la empresa/sucursal, en vivo | **Sí** |
| **Vendedor** | `tenant_users`, en vivo | **Sí** |
| **Cuentas bancarias y billetera Yape/Plin** que se imprimen | `tenant_bank_accounts` / config, en vivo | **Sí** |
| Diseño A4, tamaño del logo, qué mostrar en la nota de venta | config de empresa | Sí, **a propósito**: es estilo, no identidad del documento |

Lo que va a SUNAT (XML/CDR y `fiscal_documents`) ya es inmutable; el problema era solo que el PDF de reimpresión podía diferir
de lo enviado (p. ej. otro nombre de cliente, el RUC de la empresa o su dirección).

## 2. Decisión

Una **foto** por documento en la tabla `tenant_document_snapshots` (`doc_kind` + `doc_id`, JSON de ~1 KB) con lo que está en vivo:
cliente/proveedor, identidad de la empresa, sucursal, vendedor, cuentas/billetera, términos y una referencia al logo. El logo vive
una sola vez por contenido en `tenant_snapshot_logos` (hash SHA-256), así 10 000 ventas no duplican 10 000 veces los bytes.

- **Tabla aparte, no columna:** no engorda `tenant_sales`/`tenant_purchases` (listados y reportes siguen igual de livianos).
- **Cuándo se congela:** la primera vez que se arma el documento. Las ventas llaman a `BuildPrintData` al emitir (POS, registro,
  restaurante), así que la foto queda hecha en la emisión. La compra la congela al registrarse (`CreateAPI`).
- **Cómo se usa:** `FreezePrintData` arma el print_data con datos vivos y, si ya hay foto, la superpone; si no hay, la guarda.
  Se aplica **antes del QR** (lleva RUC y documento del cliente).
- **Cotizaciones:** al editarlas (o eliminarlas) se descarta la foto, porque su contenido cambia a propósito; se congela de nuevo
  en la siguiente impresión.
- **Documentos anteriores a la migración V190:** reciben su foto la primera vez que se imprimen, o de una vez con
  `tukifac-api backfill-document-snapshots [--tenant=slug] [--dry-run] [--active-only]`.
  **Ojo:** esa foto toma los datos VIGENTES al correrlo; lo que ya se editó antes no se puede recuperar.

## 3. Lo que no cubre (a propósito o pendiente)

- **Vista previa de una compra o venta sin registrar:** usa datos vivos (todavía no es un documento).
- **Pedidos de la tienda virtual** (`internal/ecommerce/service/print_data.go`): arman su propio print_data; falta aplicarles
  `FreezePrintData` si se quiere el mismo comportamiento.
- **Orden de compra:** su PDF se arma en el navegador con datos vivos (es un documento interno editable hasta enviarse).
- **PDF que genera el facturador (Lycet):** sale de su propio payload ya guardado; no se tocó.
- **Imágenes por URL** (QR de la billetera): se guarda la URL, no el archivo.
- **Nombres de unidad de venta** (p. ej. «Saco 100 KG») que el frontend resuelve por `sale_unit_id`: siguen en vivo. Los
  ítems del PDF de venta usan el código de unidad guardado en la línea.

## 4. Prueba

`internal/sales/service/print_snapshot_test.go`: emitir → editar cliente, razón social, RUC, dirección/teléfono de sucursal y logo →
reimprimir: no cambia nada (incluido el QR); un comprobante nuevo sí toma los datos vigentes. Una foto por documento; el logo se
guarda una vez. Verificado también en el navegador con el demo: tras editar empresa y proveedor, el PDF de la compra F777-00009001 y
el `print_data` de la venta F001-31 conservan los datos originales.

## 5. Despliegue

1. Backend con la migración **V190** (`migrate-fleet`): solo crea dos tablas nuevas.
2. Desplegar frontend de Tukifac (el PDF de compras lee `print_snapshot` del detalle).
3. Opcional, cuando se quiera cerrar la ventana de los documentos viejos: `backfill-document-snapshots --dry-run` y luego sin `--dry-run`.
