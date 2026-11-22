# Hotfix: ventas y cotizaciones en dólares con los precios sin convertir (11-nov-2026)

## El defecto
Al cambiar el documento a USD, el carrito **no convertía los precios**: el catálogo está en soles y la línea conservaba el mismo número, así que un producto de S/ 100 se emitía como **US$ 100**. Solo cambiaba la etiqueta de la moneda. La validación de precios del servidor comparaba la línea con el precio de catálogo en soles, por lo que lo aceptaba (y rechazaba el precio correcto en dólares a quien no tenía `sales.override_price`).

## La corrección (en `main`, sin push)
**Backend**
- `ValidateAuthorizedPricesInCurrency` / `validateAuthorizedPricesFX`: con un documento en USD el precio autorizado es el **precio de catálogo (soles) ÷ TC del documento**. Tolerancia de redondeo: ±0.005 US$ por unidad (±0.1 % en unidades de venta fraccionarias: gramo, mililitro). Sin TC válido no se puede autorizar un precio en dólares. En PEN todo queda idéntico.
- La venta (`SaleService.Create`) y la cotización (crear y editar) pasan su moneda y TC a esa validación. `sales.override_price` sigue eximiendo.

**Frontend (registro de venta / nota de venta / cotización)**
- Cada línea de catálogo recuerda su **precio en soles de origen** (`pen_unit_price`); en un documento en USD su precio es ese precio ÷ TC y se **recalcula** al cambiar la moneda o el TC. Teclear el precio a mano rompe el vínculo (precio pactado).
- Al cambiar la moneda, las **líneas manuales** y los **descuentos en monto** conservan su valor (PEN→USD divide, USD→PEN multiplica con el TC vigente).
- El **selector de productos** muestra el precio de venta convertido en USD.

## Pruebas
- Go: nuevos tests (precio convertido exacto y a centavos OK; US$ 100 por un producto de S/ 100 rechazado; precio distinto rechazado; con `override_price` se acepta; sin TC se rechaza; PEN sin cambios). Suites de ventas, cotizaciones, tienda virtual y membresías en verde.
- Front: `tsc` limpio, vitest 101 ✔.
- **En vivo (demo local)**: en USD con TC 3.421 el selector muestra US$ 2.34 para un producto de S/ 8; la línea entra con P. UNIT 2.338498 (total US$ 2.34); con TC 4 pasa a 2.00; al volver a PEN, S/ 8.00; con una línea manual de S/ 100 y la de catálogo, el cambio a USD (TC 4) da US$ 2.00 + US$ 25.00 = **US$ 27.00**; la nota de venta se registró (NV001-00000168, moneda DÓLARES, P. UNIT 2.00).

## Límites (siguen en la propuesta de multimoneda)
- Los pagos y la caja siguen registrando el importe en la moneda del documento sin convertir a soles (programa de multimoneda, Fase 3).
- El modal de configuración de producto (presentaciones, extras, unidades) muestra sus precios en soles; al agregar la línea se convierte.
- POS, Tukichef y tienda virtual venden solo en soles (sin cambios).
- Verificar en producción si existen ventas/cotizaciones en USD ya emitidas con precios sin convertir (consulta de solo lectura; no se modificaron documentos históricos).
