# Pruebas e implementación — Facturación electrónica (Tukifac + Lycet)

## Qué guarda cada sistema

### Backend facturador (Lycet)

Según la documentación (`API-facturacion.md`):

- **Solo guarda:** usuario SOL, clave SOL, certificado digital y logo por empresa (archivo `empresas.json` y archivos en `data/`).
- **No guarda:** comprobantes, historial de envíos, XML, CDR ni PDF.

Por tanto, **todo el historial de documentos (XML, CDR, PDF) y el estado de cada comprobante se gestiona en este backend (Tukifac)**.

### Este backend (Tukifac)

- **Base de datos (tenant):**
  - `tenant_invoices`: por cada venta enviada a SUNAT se guarda `sale_id`, `sunat_status`, `sunat_message`, `sent_at`, `response_at`, `xml_url`, `cdr_url`, `pdf_url` (rutas relativas a los archivos).
- **Archivos en disco** (carpeta configurada con `INVOICE_STORAGE_PATH`, por defecto `./storage/invoices`):
  - **XML:** comprobante firmado (UBL 2.1) — `{ruc}/{tipo}-{serie}-{correlativo}.xml`
  - **CDR:** ZIP que devuelve SUNAT con la respuesta — `{ruc}/{tipo}-{serie}-{correlativo}.cdr.zip`
  - **PDF:** representación impresa del comprobante (generado por el facturador) — `{ruc}/{tipo}-{serie}-{correlativo}.pdf`

Tras un envío exitoso a SUNAT (factura o boleta), este backend:

1. Guarda el XML devuelto por el facturador en disco y su ruta en `tenant_invoices.xml_url`.
2. Decodifica el CDR en base64, lo guarda como `.cdr.zip` y guarda la ruta en `tenant_invoices.cdr_url`.
3. Solicita el PDF al facturador (`POST /invoice/pdf` con el mismo payload), lo guarda en disco y guarda la ruta en `tenant_invoices.pdf_url`.

---

## Cuándo se actualiza `empresas.json` en el facturador

El archivo `empresas.json` del facturador se actualiza **solo cuando alguien dispara la sincronización**:

- **Panel tenant:** Configuración → SUNAT / IGV → botón **«Sincronizar con facturador»** (`POST /api/company/sync-facturador`).
- **Panel central:** Empresas → elegir tenant → botón escudo (SUNAT / Facturador) → **«Sincronizar con facturador»** (`POST /api/superadmin/tenants/:id/sync-facturador`).

**No** se actualiza automáticamente al crear un nuevo tenant ni al editar datos de la empresa. Por tanto:

- Al dar de alta un nuevo tenant con facturación electrónica, hay que **sincronizar una vez** (desde el panel tenant o desde el central) después de configurar usuario SOL, clave SOL y tener certificado/logo en el facturador.
- Si cambias usuario SOL, clave SOL o datos de la empresa, vuelve a usar **«Sincronizar con facturador»** para que Lycet tenga los datos actualizados.

**Importante (multi-tenant con un solo Lycet):**  
Al sincronizar un tenant se envía **solo la entrada de ese RUC** en `empresas.json`. Si el facturador **reemplaza** todo el archivo (en vez de hacer merge por RUC), al sincronizar el tenant A se borrarían las entradas de los demás. En ese caso hay que:

- usar un facturador que haga merge por RUC, o  
- tener un solo tenant por instancia de Lycet, o  
- implementar en Tukifac un «sincronizar todos los tenants» que construya el `empresas.json` completo con todos los RUCs y lo envíe una sola vez.

---

## Certificado para Lycet: PEM combinado (clave privada + certificado)

Lycet necesita **un único PEM** que contenga:

1. **Primero:** el bloque de la **clave privada** (`-----BEGIN PRIVATE KEY-----` o `-----BEGIN RSA PRIVATE KEY-----` ... `-----END ... -----`).
2. **Después:** el bloque del **certificado** (`-----BEGIN CERTIFICATE-----` ... `-----END CERTIFICATE-----`).

Desde el **panel central** (Empresas → SUNAT/Facturador) puedes:

- Subir **Clave privada .pem** y **Certificado .pem** por separado: este backend los combina en ese orden y envía el resultado a Lycet en `POST /api/v1/configuration/` (campo `certificate` en base64).
- O subir un único archivo que ya contenga ambos bloques y enviarlo como **Certificado .pem** (sin clave por separado).

Si solo envías el certificado público (sin clave privada), Lycet devolverá `openssl_sign(): Supplied key param cannot be coerced into a private key` al intentar firmar el XML.

### PFX / P12 (panel central)

El backend convierte el archivo a PEM antes de enviarlo a Lycet (`pkg/facturador/cert_pfx.go`):

1. Parser Go (`go-pkcs12`).
2. Si falla (certificados SUNAT en formato legacy/BER) → **OpenSSL** `pkcs12 -legacy`.

La imagen Docker de producción incluye el paquete `openssl` en Alpine. Tras cambiar el `dockerfile`, hay que **reconstruir y redesplegar** la imagen en el VPS (push a `main` o workflow manual).

---

## Flujo de una venta (factura/boleta) hasta SUNAT

