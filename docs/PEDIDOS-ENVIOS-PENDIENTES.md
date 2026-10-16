# Pedidos y envíos — estado y pendientes

Submódulo de **Vender → Pedidos y envíos** (tracking de pedidos de la tienda virtual con agencias tipo Shalom/Olva).
Código en dos repos: este (`mistiq_backend`) y `mistiq_tenant` (panel). Ambos en `main`.
Decisiones técnicas: `docs/ECOMMERCE-EVOLUTION-CONTRACT.md`, sección 13.

## Qué está hecho

**F1 — envíos**
- Empresas de envío (`tenant_carriers`): días de despacho, plazo de recojo (15 por defecto), aviso previo, enlace de rastreo.
- Pedido por agencia (`ENVIO_AGENCIA`): empresa, ciudad (ubigeo), agencia, quién recoge con DNI/CE/RUC, bultos, peso y costo del envío (informativo, no entra a la venta).
- Despacho: guía obligatoria por agencia, despacho por lote, estado `EN_AGENCIA` con fecha límite de recojo, retorno con motivo, entrega desde DESPACHADO/EN_TRANSITO/EN_AGENCIA.
- Reserva de stock **derivada** del pedido (`pkg/stockreserve`): se valida al confirmar; la respetan ventas, transferencias y salidas manuales; «Cuánto me queda» muestra reservado/disponible.
- Rótulos A4 de 1/2/4/6 por hoja o térmica 100×150, con vista previa y «+N más» (`mistiq_tenant/src/utils/shippingLabelSheetPdf.ts`).
- Tablero por etapas, lista con selección múltiple (imprimir rótulos, ir a despacho), En agencia, Retornos, checkout «A otra ciudad» en la tienda.

**F2 — pagos**
- Abonos por pedido (`tenant_ecommerce_order_payments`), validación opcional (ajuste), validar/observar/quitar.
- La venta toma los abonos validados como pagos y deja el saldo a crédito (Cobros pendientes), vencimiento = plazo de recojo; el cliente se crea con el DNI de quien recoge.
- «Emitir la venta al despachar» (ajuste + comprobante por defecto), con resultado por pedido.

**Combos en la tienda**
- La tienda ofrece los combos (grupos fijos, «elige uno» y «elige varios», con extras de precio). El cliente los arma en la ficha del producto; el carrito los distingue por la elección hecha.
- El pedido guarda la elección y el detalle de componentes por línea (`combo_json`, `combo_components_json`, migración **V146**); el precio lo calcula siempre el servidor (`combos.Resolve`).
- La reserva y la validación al confirmar se hacen **por componente** (`pkg/stockreserve`); al convertir en venta se descuentan los componentes y la línea lleva sus modificadores.
- Un combo sale «Agotado» en la tienda si no alcanza el stock de alguna de sus opciones obligatorias (con «Mostrar stock» desactivado no se calcula). Sin grupos u opciones, no se ofrece.
- El panel muestra los componentes bajo la línea (con aviso si falta stock) y el seguimiento público muestra «Incluye: …».

**F3 (parcial)**
- Seguimiento público `/ecommerce/seguimiento` (número + celular) y enlace en los mensajes de WhatsApp.
- Exportar la lista a CSV (abre en Excel).

Migraciones: **V144** (envíos), **V145** (abonos + ajustes) y **V146** (combos en las líneas del pedido). Corren solas en el deploy.

## Pendiente (por orden sugerido)

1. **Probar con datos reales** (nadie lo ha hecho fuera de la BD de pruebas): despacho con venta, impresión de rótulos, seguimiento desde un celular, abonos con «validar pagos» activado.
2. **Pedido manual**: botón del mockup, para pedidos que llegan por WhatsApp sin pasar por la tienda (crear pedido desde el panel con cliente, destino y productos).
3. **Foto del voucher** en el abono (hoy solo monto, medio, n.º de operación y fecha). El campo `voucher_url` ya existe en el modelo; falta subir el archivo (usar el patrón de `uploadEcommerceImage`).
4. **Diseño anterior (`?ui=1`)**: las páginas nuevas existen solo en el diseño nuevo; el menú viejo no las lista.
5. **Mejoras menores**
   - Mostrar los componentes del combo en el rótulo y la nota de preparación impresa (hoy solo van en el detalle del pedido).
   - Exportar a Excel real (.xlsx) en vez de CSV.
   - Insignias con conteos en el menú lateral (por vencer, por despachar).
   - Cobrar el saldo desde el propio pedido (hoy se cobra en Cobros pendientes).
   - Aviso/notificación automática cuando falten N días para que venza el plazo de recojo (el plazo y el aviso previo ya se configuran, solo se muestran en pantalla).
   - Anular la venta desde el retorno (hoy el panel avisa y se anula en Mis ventas).
6. **Tests que fallan por el entorno** (no por este módulo): billing Greenter (necesita PHP), prepayment, superadmin (timeout de login). Revisar en una máquina con PHP.

## Cómo seguir desde otra PC

1. Clonar `mistiq_backend` y `mistiq_tenant` (y los demás repos del proyecto), ambos en `main`.
2. Backend: `go run . migrate-bump-target` y `go run . migrate-fleet --limit=10 --workers=1` para llevar la BD local a V146.
3. Para probar rápido, en un tenant de pruebas: crear empresas de envío (Vender → Pedidos y envíos → Empresas de envío), activar «controlar stock» en un producto con stock, y hacer pedidos desde `/ecommerce/` con «A otra ciudad».
4. Los mockups aprobados están en `docs/pedidos-envios-mockups/` (fuente `.dc.html`, `canvas.json` y `mq.css`) y publicados en el Artifact «Pedidos y envíos — mockups» de claude.ai (el enlace está en el historial de la conversación original).
5. Panel: `npm run dev`; verificar con `npx tsc --noEmit -p .`, `npm run check:nav` y `npm run build`.

## Detalles que conviene recordar

- Los pedidos se muestran como `PW-<id>` (igual que las notificaciones).
- «Transportistas» (menú de Guías de remisión) es otra cosa: transporte para GRE. Aquí se llama **Empresas de envío**.
- Emitir la venta al despachar necesita caja abierta del usuario, igual que convertir a mano; si falla, el despacho queda hecho y el error se informa por pedido.
- Las rutas públicas nuevas son `/api/public/ecommerce/carriers`, `/ubigeo/*` y `/track` (esta última con límite de 20 solicitudes por minuto por IP).
