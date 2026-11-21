# Porte de tukifac a Mistiq (backend) — estado

Mistiq nació de `tukifac_premium` el **30-may-2026** (último commit común: `0d0030e`). Desde entonces tukifac sumó
~490 commits y Mistiq ~80. Esta rama (`feat/portar-tukifac`) trae lo de tukifac **sin** el módulo Equipos/Shalom (control
interno de la empresa tukifac) y sin sus `docs/`, `deploy/` propios ni el empaquetado.

## Método
1. Se clasificó cada archivo comparando contenido sin fines de línea (tukifac guarda CRLF, Mistiq LF):
   igual / nuevo en tukifac / Mistiq quedó con una versión vieja de tukifac / solo Mistiq / cambiado en ambos.
2. Nuevos y desactualizados se copiaron; los cambiados en ambos se fusionaron en 3 vías usando como base la versión de
   tukifac más parecida a la de Mistiq; los conflictos (16 archivos) se resolvieron a mano.
3. Verificación: `go build`, `go vet`, `go test ./...` y migración real (`migrate-central`, `migrate-bump-target`,
   `migrate-fleet`, `migrate-backfill-fleet`) sobre la BD local `demomistiq` sin errores (V148..V208).

## Migraciones de tenant (IMPORTANTE)
Mistiq ya usaba V133..V147 para ecommerce y tukifac usaba esos mismos números para otras cosas, así que las migraciones de
tukifac **V133..V192 se renumeraron +15 → V148..V207** (mismo orden). V208 es propia de Mistiq (campos de unidad de venta y
extras en las líneas del pedido web). Los backfills mantienen su numeración (`tenantbackfills`, V146 de vuelto mixto).
Para el próximo porte: las migraciones nuevas de tukifac (V193 en adelante) deben entrar como V209 en adelante (+16).

## Qué se trajo (resumen)
Sucursales (aislamiento por sucursal, permisos `branches.*`, versión de sesión, emisor único de JWT), permisos finos
(clientes/proveedores, caja y bancos, POS, costos, reportes), contabilidad (registros, libro mayor, SIRE), compras
(órdenes de compra, multimoneda, presentaciones), caja y bancos (transferencias entre cuentas, catálogo de bancos, saldos
por moneda, métodos de pago con logo), exportación de servicios, recargo al consumo, notas de crédito por monto y
parciales, detracción 1004, unidades de venta y venta fraccionada, productos por sucursal y categorías por sucursal,
importación avanzada de productos, tienda virtual (PDF del pedido con enlace firmado, medios de pago, galería, estilo del
banner), resumen fiscal en la BD central, telemetría RUM, planes (módulos despatch / docs avanzados), mejoras del cron de
suscripciones y del deploy.

## Ecommerce: integración con lo propio de Mistiq
El pedido web de Mistiq (tablas normalizadas, reserva de stock, envío por agencia, abonos, combos, pedido manual) se
conserva y se le sumó lo de tukifac: unidad de venta y extras por línea (el servidor calcula nombre y precio), validación
de stock total al crear el pedido, PDF del pedido, medios de pago informativos, galería y opciones del producto.
`stockreserve` usa la cantidad en unidad base.

## Lo que NO se trajo
- `internal/equipos`, `internal/shalom`, modelos `equip_*` y su cron (decisión del dueño).
- `docs/` de tukifac, `.env*` (solo se añadieron `DB_TENANT_MAX_IDLE` y el bloque SMTP) y su empaquetado.
- Se conservaron el despliegue y los nombres de Mistiq (el deploy ganó la migración de todos los tenants antes del reinicio).

## Antes de desplegar a producción
- **El panel web del negocio (`mistiq_tenant`) todavía no usa lo nuevo**: cambiaron permisos (p. ej. clientes y proveedores
  separados), el modo/cabecera de sucursal y el JWT. Hasta portar ese panel, algunas pantallas pueden mostrar o negar
  acciones de forma distinta. No desplegar sin probarlo con el panel.
- Correr la suite en una máquina con PHP (los tests de Greenter fallan sin PHP; es del entorno).
- Backup de la BD central y de los tenants; las migraciones solo agregan.
