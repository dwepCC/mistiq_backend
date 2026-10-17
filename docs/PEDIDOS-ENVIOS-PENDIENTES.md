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

**Pedido manual y voucher**
- «Pedido manual» (Pedidos web, permiso `ecommerce.orders_manage`): el personal registra un pedido que llegó por WhatsApp, llamada o mostrador. Cliente (se puede buscar uno existente), productos (también variantes y combos), y entrega por recojo, domicilio o agencia. Usa el mismo `CreateOrder` que la tienda: precio y stock salen del servidor.
- Diferencias con el pedido de la tienda: admite productos no publicados en la tienda y empresas de envío que la tienda no muestra; no genera el aviso «nuevo pedido online»; queda con `source = MANUAL` (migración **V147**; la lista lo marca como «Manual») y el historial guarda quién lo creó.
- Foto del voucher en el abono: se sube con `POST /api/ecommerce/payments/voucher` (JPG/PNG/WebP, nombre con sufijo aleatorio) y el abono guarda su URL. El servidor solo acepta URLs de voucher de la carpeta de ESE negocio. Se ve en el pedido como miniatura con «Ver voucher».

**Ajustes menores (octubre 2026)**
- **Aviso de recojo:** un trabajo cada 6 horas (`pkg/cron/pickup_reminders.go`) recorre los negocios y crea una notificación del panel cuando un paquete en agencia está por vencer (días de «aviso previo» de la empresa, 5 por defecto) y otra cuando venció el plazo. Cada aviso se crea una sola vez por pedido.
- **Cobrar el saldo desde el pedido:** el pedido muestra «Cobrar saldo» (permiso `receivables.collect`) y abre el mismo cobro de Cobros pendientes; el saldo del pedido sale de lo que sigue pendiente en la venta.
- **Combos en rótulo y nota:** el rótulo muestra «Combo (lo que lleva)» y la nota del pedido imprime «Incluye: …».
- **Retorno que anula la venta:** al registrar el retorno de un pedido con nota de venta, se puede anular la venta en el mismo paso (devuelve stock y dinero). Boleta o factura siguen pidiendo nota de crédito.
- **Insignias en las pestañas** de Pedidos y envíos: por confirmar, listos para despachar, en agencia y retornos.
- **Pedido manual:** costo del envío, bultos y peso, abono inicial con foto del voucher y búsqueda del nombre por DNI en la misma ventana.
- **Voucher:** el personal puede agregar o cambiar la foto de un abono ya registrado; el cliente puede enviar su voucher desde el seguimiento (queda PENDIENTE y avisa al negocio, máx. 3 sin revisar por pedido).
- **Excel real:** «Exportar a Excel» genera un .xlsx (cifras como números, filtros y primera fila fija).

**F3 (parcial)**
- Seguimiento público `/ecommerce/seguimiento` (número + celular) y enlace en los mensajes de WhatsApp.
- Exportar la lista a CSV (abre en Excel).

Migraciones: **V144** (envíos), **V145** (abonos + ajustes), **V146** (combos en las líneas del pedido) y **V147** (origen del pedido). Los ajustes menores posteriores no necesitaron migración. Corren solas en el deploy.

## Pendiente (por orden sugerido)

1. **Probar con datos reales** (nadie lo ha hecho fuera de la BD de pruebas): despacho con venta, impresión de rótulos, seguimiento desde un celular, abonos con «validar pagos» activado.
2. **Diseño anterior (`?ui=1`)**: decisión del negocio: se trabaja solo con el diseño nuevo, así que el menú viejo no se actualiza (las páginas de envíos se alcanzan solo por el menú nuevo).
3. **Mejoras menores** (todas las anteriores ya están hechas; esto es lo que queda)
   - Aviso por WhatsApp automático (hoy el aviso de recojo es del panel y el mensaje se envía con un clic).
   - Badges también en el menú lateral de áreas (hoy están en las pestañas de Pedidos y envíos).
   - Que el voucher que sube el cliente pueda ser un PDF (hoy solo imágenes).
   - Reintentar solo el abono o el paquete del pedido manual cuando falla justo después de crearlo (hoy se completa desde el pedido).
4. **Tests que fallan por el entorno** (no por este módulo): billing Greenter (necesita PHP), prepayment, superadmin (timeout de login). Revisar en una máquina con PHP.

## Cómo seguir desde otra PC

1. Clonar `mistiq_backend` y `mistiq_tenant` (y los demás repos del proyecto), ambos en `main`.
2. Backend: `go run . migrate-bump-target` y `go run . migrate-fleet --limit=10 --workers=1` para llevar la BD local a V147.
3. Para probar rápido, en un tenant de pruebas: crear empresas de envío (Vender → Pedidos y envíos → Empresas de envío), activar «controlar stock» en un producto con stock, y hacer pedidos desde `/ecommerce/` con «A otra ciudad».
4. Los mockups aprobados están en `docs/pedidos-envios-mockups/` (fuente `.dc.html`, `canvas.json` y `mq.css`) y publicados en el Artifact «Pedidos y envíos — mockups» de claude.ai (el enlace está en el historial de la conversación original).
5. Panel: `npm run dev`; verificar con `npx tsc --noEmit -p .`, `npm run check:nav` y `npm run build`.

## Detalles que conviene recordar

- Los pedidos se muestran como `PW-<id>` (igual que las notificaciones).
- «Transportistas» (menú de Guías de remisión) es otra cosa: transporte para GRE. Aquí se llama **Empresas de envío**.
- Emitir la venta al despachar necesita caja abierta del usuario, igual que convertir a mano; si falla, el despacho queda hecho y el error se informa por pedido.
- Las rutas públicas nuevas son `/api/public/ecommerce/carriers`, `/ubigeo/*` y `/track` (esta última con límite de 20 solicitudes por minuto por IP).