1. En el **panel tenant**, el usuario registra una venta con tipo de comprobante Factura (01) o Boleta (03) y serie correspondiente.
2. La venta queda con `billing_status` en `pending` (o similar) hasta que se envíe a SUNAT.
3. El usuario (o un proceso) dispara **«Enviar a SUNAT»** para esa venta → `POST /api/billing/send/:saleId`.
4. Este backend:
   - Construye el payload UBL según `API-facturacion.md` (empresa, cliente, ítems, totales, leyendas).
   - Llama al facturador `POST /api/v1/invoice/send?token=...` con ese payload.
   - El facturador firma, envía a SUNAT y devuelve XML, hash y respuesta (incl. CDR en base64 si es aceptado).
5. Este backend:
   - Actualiza `tenant_invoices` y `billing_status` de la venta (`accepted` / `rejected` / `error`).
   - Guarda XML, CDR (ZIP) y PDF en disco y guarda las rutas en `xml_url`, `cdr_url`, `pdf_url`.
6. El tenant puede **descargar** los documentos con:
   - `GET /api/billing/invoice/:saleId/document/xml`
   - `GET /api/billing/invoice/:saleId/document/cdr`
   - `GET /api/billing/invoice/:saleId/document/pdf`

---

## Cómo probar que todo funciona

### 1. Variables de entorno (backend Tukifac)

En `.env` (o entorno del servidor):

```env
FACTURADOR_BASE_URL=https://tu-lycet.com/api/v1
FACTURADOR_TOKEN=tu_client_token
INVOICE_STORAGE_PATH=./storage/invoices
```

- `FACTURADOR_BASE_URL`: base URL del API del facturador (sin barra final).
- `FACTURADOR_TOKEN`: mismo valor que `CLIENT_TOKEN` en el facturador.
- `INVOICE_STORAGE_PATH`: carpeta donde Tukifac guardará XML, CDR y PDF (debe existir o crearse con permisos de escritura).

### 2. Configuración del tenant

- **Panel tenant** (o panel central para ese tenant):
  - Configuración → SUNAT: activar facturación electrónica, ambiente (beta/producción), usuario SOL, clave SOL.
  - En el facturador deben existir el certificado y el logo para el RUC (p. ej. en `data/{ruc}-cert.pem` y `data/{ruc}-logo.png`), o subirlos vía `POST /api/v1/configuration/` con certificado/logo en base64.
- Pulsar **«Sincronizar con facturador»** para que Lycet tenga el RUC en `empresas.json` con SOL_USER, SOL_PASS y nombres de archivo.

### 3. Probar envío a SUNAT

1. Crear una venta en el panel tenant con tipo **Factura** o **Boleta** y serie que tenga código SUNAT 01 o 03.
2. Ir a Facturación Electrónica (o Ventas, según el flujo) y pulsar **«Enviar a SUNAT»** para esa venta.
3. Comprobar:
   - Respuesta JSON del `POST /api/billing/send/:saleId`: `success: true` y en `invoice` los campos `sunat_status` (p. ej. `accepted`), `sunat_message`, `xml_url`, `cdr_url`, `pdf_url`.
   - En disco, en `INVOICE_STORAGE_PATH/{ruc}/`, que existan los archivos `01-F001-1.xml`, `01-F001-1.cdr.zip`, `01-F001-1.pdf` (o el tipo/serie/correlativo que corresponda).
4. Probar descargas (con sesión del tenant y módulo billing activo):
   - `GET /api/billing/invoice/:saleId/document/xml` → debe devolver el XML.
   - `GET /api/billing/invoice/:saleId/document/cdr` → el ZIP del CDR.
   - `GET /api/billing/invoice/:saleId/document/pdf` → el PDF.

### 4. Si algo falla

- **«facturador no configurado»:** revisar `FACTURADOR_BASE_URL` y `FACTURADOR_TOKEN`.
- **«la conexión con SUNAT no está activada»:** activar facturación en Configuración → SUNAT y guardar.
- **«Error al sincronizar con el facturador»:** comprobar que el facturador esté en marcha, el token sea correcto y que el RUC tenga certificado/logo en el facturador.
- **SUNAT rechaza el comprobante:** revisar `sunat_message` y el CDR (descomprimir el ZIP y ver el XML de respuesta de SUNAT) para ver el motivo.
- **No se generan XML/CDR/PDF en disco:** comprobar permisos de escritura en `INVOICE_STORAGE_PATH` y que el facturador haya devuelto `xml` y `cdrZip` en la respuesta de `invoice/send`; el PDF se pide después con el mismo payload.

---

## Resumen

| Responsabilidad                         | Dónde se gestiona |
|----------------------------------------|-------------------|
| Usuario SOL, clave, certificado, logo  | Facturador (empresas.json + data/) |
| Sincronizar esa config con el facturador | Tukifac (panel tenant o central → «Sincronizar con facturador») |
| Enviar factura/boleta a SUNAT          | Tukifac llama al facturador; facturador firma y envía |
| Guardar XML, CDR, PDF                 | Tukifac (BD: rutas; disco: archivos) |
| Descargar XML, CDR, PDF                | Tukifac (`GET .../invoice/:saleId/document/:kind`) |
