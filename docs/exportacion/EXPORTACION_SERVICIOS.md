# Exportación de servicios a clientes del exterior (operaciones 0201, 0202 y 0208)

Rama `feature/exportacion-servicios` (backend, frontend Tukifac y facturador Lycet). Nada de esto está en `main`.

## Qué resuelve
Hasta ahora la factura exigía RUC. Una empresa que presta un servicio a alguien del exterior (un hotel con un huésped extranjero, una consultoría
con un cliente en EE. UU.) no podía facturar. Esta rama agrega las **exportaciones de servicios**: IGV 0 %, sin RUC del adquirente.

| Operación (catálogo 51) | Para qué | Dato que pide el comprobante |
|---|---|---|
| **0201** | Servicio prestado íntegramente en el país a un no domiciliado | País donde se usa el servicio |
| **0202** | Hospedaje (con alimentación) a no domiciliados | Huésped, documento, países, ingreso al país, ingreso/salida del hotel |
| **0208** | Servicio realizado parcialmente en el extranjero | País donde se usa el servicio |

No incluye la exportación de bienes (0200: requiere DAM e Incoterm) ni la 0401 (ver «Pendiente»).

## Cómo se usa
1. **Ajustes → Comprobantes → Exportación de servicios**: activar el interruptor (apagado por defecto; `tenant_company_configs.exports_enabled`).
2. **Nuevo comprobante** (factura o boleta): en «Tipo operación» aparecen tres opciones «Exportación – …».
3. Un solo recuadro con lo mínimo de esa operación. Las líneas pasan solas a **afectación 40** (IGV 0) y la factura no exige RUC.
   - **0201/0208:** nada obligatorio. El país donde se usa el servicio es **opcional** (queda escondido tras un enlace): la guía de SUNAT lo marca
     obligatorio, pero el beta aceptó factura y boleta sin él (12-nov-2026). Si se indica, se envía; confirmar en producción.
   - **0202 (hospedaje):** el **cliente elegido es el huésped**: su nombre, tipo y número de documento salen del cliente (pasaporte o carné de
     extranjería; no sirve «Clientes varios»), no se vuelven a pedir. Solo se piden el país del pasaporte (la residencia es la misma salvo que se indique
     otra), la fecha de ingreso al Perú y las fechas de entrada y salida del hotel (los días se calculan).

## Base legal (leída de fuentes oficiales, 12-nov-2026)
- Ley del IGV (TUO, DS 055-99-EF) **art. 33.4**: el hospedaje con alimentación a no domiciliados es exportación si no pasa de **60 días** por cada ingreso al país y
  se presenta el pasaporte. DS 122-2001-EF (modificado por DS 199-2013-EF y 342-2017-EF): hotel inscrito en el Registro Especial de SUNAT y con certificado de
  categorización; leyenda **«EXPORTACIÓN DE SERVICIOS - DECRETO LEGISLATIVO Nº 919»**; solo hospedaje y alimentación en esa factura; si no califica, se restituye el IGV.
- Reglamento de Comprobantes de Pago, art. 4.1.1 d) y 4.1.2: la factura de exportación se emite sin RUC. RS 097-2012 art. 17.2 y anexo de la RS 123-2022:
  el adquirente se identifica con **«-»**.
- Catálogo 51 (0201/0202/0208) y catálogo 55 «Beneficio Hospedajes» 4000–4009 (anexo de la RS 108-2026); país de uso del servicio: campo 51 de la guía XML.

## Qué se envía al facturador
- `tipoOperacion` = 0201/0202/0208; líneas con `tipAfeIgv` 40 (tributo 9995 EXP); en **factura** el adquirente es `tipoDoc "-" / numDoc "-"` (la boleta conserva su identificación).
- **0202**: por línea, `atributos` 4000–4009 (países, fechas a mediodía hora Perú, días, huésped); razón social = nombre del huésped; leyenda del DL 919 en el PDF.
- **0201/0208**: atributo reservado `EXP_PAIS_USO`, que **Lycet** convierte en `/Invoice/cac:Delivery/cac:DeliveryLocation/cac:Address/cac:Country` (`TukifacInvoiceBuilder::injectExportCountry`).

## Orden de despliegue
Backend (migración **V186**: `tenant_sale_exports` + `exports_enabled`) → facturador Lycet (otro VPS) → frontend. Sin el cambio de Lycet, 0201/0208 enviarían
el atributo reservado como propiedad del ítem y SUNAT lo rechazaría; 0202 funciona sin tocar Lycet.

## Pruebas en SUNAT beta (12-nov-2026, RUC de pruebas 10726187938 con MODDATOS)
Se levantó el facturador local con el código de la rama y se emitió contra el ambiente de pruebas de SUNAT, primero con un arnés directo y luego por el
sistema completo (backend → facturador → SUNAT). **Todas aceptadas con CDR 0:**

| Comprobante | Resultado |
|---|---|
| Factura 0201 (servicio al exterior, país de uso) | aceptada |
| Factura 0202 (hospedaje, datos del huésped) | aceptada |
| Factura 0208 (parcialmente en el extranjero) | aceptada |
| Boleta 0201 y boleta 0208 | aceptadas |
| Nota de crédito de una factura 0201, de una factura 0202 y de una boleta de exportación | aceptadas |

SUNAT rechazó tres cosas que las pruebas locales no podían ver, ya corregidas:
1. **3107** (código de tributo global inválido para el tipo de operación): Greenter escribe siempre un subtotal de IGV (1000) con 0; en una exportación debe ir
   solo 9995/EXP. Lycet lo quita (`TukifacInvoiceBuilder::stripIgvTaxSubtotal`). `TukifacSee` ahora también aplica el ajuste a toda exportación (antes solo con atributos reservados).
2. **3065** (falta el tag de la fecha del concepto 4004): la fecha de salida del hotel debe ir en `cbc:StartDate`, no solo en `cbc:EndDate`.
3. **3206** (catálogo 51): **la operación 0202 no existe para boleta**. Solo se emite en factura (backend y pantalla lo impiden).
   Además SUNAT exige el concepto 4002 (ingreso al país): sin él rechaza con 3141.

## Reportes
- Registro de ventas (formato 14.1): las ventas de exportación salen en la columna «Exportación» sin IGV; el hospedaje 0202 muestra al huésped (nombre y pasaporte)
  como adquirente en lugar del cliente genérico; las notas de crédito entran en negativo.
- Reporte de ventas y dashboard: incluyen las exportaciones con IGV 0 (verificado por API).

## Verificado
- Go: reglas de validación, venta (0201/0202/0208, IGV 0, rechazos, 0202 solo en factura), payload, fechas, registro de ventas, migración V186 en MariaDB y por el runner de la flota.
- PHP (Lycet): 7 pruebas del XML (país de uso, orden UBL, atributos 4000–4009, sin IGV 1000, XML idéntico a Greenter fuera de exportaciones).
- En pantalla (local): interruptor, formulario, boleta/factura; el selector de boleta no ofrece 0202.
- SUNAT beta: ver arriba.

## Pendiente
1. Probar contra SUNAT **producción** con un caso real controlado antes de abrirlo a clientes (el beta no valida todo lo que valida producción).
2. Notas de débito de exportación (no se probaron; las de crédito sí).
3. Resumen diario / comunicación de baja de boletas de exportación (no se probó).
4. Decidir qué hacer con la operación **0401** existente (factura sin RUC a no domiciliados): no se encontró norma que la respalde y nunca se emitió con éxito en producción.
5. Antes de que un hotel real use el 0202: inscripción en el Registro Especial de SUNAT, certificado de categorización y archivo de copias del pasaporte/TAM.
6. MySQL 8 (producción): V186 es un `AutoMigrate` de una tabla nueva y una columna; solo se probó en MariaDB.

## Simplificación (12-nov-2026): país en el cliente y fechas por defecto

- `tenant_contacts.country` (V187, opcional, ISO alfa-2; vacío = Perú). Se registra al crear al cliente con pasaporte/carné de extranjería. El hospedaje (0202) toma de ahí el país del pasaporte, así que el formulario de venta ya no lo pide (solo si el cliente no lo tiene).
- Las fechas del hospedaje son opcionales: vacías = día de la emisión (ingreso y salida del hotel hoy; ingreso al país = ingreso al hotel). SUNAT exige los atributos 4002-4009, por eso siempre se envían, con esos valores por defecto. Quien necesite otras fechas usa «Cambiar fechas». La regla de 60 días solo se puede validar con la fecha real de ingreso al país; con el valor por defecto queda bajo responsabilidad del hotel.
