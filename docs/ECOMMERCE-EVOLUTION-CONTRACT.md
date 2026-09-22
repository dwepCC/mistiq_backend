# Contrato técnico — Evolución del Ecommerce Mistiq

Estado: **Fase 1, Fase 1.5, Fase 2, Fase 3, Fase 4, Fase 5, Fase 6, Fase 7, Fase 8, Fase 9 y Fase 10: APROBADAS.** Fase 10 incluye la decisión de negocio explícita de mantener GRE sin integrar con el despacho ecommerce — la guía de remisión se sigue emitiendo aparte, desde Billing → Guías (ver bitácora de Fase 10). Aprobación general recibida sobre la v2, cerrando las decisiones pendientes: variantes vía `TenantProductPresentation` con nombre compuesto (sin nueva infraestructura), `PaymentStatus` como marcador inerte no editable por el cliente público, y `ecommerce.orders` como permiso legacy deprecado (sin retirar, sin nuevas asignaciones, sin inferencia automática sobre roles personalizados). Ver bitácora de fases al final del documento para el estado real de avance.

Referencia de auditoría original: `TenantEcommerceOrder` ([pkg/database/migrations.go:1071](../pkg/database/migrations.go)), `ConvertToSale` ([internal/ecommerce/service/convert.go](../internal/ecommerce/service/convert.go)), RBAC de tenant ([pkg/middleware/tenant_permissions.go](../pkg/middleware/tenant_permissions.go), [internal/users/service/role_service.go](../internal/users/service/role_service.go)), hub SSE de billing ([pkg/billingevents](../pkg/billingevents)), GRE ([internal/billing/service/despatch_payload.go](../internal/billing/service/despatch_payload.go)), patrón PrintData+jsPDF ([internal/ecommerce/service/print_data.go](../internal/ecommerce/service/print_data.go)).

Referencia de la segunda auditoría (variantes, esta revisión): `TenantModifierGroup`/`TenantModifierOption` ([pkg/database/migrations.go:1113-1140](../pkg/database/migrations.go)), `pkg/modifierkind/kind.go`, migración histórica [v055_product_presentations.go](../pkg/database/tenantmigrations/v055_product_presentations.go), `ProductService.syncPresentations`/`syncModifierGroups`/`filterExtraModifierGroupIDs` ([internal/products/service/product_service.go:999-1110](../internal/products/service/product_service.go)).

---

## 0. Alcance, principio arquitectónico y decisiones aprobadas (sin cambios respecto a v1)

**Objetivo:** evolucionar `CATÁLOGO → CARRITO → WHATSAPP` hacia `CATÁLOGO → PRODUCTO → CARRITO → CHECKOUT → PEDIDO ONLINE → WHATSAPP → GESTIÓN INTERNA → PREPARACIÓN → EMPAQUETADO → DESPACHO → TRACKING → ENTREGA`, sin reescribir el ecommerce existente y sin duplicar inventario, clientes, ventas, sucursales, usuarios, RBAC, facturación o documentos ya existentes.

**Decisiones de negocio ya aprobadas (v1), no se vuelven a cuestionar:**

1. Ecommerce ligero y rápido, no un Shopify.
2. WhatsApp sigue siendo el canal, no el sistema de registro.
3. El pedido debe persistir en Mistiq **antes** de abrir WhatsApp.
4. Sin push, sin email, sin SMS por ahora. Solo notificaciones internas en el header del panel.
5. Sin pasarela de pago en esta evolución.
6. **Sin reserva de stock, sin HOLD, sin expiración/liberación de reservas, sin descuento de stock al crear el pedido** — reafirmado explícitamente en esta revisión (punto 9 del usuario): el pedido es una intención de compra que continúa por WhatsApp; el stock se comporta exactamente igual que hoy hasta que el pedido se convierte en venta.
7. La reserva de stock queda como capacidad futura marcada arquitectónicamente (sección 12), no implementada.
8. Despacho logístico y Guía de Remisión SUNAT son conceptos separados.
9. No se construye un sistema logístico gigante ni se reemplaza el ERP.
10. Arquitectura multi-tenant, respeta RBAC existente, evolución incremental.

**Nuevas decisiones aprobadas en esta revisión** (detalladas en sus secciones respectivas, resumen aquí):

11. **Pedido y Venta tienen ciclos de vida completamente independientes.** `ConvertToSale` ya no fuerza ningún cambio de `Order Status` (sección 4).
12. **RBAC granular por responsabilidad**, reemplazando el uso indiferenciado de `ecommerce.orders` (sección 7).
13. **`DEVUELTO` restringido a Administrador y Supervisor** (sección 5, 7).
14. **Sin picking persistente por línea en esta fase** — checkboxes de picking son ayuda visual efímera, no estado guardado (sección 1.2, 17).
15. **Flujo de despacho corregido**: `EMPAQUETADO → LISTO_PARA_DESPACHO → [botón Despachar] → DESPACHADO` (sección 5, 9-10).

---

## 1. Modelo de datos (entidades)

### 1.1 `TenantEcommerceOrder` — evolución del modelo existente

Sin cambios respecto a v1 en la lista de campos nuevos (`BranchID`, `CustomerAccountID`, `ContactID`, `DeliveryAddressID`, `DeliveryMethod`, `PaymentStatus`, `Subtotal`, campos `Guest*`). Ver v1 para la justificación campo por campo — se mantiene íntegra.

**Cambio de esta revisión:** se elimina cualquier lógica que ate `ConvertToSale` a una transición de `Status`. `ConvertedSaleID`/`ConvertedAt` se mantienen exactamente como hoy, solo como trazabilidad — ver sección 4.

```go
type TenantEcommerceOrder struct {
    ID                 uint
    CustomerName       string
    CustomerPhone      string
    CustomerAccountID  *uint      // FK TenantEcommerceCustomerAccount, null si invitado
    ContactID          *uint      // FK TenantContact, opcional
    BranchID           *uint      // FK TenantBranch, asignada en confirmación
    ItemsJSON          string     // DEPRECATED para pedidos nuevos, se mantiene por compatibilidad histórica
    Subtotal           float64
    Total              float64
    DeliveryMethod     string     // RECOJO_TIENDA | ENVIO_DOMICILIO
    DeliveryAddressID  *uint      // FK TenantEcommerceCustomerAddress, null si recojo en tienda
    PaymentStatus      string     // PENDIENTE | PAGADO | NO_APLICA (default NO_APLICA) — inerte, ver §12
    Status             string     // enum ampliado, sección 3 — ciclo de vida INDEPENDIENTE de la venta
    Notes              string
    ConvertedSaleID    *uint      // trazabilidad hacia TenantSale, no dispara cambios de Status
    ConvertedAt        *time.Time
    CreatedAt          time.Time
    UpdatedAt          time.Time
}
```

### 1.2 `TenantEcommerceOrderItem` — sin cambios estructurales, aclaración de alcance (punto 7 del usuario)

Se mantiene el struct de v1:

```go
type TenantEcommerceOrderItem struct {
    ID             uint
    OrderID        uint
    ProductID      uint
    PresentationID *uint   // FK TenantProductPresentation, null si el producto no tiene variantes
    Name           string
    Quantity       float64
    UnitPrice      float64
    Subtotal       float64
    CreatedAt      time.Time
}
```

**Aclaración explícita (nueva):** **no se agrega** `PickedQuantity` ni `FulfillmentStatus` por línea en esta fase. La auditoría de esta revisión no encontró ningún caso de uso real en el código actual (POS, Inventario, Restaurante) que dependa de picking parcial persistente por línea — todo lo que existe hoy opera a nivel de documento completo (una transferencia, un ajuste, una venta se confirman como un todo). Introducir persistencia de picking parcial sin un caso de uso real violaría el principio de "no agregar campos porque aparecieron en el brief". `EN_PREPARACION → EMPAQUETADO` representa la preparación del **pedido completo**; cualquier checkbox de picking en la UI del almacenero es **estado de React efímero** (se pierde al recargar la página), nunca se envía al backend como progreso parcial. Si en el futuro el negocio reporta una necesidad real (ej. pedidos con 20+ líneas que se preparan en varias sesiones), se audita y diseña esa pieza como su propia fase — no ahora.

### 1.3 – 1.7 Sin cambios respecto a v1, salvo el dominio de despacho (corregido tras Fase 8/9 — ver bitácora)

`TenantEcommerceOrderStatusHistory`, `TenantEcommerceCustomerAccount` (+ regla de vinculación segura 1.4.1/1.4.2), `TenantEcommerceCustomerAddress`, `TenantNotification` — se mantienen exactamente como en v1, sin cambios derivados de esta revisión.

**Dominio de despacho — corregido respecto al plan original de v1/v2:** el plan original de esta sección mencionaba `TenantEcommerceDispatch`/`TenantEcommerceCarrier` como par de entidades. La implementación real (Fase 8, decisión aprobada explícitamente) **no crea `TenantEcommerceCarrier`** — no existe ningún catálogo de transportistas para ecommerce. `TenantEcommerceDispatch` tiene `CarrierName`/`TrackingCode` como texto libre. El dominio de despacho real son DOS entidades: `TenantEcommerceDispatch` (Fase 8, migración v142) y `TenantEcommerceDispatchStatusHistory` (Fase 9, migración v143) — historial exclusivo del ciclo de vida del Dispatch, separado de `TenantEcommerceOrderStatusHistory` (exclusivo del ciclo de vida del Order). Ver sección 5 y bitácora de Fases 8/9 para el detalle completo.

### 1.8 Variantes — resultado de la segunda auditoría dirigida (punto 2 del usuario)

**Alcance de la auditoría solicitada:** se investigó específicamente `ProductAttribute`, `ProductAttributeValue`, `TenantProductPresentation`, `SaleUnit`, stock/precio por presentación, y su consumo en POS/Inventario.

**Hallazgos verificados (archivo:línea real):**

1. **`ProductAttribute`/`ProductAttributeValue`/`SaleUnit` no existen en el código.** Búsqueda exhaustiva (`grep -r "ProductAttribute|AttributeValue|SaleUnit|ProductVariant|VariantOption"` sobre todo `mistiq_backend`) — cero resultados. No es un caso de "no lo encontré todavía", es una confirmación negativa completa: ese vocabulario no está en el código.

2. **Existe una tabla `TenantUnit`** ([migrations.go:955](../pkg/database/migrations.go)) — pero es el catálogo de **unidades de medida SUNAT** (`Code`, `Name`, `Symbol`, ej. "NIU", "KGM"), referenciada por `TenantProduct.UnitID` para el catálogo N°03 de SUNAT. No tiene ninguna relación con variantes de talla/color — es un concepto de unidad de medida fiscal, no de presentación comercial. Confirmado que no debe confundirse ni reutilizarse para esto.

3. **Hallazgo crítico que no estaba en la auditoría v1: existió un sistema previo de variantes basado en `TenantModifierGroup`/`TenantModifierOption` con `Kind = "presentation"`, y Mistiq ya lo migró y deprecó deliberadamente.** Evidencia directa en la migración histórica [v055_product_presentations.go](../pkg/database/tenantmigrations/v055_product_presentations.go):
   - Antes de la migración v055, los grupos de modificadores con `Kind: "presentation"` (`pkg/modifierkind/kind.go`: `Presentation = "presentation"`, requerido + selección única) se usaban para representar variantes tipo "Rojo", "XL" vinculadas a un producto.
   - La migración v055 **lee todos los grupos `kind=presentation`, copia sus opciones a filas nuevas en `tenant_product_presentations`, borra el vínculo producto↔grupo, y reconvierte esos grupos a `kind=extra`** ([v055_product_presentations.go:68-128](../pkg/database/tenantmigrations/v055_product_presentations.go)) — es un backfill de migración de arquitectura, no una casualidad de nombres.
   - Confirmado además en el código de servicio actual: `ProductService.CreateModifierGroup` ([product_service.go:1759](../internal/products/service/product_service.go)) **hardcodea `Kind: modifierkind.Extra`** al crear grupos nuevos — hoy es **imposible crear un grupo `kind=presentation` desde la API actual**, ni siquiera pasando ese valor. Y `filterExtraModifierGroupIDs` ([product_service.go:1007](../internal/products/service/product_service.go)) excluye explícitamente cualquier grupo que no sea `extra` al sincronizar los grupos vinculados a un producto.
   - **Conclusión: `TenantModifierGroup`/`Option` con `Kind=presentation` es un camino muerto, deliberadamente cerrado por el propio equipo de Mistiq en una migración anterior.** No es infraestructura "por descubrir" — es infraestructura que existió, se usó, y se reemplazó a propósito por el sistema actual de una sola dimensión.
   - `TenantModifierGroup`/`Option` con `Kind=extra` sigue vivo y en uso, pero es un dominio distinto (extras que suman precio, ej. "agregar queso" en Restaurante) — no debe reutilizarse para variantes de producto.

4. **`TenantProductPresentation`/`TenantProductPresentationStock` son, confirmado de forma definitiva, el único sistema de variantes vigente**, de una dimensión (`Name` texto libre, sin atributo tipado), con stock y precio propios por sucursal, consumido activamente por POS (`POSProductCatalog.tsx`) e Inventario (`ProductTransferModal.tsx`, `StockAdjustmentModal.tsx`).

**Recomendación técnica concreta (reemplaza la "Opción A" de v1, ahora con evidencia de que es la línea arquitectónica que Mistiq ya eligió una vez):**

**Usar `TenantProductPresentation` tal cual, sin ninguna entidad nueva, incluyendo para combinaciones tipo Color + Talla, mediante nombre compuesto** (ej. `"Rojo / XL"`, `"Azul / M"`) como una fila de presentación por cada combinación real que el tenant vende. Esto:

- No requiere ninguna migración de esquema nueva para variantes (solo lo ya contemplado en v1: exponer `presentations` en `PublicProductsAPI` y aceptar `presentation_id` en el pedido).
- Es 100% compatible con POS/Inventario/stock-por-sucursal tal como funcionan hoy, porque es literalmente la misma tabla que ya usan.
- Respeta la decisión arquitectónica que Mistiq ya tomó en v055 (abandonar el modelo de atributos combinables vía modifier groups en favor de una lista plana por producto).
- **Trade-off honesto, para que quede explícito:** el frontend público no podrá ofrecer dos selectores independientes ("Color: ▾" y "Talla: ▾") que se crucen dinámicamente — mostrará una lista plana de combinaciones disponibles (ej. un `<select>` o chips con "Rojo / XL", "Rojo / M", "Azul / XL"...). Para catálogos con pocas combinaciones por producto (caso típico de una tienda pequeña/mediana) esto es perfectamente usable; para catálogos con muchas combinaciones por producto (ej. 5 colores × 6 tallas = 30 combinaciones) la UX de lista plana se degrada.

**Construir un modelo de atributos combinables real (`ProductAttribute`/`ProductAttributeValue`/tabla de combinaciones con SKU propio) es técnicamente posible pero significa reabrir una decisión de arquitectura que Mistiq ya cerró deliberadamente en v055, y tocar el modelo que POS/Inventario ya consumen en producción.** No se recomienda para esta evolución. Se dejaría como una iniciativa propia, separada, con su propio contrato técnico, si en el futuro el catálogo real de algún tenant lo exige (ej. una tienda de ropa con dutenas de combinaciones por producto y necesidad real de selectores cruzados).

**Esta recomendación se entrega como respuesta técnica al punto 2; queda pendiente únicamente tu confirmación final de seguir con ella (no requiere más auditoría de tu parte — ver sección 17).**

---

## 2. Migraciones necesarias

Sin cambios respecto a v1 en el principio (todo aditivo, mismo patrón que `v132_permission_catalog_redesign.go`, ninguna migración borra o renombra columnas existentes de `tenant_ecommerce_orders`).

**Tabla actualizada con los números y nombres REALES del registry** (`pkg/database/tenantmigrations/registry.go`) — la tabla original de v2 usaba nombres tentativos `vNNN_*`; se reemplaza acá por la lista real para que este documento no contradiga el código:

| Migración real | Contenido |
|---|---|
| `v134_ecommerce_order_fields.go` | ALTER `tenant_ecommerce_orders`: `branch_id`, `customer_account_id`, `contact_id`, `delivery_address_id`, `delivery_method`, `payment_status`, `subtotal`, `guest_address_line`, `guest_reference`, `guest_ubigeo` |
| `v135_ecommerce_order_status_expand.go` | Amplía validación de `status` al enum real (sección 3) |
| `v136_ecommerce_order_items.go` | CREATE `tenant_ecommerce_order_items` (sin `picked_quantity`/`fulfillment_status`, ver 1.2) |
| `v137_ecommerce_order_status_history.go` | CREATE `tenant_ecommerce_order_status_history` |
| `v138_ecommerce_orders_rbac_v2.go` | Crea los 6 permisos granulares (sección 7) + backfill de `ecommerce.orders` existente |
| `v139_tenant_notifications.go` | CREATE `tenant_notifications` (Fase 3 — solo la tabla; API de lectura + SSE es Fase 6) |
| `v140_ecommerce_customer_accounts.go` | CREATE `tenant_ecommerce_customer_accounts`, `tenant_ecommerce_customer_addresses` |
| `v141_notification_reads.go` | CREATE `tenant_notification_reads` (Fase 6) + completa índices de `tenant_notifications` que v139 nunca creó para tenants existentes |
| `v142_ecommerce_dispatch.go` | CREATE `tenant_ecommerce_dispatches` (Fase 8) — **NO** crea `tenant_ecommerce_carriers`: esa tabla nunca existió ni se planea, `CarrierName`/`TrackingCode` son texto libre dentro de `tenant_ecommerce_dispatches` (ver 1.3-1.7 y bitácora de Fase 8) |
| `v143_dispatch_status_history.go` | CREATE `tenant_ecommerce_dispatch_status_histories` (Fase 9) — historial exclusivo del Dispatch, separado de `tenant_ecommerce_order_status_histories` |
| — sin migración de datos de producto — | La recomendación de la sección 1.8 no requiere ninguna migración de esquema; es un cambio de API (exponer `presentations`), no de modelo |

---

## 3. Estados — sin cambios (aprobado en v1 y reconfirmado explícitamente, punto 6 del usuario)

### 3.1 Order Status

```
PENDIENTE → CONFIRMADO → EN_PREPARACION → EMPAQUETADO → LISTO_PARA_DESPACHO → DESPACHADO → ENTREGADO
```
Alternativos: `CANCELADO`, `RECHAZADO` (desde cualquier estado previo a `DESPACHADO`), `DEVUELTO` (solo desde `ENTREGADO`, **restringido a Administrador/Supervisor**, ver sección 5 y 7).

### 3.2 Payment Status — sin cambios

```
NO_APLICA (default) | PENDIENTE | PAGADO
```
Inerte en esta fase (decisión aprobada #6).

### 3.3 Dispatch/Fulfillment Status — implementado en Fase 8/9, vive en `TenantEcommerceDispatch.Status`

```
PENDIENTE_DESPACHO → DESPACHADO → EN_TRANSITO → ENTREGADO
```
Alternativo: `DEVUELTO` (definido en el enum, sin código que lo asigne todavía — ver Deuda técnica #8).

**Ciclo de vida INDEPENDIENTE del `Order.Status` (3.1)** — la relación exacta entre ambos, tal como quedó implementada:

| Transición de Dispatch | Efecto en `Order.Status` |
|---|---|
| (creación) → `DESPACHADO` (Fase 8) | `Order.Status` pasa `LISTO_PARA_DESPACHO → DESPACHADO` (los dos cambian juntos, en la misma transacción) |
| `DESPACHADO → EN_TRANSITO` (Fase 9) | **Ningún cambio** — `Order.Status` permanece `DESPACHADO` |
| `EN_TRANSITO → ENTREGADO` (Fase 9) | `Order.Status` pasa `DESPACHADO → ENTREGADO` (los dos cambian juntos, en la misma transacción, con `DeliveredAt`) |

`PENDIENTE_DESPACHO` queda definido en el enum para una evolución futura (ningún endpoint lo asigna hoy — el despacho siempre nace en `DESPACHADO`).

---

## 4. Pedido vs. Venta — REVISADO (punto 1 del usuario, decisión resuelta)

**Cambio respecto a v1:** se elimina por completo el acoplamiento que existía hoy en código real (`ConvertToSale` fuerza `status = "cerrado"`, [convert.go:219-226](../internal/ecommerce/service/convert.go)) y que v1 había dejado como "decisión pendiente". **Queda resuelto: no se fuerza ningún cambio de `Order Status` al convertir.**

| Momento | Qué existe | Dónde |
|---|---|---|
| Pedido existe | Desde `CreatePublicOrderAPI`, status `PENDIENTE` | `TenantEcommerceOrder` |
| Pedido avanza su propio ciclo | `CONFIRMADO → EN_PREPARACION → EMPAQUETADO → LISTO_PARA_DESPACHO → DESPACHADO → ENTREGADO`, gobernado únicamente por las transiciones de la sección 5 | Panel tenant, permisos granulares §7 |
| Pedido se convierte en venta | Manual, exige `target/series_id/branch_id`, **puede ocurrir en cualquier punto desde `CONFIRMADO` en adelante, sin importar en qué estado de preparación/despacho esté el pedido** | `internal/ecommerce/service/convert.go` — se modifica para **dejar de escribir `status: "cerrado"`** en el `Updates(...)` de la línea 220-224; solo escribe `converted_sale_id`/`converted_at` |
| Quién puede convertir | `ecommerce.orders_convert` (nuevo, sección 7) | — |
| Qué se copia | Sin cambios: nombre/teléfono/notas → `Notes` de la venta; ítems re-resueltos por producto vigente | — |
| Trazabilidad | `ConvertedSaleID`/`ConvertedAt` se mantienen exactamente igual — siguen siendo la única fuente de verdad de "este pedido ya se facturó", **independiente de si ya fue entregado o no** | — |

**Justificación del desacople:** un pedido puede facturarse antes de despachar (ej. el negocio prefiere emitir boleta al confirmar) o después de entregar (ej. el negocio solo emite comprobante cuando el cliente lo pide) — ambos son flujos reales y válidos que la auditoría no puede descartar sin una regla de negocio explícita, y esta revisión ya la recibió: pedido y venta son independientes. Un pedido puede llegar a `ENTREGADO` sin nunca convertirse a venta con comprobante fiscal formal (ej. si el negocio solo usa notas de venta informales) — eso ya era posible hoy (la conversión siempre fue opcional/manual) y sigue siéndolo.

---

## 5. Tabla de transiciones — REVISADA (permisos granulares, punto 3; flujo de despacho corregido, punto 8; DEVUELTO restringido, punto 4; separación Order/Dispatch corregida tras Fase 8/9)

**Nota de reconciliación:** las filas siguientes mezclaban, en el plan original, transiciones de `Order.Status` con transiciones de `Dispatch.Status` sin distinguir el dominio — la implementación real (Fase 8/9) los mantiene en tablas de historial SEPARADAS (`TenantEcommerceOrderStatusHistory` vs. `TenantEcommerceDispatchStatusHistory`, nunca la misma fila para los dos). La columna "Dominio" se agregó acá para dejarlo inequívoco.

| Dominio | Estado actual | Acción | Estado siguiente | Permiso requerido | Efecto colateral |
|---|---|---|---|---|---|
| Order | — | Cliente finaliza checkout | `PENDIENTE` | Público (sin auth) | Crea `TenantEcommerceOrder` + `TenantEcommerceOrderItem[]`; emite `TenantNotification` tipo `ecommerce.order.created` |
| Order | `PENDIENTE` | Staff confirma y asigna sucursal | `CONFIRMADO` | `ecommerce.orders_manage` | Setea `BranchID`; registra en `OrderStatusHistory`; emite `ecommerce.order.confirmed` |
| Order | `PENDIENTE`/`CONFIRMADO` | Staff rechaza | `RECHAZADO` | `ecommerce.orders_manage` | Requiere `Notes` con motivo; emite `ecommerce.order.cancelled` |
| Order | `CONFIRMADO` | Almacenero inicia picking | `EN_PREPARACION` | `ecommerce.orders_prepare` | — (sin persistencia por línea, ver 1.2) |
| Order | `EN_PREPARACION` | Almacenero termina de empacar | `EMPAQUETADO` | `ecommerce.orders_prepare` | — |
| Order | `EMPAQUETADO` | Staff marca listo para despacho | `LISTO_PARA_DESPACHO` | `ecommerce.orders_prepare` | El Almacenero **puede** ejecutar esta transición — es la última que le corresponde según su alcance (ver §7) |
| Order **+** Dispatch (creación) | `LISTO_PARA_DESPACHO` | `POST /orders/:id/dispatch` — crea `TenantEcommerceDispatch` (transportista/tracking/bultos, todos opcionales) | Order→`DESPACHADO`, Dispatch→`DESPACHADO` | `ecommerce.orders_dispatch` | Atómico (una transacción, `SELECT...FOR UPDATE`); registra `OrderStatusHistory`. **El Almacenero NO tiene este permiso.** Etiqueta: ver Fase 10 |
| Dispatch únicamente | `DESPACHADO` | `PUT /dispatches/:id/status {status:"EN_TRANSITO"}` | Dispatch→`EN_TRANSITO` | `ecommerce.orders_dispatch` | **`Order.Status` NO cambia** (permanece `DESPACHADO`); registra `DispatchStatusHistory` — NUNCA `OrderStatusHistory` |
| Order **+** Dispatch | `EN_TRANSITO` | `PUT /dispatches/:id/status {status:"ENTREGADO"}` | Order→`ENTREGADO`, Dispatch→`ENTREGADO` | `ecommerce.orders_dispatch` | Atómico; setea `Dispatch.DeliveredAt` (reloj del servidor); registra AMBOS historiales (`DispatchStatusHistory` y `OrderStatusHistory`) |
| Order | `ENTREGADO` | Reclamo/devolución | `DEVUELTO` | **`ecommerce.orders_return`** (exclusivo Administrador/Supervisor) | Registra en `OrderStatusHistory`; no crea todavía flujo de devolución en inventario/ventas (§16); **`Dispatch.Status` NO se sincroniza a `DEVUELTO`** — deuda técnica #8, decisión pendiente |
| Order | Cualquiera antes de `DESPACHADO` | Cancelación | `CANCELADO` | `ecommerce.orders_manage` | Requiere `Notes` con motivo; si ya se convirtió a venta, bloquear cancelación del pedido (sin cambios respecto a v1) |
| Order | Cualquier estado `>= CONFIRMADO`, salvo `CANCELADO`/`RECHAZADO` | Conversión a venta | Sin cambio de `Status` (ver §4) | `ecommerce.orders_convert` | Setea `ConvertedSaleID`/`ConvertedAt` únicamente; emite `ecommerce.order.converted` |

---

## 6. Contrato de API — permisos actualizados

### 6.1 Público — sin cambios respecto a v1

(catálogo, checkout, auth de cliente, cuenta — igual que v1, sección 6.1 original, sin permisos de tenant involucrados)

### 6.2 Panel tenant — permisos revisados (tabla reconciliada con las rutas REALES de `internal/ecommerce/routes.go`/`internal/notifications/routes.go` tras Fase 5-9)

| Method real | Path | Permiso | Notas |
|---|---|---|---|
| GET | `/api/ecommerce/orders` | `ecommerce.orders_view` | Filtros: `status`, `branch_id`, `q`, `date_from`, `date_to` (Fase 5) |
| GET | `/api/ecommerce/orders/:id` | `ecommerce.orders_view` | Incluye `items`, `history` y `dispatch` (Fase 5/8) |
| **PUT** | `/api/ecommerce/orders/:id/status` | Depende de la transición solicitada — el servicio valida contra la tabla §5 y exige el permiso correspondiente a esa transición específica (no un permiso único para "cambiar estado a lo que sea") | **Corregido: el método real es `PUT`, no `PATCH`.** El mismo endpoint puede ser llamado por distintos roles, pero cada transición exige su propio permiso, evaluado server-side. No existe (ni existió nunca) un endpoint `/orders/:id/branch` separado — la sucursal se asigna dentro del body de esta misma transición (`PENDIENTE→CONFIRMADO`) |
| POST | `/api/ecommerce/orders/:id/convert` | `ecommerce.orders_convert` | Ya no fuerza `status` (sección 4) |
| POST | `/api/ecommerce/orders/:id/dispatch` | `ecommerce.orders_dispatch` | Crea el `Dispatch` y pasa `Order`→`DESPACHADO` (Fase 8) |
| PATCH | `/api/ecommerce/dispatches/:id` | `ecommerce.orders_dispatch` | Solo metadata (carrier/tracking/bultos/peso/dimensiones/notas) — nunca `status` (Fase 8) |
| **PUT** | `/api/ecommerce/dispatches/:id/status` | `ecommerce.orders_dispatch` | **Nuevo en Fase 9** — único punto de entrada para `DESPACHADO→EN_TRANSITO→ENTREGADO`, separado a propósito de la metadata |
| GET | `/api/ecommerce/orders/:id/shipping-label` | `ecommerce.orders_dispatch` | Ver bitácora de Fase 10 para el endpoint/método real — no asumir que esta fila ya estaba implementada antes de esa fase |
| — | ~~CRUD `/api/ecommerce/carriers`~~ | — | **Nunca implementado, decisión revertida en Fase 8**: no existe catálogo de transportistas para ecommerce, `CarrierName`/`TrackingCode` son texto libre dentro de `TenantEcommerceDispatch` |
| GET/POST | `/api/notifications*` | Cualquier usuario autenticado (sin `RequireModule`) — el filtrado real de qué notificaciones ve cada uno pasa por el mapa `Type→permiso` de `internal/notifications/service`, no por un permiso a nivel de ruta | Implementado en Fase 6: `GET /api/notifications`, `GET /api/notifications/unread-count`, `POST /api/notifications/:id/read`, `POST /api/notifications/read-all`, `GET /api/notifications/events` (SSE) |

**Validación server-side explícita (refuerza el punto 3):** el backend nunca confía en que el frontend solo muestre el botón correcto — cada handler de transición valida el permiso específico de esa transición antes de ejecutar el `Update`, con el mismo mecanismo `RequirePermission`/`RequireAnyPermission` ya existente ([pkg/middleware/permissions.go](../pkg/middleware/permissions.go)), sin necesidad de nueva infraestructura de autorización.

---

## 7. RBAC — REVISADO por completo (punto 3 y 5 del usuario)

### 7.1 Por qué no se reutiliza `ecommerce.orders` tal cual

La auditoría original ya había confirmado que `ecommerce.orders` es un permiso **único e indiferenciado**: quien lo tiene puede ver, confirmar, convertir a venta y (con la UI nueva) despachar/cancelar/devolver — todo junto. Eso es exactamamente lo que el punto 3 del usuario prohíbe para el Almacenero. No hay forma de lograr la separación pedida sin nuevos permisos — no es una decisión de conveniencia, es un requisito imposible de cumplir con el catálogo actual.

### 7.2 Nuevos permisos (todos `module=ecommerce`, mismo formato `module.action` ya usado en el resto del sistema, ej. `inventory.confirm_document`)

| Permiso | Label | Cubre |
|---|---|---|
| `ecommerce.orders_view` | "Ver pedidos web" | Listar y ver detalle de pedidos (sin acción alguna) |
| `ecommerce.orders_prepare` | "Preparar pedidos web" | Transiciones `CONFIRMADO→EN_PREPARACION→EMPAQUETADO→LISTO_PARA_DESPACHO` |
| `ecommerce.orders_manage` | "Gestionar pedidos web" | Confirmar (`PENDIENTE→CONFIRMADO`), asignar/reasignar sucursal, rechazar, cancelar, editar información comercial (notas, datos de contacto del pedido) |
| `ecommerce.orders_convert` | "Convertir pedidos web a venta" | `POST /orders/:id/convert` |
| `ecommerce.orders_dispatch` | "Despachar pedidos web" | Crear despacho, actualizar tracking, marcar entregado |
| `ecommerce.orders_return` | "Registrar devoluciones de pedidos web" | Única transición autorizada hacia `DEVUELTO` |

Regla de implicancia genérica ya existente ([pkg/middleware/tenant_permissions.go:16-20](../pkg/middleware/tenant_permissions.go)): `ecommerce.manage` sigue implicando **todos** los permisos del módulo `ecommerce`, incluidos estos 6 nuevos — así Administrador no requiere asignación explícita de cada uno.

### 7.3 Asignación por rol (mínima granularidad que cumple exactamente lo pedido)

| Rol | Permisos asignados | Cubre | Explícitamente NO cubre |
|---|---|---|---|
| **Almacenero** | `orders_view`, `orders_prepare` | Ver pedidos, ver detalle, preparar, empaquetar, marcar listo para despacho | Convertir a venta, cancelar, devolver, modificar información comercial, despachar — **ninguno de estos permisos se le asigna** |
| **Vendedor** | `orders_view`, `orders_manage`, `orders_convert` | Consultar, confirmar, gestionar pedido (incl. cancelar antes de despacho), convertir a venta | Despachar, marcar devuelto |
| **Supervisor** | `orders_view`, `orders_manage`, `orders_prepare`, `orders_convert`, `orders_dispatch`, `orders_return` | Gestión completa: todo lo anterior + despacho + devolución | — (alcance completo dentro del módulo, salvo configuración de tienda que sigue bajo `ecommerce.manage`) |
| **Administrador** | `ecommerce.manage` (ya existente, implica todo) | Todo, incluida configuración de tienda/transportistas | — |
| **Cajero, Contador** | Ninguno de los nuevos (sin cambios) | — | Sin acceso a pedidos web, consistente con sus responsabilidades actuales |

Esto responde exactamente a la matriz que diste: Almacenero limitado a ver/preparar, Vendedor sin despacho/devolución, Supervisor con gestión completa, Administrador con todo — usando 6 permisos nuevos, no una explosión de granularidad mayor (se evaluó separar `orders_manage` en "confirmar" vs. "cancelar" vs. "editar", pero no hay ningún requisito tuyo que distinga esos tres para Vendedor, así que se mantienen juntos — la granularidad se detiene donde hay una diferencia de rol real que la exige).

### 7.4 Seed — REVISADO (punto 5 del usuario)

`internal/users/service/role_service.go:234-294` (`defaultRolePermissions`) se modifica así:

- **Almacenero** ([role_service.go:271-280](../internal/users/service/role_service.go)): agrega `ecommerce.orders_view`, `ecommerce.orders_prepare` únicamente. **No se agrega `ecommerce.orders_manage`, `_convert`, `_dispatch` ni `_return`.**
- **Vendedor** ([role_service.go:261-270](../internal/users/service/role_service.go)): agrega `ecommerce.orders_view`, `ecommerce.orders_manage`, `ecommerce.orders_convert`.
- **Supervisor**: agrega los 6 permisos completos.
- **Administrador**: sin cambio (ya tiene `ecommerce.manage`, que implica todo).
- **Cajero, Contador**: sin cambios, no reciben ningún permiso de `ecommerce.orders_*`.

Esto reemplaza directamente lo que v1 proponía como "agregar `ecommerce.orders` completo a Almacenero y Vendedor" — descartado explícitamente por el punto 5 del usuario.

---

## 8. Contrato frontend — sin cambios estructurales, ajuste de UI por permiso

Igual que v1 (sección 8), con un ajuste: cada botón de transición en `PedidosWebPage.tsx`/`PedidoWebDetailPage.tsx` se muestra u oculta según el permiso granular del usuario logueado (`ecommerce.orders_prepare` muestra "Iniciar preparación"/"Marcar empaquetado"/"Marcar listo"; `ecommerce.orders_dispatch` muestra "Despachar"; `ecommerce.orders_return` muestra "Registrar devolución"), reutilizando el mismo patrón `hasPermission()` que ya usa el resto del panel — sin componente nuevo de autorización.

Checkboxes de picking (punto 7): estado local de React (`useState` dentro de `PedidoWebDetailPage.tsx`), nunca enviado al backend, se reinicia si se recarga la página — documentado en el propio componente con un comentario corto para que no se confunda con persistencia real.

---

## 9. Flujo ecommerce público — sin cambios respecto a v1

(sección 9 de v1, sin cambios: catálogo → producto → carrito → checkout → pedido → confirmación → WhatsApp; login → cuenta → pedidos → tracking)

---

## 10. Flujo panel tenant — CORREGIDO (punto 8 del usuario)

**v1 tenía una inconsistencia real**: el diagrama de esta sección escribía "DESPACHAR" *antes* de "LISTO_PARA_DESPACHO", contradiciendo la tabla de transiciones (sección 5), que siempre tuvo el orden correcto. Corregido:

**Corregido de nuevo tras Fase 9** (el diagrama todavía combinaba "actualizar tracking" y "confirmar entrega" como si `DESPACHADO→ENTREGADO` fuera una sola transición directa del pedido — la implementación real inserta `EN_TRANSITO` como estado intermedio del **Dispatch**, no del pedido):

```
PEDIDOS (/sales/pedidos-web)
  → CONFIRMAR (asigna sucursal si falta)               [ecommerce.orders_manage]    → Order: CONFIRMADO
  → PREPARAR (Almacenero, vista de picking)             [ecommerce.orders_prepare]   → Order: EN_PREPARACION
  → EMPAQUETAR                                          [ecommerce.orders_prepare]   → Order: EMPAQUETADO
  → MARCAR LISTO PARA DESPACHO                          [ecommerce.orders_prepare]   → Order: LISTO_PARA_DESPACHO
  → DESPACHAR (crea TenantEcommerceDispatch)            [ecommerce.orders_dispatch]  → Order: DESPACHADO, Dispatch: DESPACHADO
  → MARCAR EN TRÁNSITO                                  [ecommerce.orders_dispatch]  → Order: sin cambio (sigue DESPACHADO), Dispatch: EN_TRANSITO
  → CONFIRMAR ENTREGA                                   [ecommerce.orders_dispatch]  → Order: ENTREGADO, Dispatch: ENTREGADO (+ DeliveredAt)
  → (excepcional) REGISTRAR DEVOLUCIÓN                  [ecommerce.orders_return]    → Order: DEVUELTO (Dispatch.Status NO se sincroniza — deuda técnica #8)
```

```
NOTIFICACIONES (Header)
  "Nuevo pedido online #125" → click navega a /sales/pedidos-web?id=125
  "Pedido #125 listo para despacho" → idem
```

---

## 11. Trazabilidad — sin cambios de estructura, aclaración sobre el punto 4 (§4 nueva)

Igual que v1 (sección 11), con la aclaración de que `ConvertedSaleID` ya no implica nada sobre el `Status` del pedido — pueden coexistir en cualquier combinación (ej. `Status=EN_PREPARACION` con `ConvertedSaleID` ya asignado, o `Status=ENTREGADO` sin conversión todavía). Las preguntas que el diseño debe responder no cambian: "¿de qué pedido salió esta venta?" y "¿dónde está este pedido?" siguen respondidas de la misma forma.

---

## 12. Marcador arquitectónico para reserva de stock futura — reafirmado sin cambios

Sin cambios respecto a v1 (sección 12). Reconfirmado explícitamente por el punto 9 de esta revisión: el campo `PaymentStatus` sigue siendo el único punto de enganche documentado para una futura reserva de stock cuando exista pasarela de pago — **no se implementa nada de eso ahora**. El fix de locking (`FOR UPDATE` en `RecordMovementTx`) se mantiene como recomendación independiente (Fase 1.5), no ligada a reserva de stock de ecommerce.

---

## 13. SEO — sin cambios respecto a v1

## 14. Performance — sin cambios respecto a v1

---

## 15. Fases de implementación — ajuste menor

Sin cambios de orden respecto a v1. Ajustes de contenido dentro de cada fase, reflejando esta revisión:

| Fase | Contenido | Ajuste de esta revisión |
|---|---|---|
| 0 | Contrato técnico | Esta v2 |
| 1 | Modelo de pedido online | `ConvertToSale` se modifica para **no** escribir `status` (sección 4) |
| 1.5 | Fix de locking de inventario | Sin cambios |
| 2 | Variantes en catálogo público | Confirmado: opción de nombre compuesto sobre `TenantProductPresentation`, sin migración de esquema nueva (sección 1.8) |
| 3 | Checkout + dirección + WhatsApp | Sin cambios |
| 4 | Cuenta del cliente | Sin cambios |
| 5 | Panel de pedidos evolucionado | Botones de transición ahora gateados por permiso granular (sección 7), no por un único `ecommerce.orders` |
| 6 | Notificaciones internas | Sin cambios |
| 7 | Picking/preparación | Explícitamente sin persistencia por línea (sección 1.2) — reduce el alcance de esta fase respecto a lo que v1 dejaba abierto |
| 8 | Empaquetado/despacho | Confirma el flujo corregido de la sección 10; requiere `ecommerce.orders_dispatch` |
| 9 | Tracking/transportistas | Sin cambios |
| 10 | Etiqueta/documentos | Sin cambios |
| 11 | Responsive/performance/SEO | Sin cambios |

---

## 16. Riesgos — actualizado

1. **Backfill de estados existentes** — sin cambios respecto a v1 (aproximación documentada, sin impacto en pedidos nuevos).
2. ~~Cambio de comportamiento en `ConvertToSale`~~ — **resuelto**, ya no es un riesgo, es una decisión aprobada (sección 4).
3. **Race condition de stock** (preexistente) — sin cambios, documentada en sección 12.
4. **Migración de `ItemsJSON`** — sin cambios respecto a v1.
5. **Autenticación de cliente final separada** — sin cambios respecto a v1.
6. **Nuevo — migración de permisos existentes**: tenants que ya tienen `ecommerce.orders` asignado a un rol personalizado (no solo a los 6 roles de sistema) reciben el backfill del superset (`view+manage+convert+dispatch`, sección 2) para no perder funcionalidad de un día para otro — esto significa que, **inmediatamente después de la migración**, un tenant que ya le había dado `ecommerce.orders` a su propio rol "Almacén" (creado a mano, fuera de los 6 roles de sistema) seguiría teniendo permiso de despachar/convertir hasta que el propio tenant ajuste manualmente los permisos de ese rol desde la pantalla de Roles. **No se puede inferir automáticamente la intención de cada tenant** — se documenta como limitación aceptada, no se fuerza una reducción de permisos sin que el tenant lo decida.
7. **Nuevo — UX de variantes con nombre compuesto**: para productos con muchas combinaciones (sección 1.8), la lista plana de presentaciones puede volverse larga. Riesgo de UX, no de arquitectura — mitigable con un buscador/filtro simple dentro del selector si se vuelve un problema real, sin necesidad de rediseñar el modelo de datos.

---

## 17. Decisiones pendientes — actualizado

**Resueltas en esta revisión** (ya no requieren tu input, se listan solo para trazabilidad del proceso):
- ~~¿`ConvertToSale` debe seguir forzando el order status?~~ → No, desacoplado (sección 4).
- ~~¿División de `ecommerce.orders` en sub-permisos?~~ → Sí, 6 permisos nuevos (sección 7).
- ~~¿Quién puede marcar `DEVUELTO`?~~ → Solo Administrador/Supervisor vía `ecommerce.orders_return` (sección 5, 7).
- ~~¿Seed de Almacenero/Vendedor con `ecommerce.orders` completo?~~ → No, seed granular (sección 7.4).
- ~~Nombres de estados~~ → Confirmados sin cambios (sección 3).
- ~~Picking por línea~~ → No implementado en esta fase (sección 1.2).

**Cerradas en la aprobación final (sin cambios pendientes):**
- Variantes: aprobado `TenantProductPresentation` con nombre compuesto, sin `ProductAttribute`/`ProductAttributeValue`/`ProductVariant`/`VariantCombination`/SKU nuevo. Cualquier necesidad futura de atributos combinables reales es un proyecto arquitectónico independiente, no una extensión de esta evolución.
- `PaymentStatus`: se crea únicamente como marcador arquitectónico, valor fijo `NO_APLICA` en la creación de todo pedido, no editable por el cliente público (el backend lo ignora si viene en el request), sin ninguna lógica de pago/reserva/HOLD/expiración asociada.
- `ecommerce.orders` legacy: se mantiene deprecado indefinidamente por ahora, sin retiro ni nuevas asignaciones; su retiro (si acaso) requiere una auditoría de uso real y una migración independiente, fuera de esta evolución.

No quedan decisiones abiertas. Comienza la implementación por la Fase 1.

---

## Bitácora de fases

### Fase 1 — Modelo de pedido online

Estado: **APROBADA**. Commit local `bce9625` (sin push). Entregado: migraciones v134-v138, `TenantEcommerceOrderItem`/`TenantEcommerceOrderStatusHistory`, máquina de estados con permiso por transición ([order_status.go](../internal/ecommerce/service/order_status.go)), `ConvertToSale` desacoplado del `Status` del pedido, RBAC granular (`ecommerce.orders_{view,prepare,manage,convert,dispatch,return}`). 14 tests nuevos, todos en verde; sin regresiones en el resto del backend. Reporte de cierre completo entregado en el chat de la sesión de implementación (no duplicado acá para no mantener dos copias de la misma información — este documento es el contrato de diseño, el reporte de fase es el registro de ejecución).

No implementado en esta fase (explícitamente fuera de alcance, ver §17 de v1 y el punto 7 de la aprobación): picking persistente por línea, `TenantEcommerceDispatch`/`TenantEcommerceCarrier` (Fase 8), endpoint `PATCH /orders/:id/branch` para reasignar sucursal fuera de la confirmación (Fase 5), cualquier UI de frontend.

### Fase 1.5 — Fix de locking de inventario (independiente del ecommerce)

Estado: **APROBADA**. Commit local `191403f` (sin push). `RecordMovementTx` ([internal/inventory/service/inventory_service.go](../internal/inventory/service/inventory_service.go)) ahora usa `SELECT ... FOR UPDATE` sobre la fila de stock antes de validar/actualizar el saldo — confirmado como race condition real (no teórica) contra MySQL 8 real antes del fix (30 salidas concurrentes contra un stock de 10 pasaban las 30) y corregido (10/30 tras el fix, stock final en 0). Test de concurrencia real (no simulado) en `internal/inventory/service/inventory_concurrency_test.go`, mismo patrón que `pkg/saas/docusage/concurrency_test.go` (DSN de MySQL opcional por variable de entorno, se salta si no está configurada — sqlite no puede reproducir esta race, serializa escrituras a nivel de archivo completo).

Hallazgo documentado, no corregido (fuera de alcance de este fix): `adjustmentInWithSerials`/`adjustmentOutWithSerials` (mismo archivo) tienen el mismo patrón de lectura-sin-lock, pero no tienen ningún caller en todo el repo — código muerto, confirmado por grep. Además, `TenantProductStock`/`TenantProductPresentationStock` no tienen índice único en (product_id, branch_id)/(presentation_id, branch_id) — dos transacciones concurrentes creando la PRIMERA fila de stock de un producto/sucursal (caso raro: solo pasa antes de que exista cualquier movimiento previo) podrían crear filas duplicadas. `FOR UPDATE` no protege contra esto (no hay fila que bloquear todavía). Es un problema distinto (duplicación en la creación inicial, no sobreventa en movimientos), de menor probabilidad real, y su fix correcto requeriría una migración de esquema (constraint único) — se deja documentado como hallazgo pendiente de decisión, no se implementa sin autorización explícita. **Deuda técnica separada, confirmado en la aprobación de Fase 1.5: no se agrega la migración de índice único hasta auditar todas las rutas que crean filas de stock y diseñar la estrategia de INSERT concurrente correspondiente.**

### Fase 2 — Variantes en catálogo público

Estado: **APROBADA**. Commits locales `11259bd` (backend, mistiq_backend) y `ce4e9cc` (frontend, mistiq_tenant), sin push. `ProductReportItem` gana `Presentations []PresentationOption` (id/name/sale_price/stock), poblado en `enrichReport` ([internal/products/service/product_service.go](../internal/products/service/product_service.go)) reutilizando exactamente `TenantProductPresentation`/`TenantProductPresentationStock` — mismas tablas que ya consumen POS e Inventario, sin infraestructura nueva. El catálogo público (`EcommerceService.PublicProducts`) expone esto solo para productos con variantes, respetando "Mostrar stock" (oculta el número, conserva la identidad de la presentación). En el frontend, `ProductDetailModal` agrega el selector de presentación (lista plana, sin cruces de atributos) y `storeCart` pasa a identificar líneas por `(product_id, presentation_id)` en vez de solo `product_id`, para que dos presentaciones del mismo producto no se fusionen. No se tocó `OrderItemInput`/`CreatePublicOrderAPI` (contrato de creación de pedidos), por instrucción explícita — la identidad de la presentación viaja embebida en el nombre de línea hacia WhatsApp/pedido mientras esa pieza no se construye en una fase futura.

Verificación: 8 tests nuevos de backend (Go, reales, en verde) cubriendo producto simple/una presentación/múltiples presentaciones/alcance por sucursal/"Mostrar stock"/aislamiento entre tenants. `tsc --noEmit` del frontend sin errores. **Limitación documentada, no se simuló**: no se hizo verificación end-to-end en navegador contra el backend real — el proceso Go que ya corría localmente en el puerto 3000 fue iniciado fuera de esta sesión (por el usuario) y no incluye este código (no hay hot-reload en Go), y reiniciar un proceso que no inicié yo sin preguntar no correspondía; verificar en navegador además requiere resolución de tenant por subdominio, no disponible con `localhost` plano. La verificación de este frontend se apoya en TypeScript + revisión manual del flujo exacto, no en una prueba en vivo.

### Fase 3 — Checkout + dirección + WhatsApp

Estado: **APROBADA**. Commits locales `55b78e6` (backend, mistiq_backend) y `dd084f1` (frontend, mistiq_tenant), sin push. `EcommerceService.CreateOrder` reescrito por completo: el cliente público solo puede enviar `product_id`/`presentation_id`/`quantity` por línea — nombre, precio, subtotal y total se resuelven SIEMPRE contra el catálogo real del tenant vigente en el momento de crear el pedido (nunca lo que el frontend tenía cacheado), dentro de una única transacción que no deja nada a medias si cualquier línea es inválida. `TenantEcommerceOrderItem.PresentationID` pasa de ser solo informativo a ser el dato estructurado real (FK verdadera a `TenantProductPresentation`, validada contra producto/tenant/estado activo). Se agregan `delivery_method` (obligatorio) y dirección (snapshot de invitado o FK autenticada, esta última soportada a nivel de servicio para cuando exista login de cliente en Fase 4). Se persiste la fila de creación en `TenantEcommerceOrderStatusHistory` y una `TenantNotification` (tabla nueva, migración v139 — solo la fila; el hub SSE/badge que la entrega en vivo al panel sigue siendo Fase 6). En el frontend, `StoreCartDrawer` pide método de entrega/dirección y arma el mensaje de WhatsApp con los ítems YA RESUELTOS que devuelve el backend después de persistir el pedido, nunca con datos del carrito del navegador.

Verificación: 20 tests nuevos de backend (17 a nivel de servicio + 3 a nivel de HTTP handler), todos reales y en verde — incluye una prueba de seguridad explícita que envía un body con `unit_price`/`subtotal`/`total` falsos y confirma que el pedido se persiste al precio REAL del catálogo, no al inventado por el cliente. `tsc --noEmit` del frontend sin errores; cadena `presentation_id` verificada por revisión estática de código (carrito → checkout → request), no por prueba en navegador — misma limitación de infraestructura ya documentada en Fase 2 (backend local desactualizado respecto al código de esta sesión, resolución de tenant por subdominio no disponible en `localhost`).

### Fase 4 — Cuenta del cliente

Estado: **APROBADA**. Commits locales `27e1dba` (backend, mistiq_backend) y `2b31b97` (frontend, mistiq_tenant), sin push. `TenantEcommerceCustomerAccount`/`TenantEcommerceCustomerAddress` (migración v140) implementan por primera vez lo que la Fase 1 solo había dejado como columnas preparadas — identidad de login del comprador final, completamente separada de `TenantUser` (staff/RBAC) y de `TenantContact` (facturación; sin autocompletar `ContactID` al registrarse, decisión deliberada para no hacer matching peligroso por teléfono).

Auth de cliente aislada de la de staff en **dos** capas: secreto de firma JWT distinto (`EcommerceCustomerJWTSecret`, mismo criterio que ya separaba `SAJWTSecret` de `JWTSecret`) — un token de cliente no solo falla el chequeo de `type`, la firma ni siquiera valida contra el secreto de staff — más bcrypt para password (mismo mecanismo que `TenantUser`). Verificado con tests cruzados reales: token de cliente contra el middleware de staff, y token de staff contra el de cliente, ambos rechazados.

Ownership real en toda operación de cliente (pedidos, direcciones): siempre se deriva del token, nunca de un ID enviado por el frontend — `GetCustomerOrder` exige `id + customer_account_id` en la misma query, el checkout resuelve `CustomerAccountID` del token (el body no tiene ese campo) y valida `DeliveryAddressID` contra ese mismo cliente antes de aceptarlo. Vinculación de pedido de invitado (`LinkGuestOrder`) más estricta que lo que proponía el contrato original: el teléfono de comparación se deriva SIEMPRE de la cuenta autenticada, nunca de un dato que el cliente pueda enviar — cierra la posibilidad de "probar" teléfonos ajenos.

Sin CRM (perfil de solo lectura, sin segmentación/fidelización) y sin password reset — auditado que no existe infraestructura reutilizable para ningún tipo de usuario del sistema (ni siquiera para staff), documentado como pendiente explícito en vez de improvisar un flujo inseguro.

Verificación: 26 tests nuevos de backend (18 servicio + 8 handler), todos reales y en verde, cubriendo el checklist de 26 puntos completo (registro/duplicados/aislamiento entre tenants, password nunca en texto plano, login sin diferenciar "no existe" de "password incorrecta", rechazo cruzado de tokens, ownership de pedidos/direcciones incluido cross-tenant, checkout autenticado, guest intacto, vinculación segura). Frontend: `tsc --noEmit` sin errores en todo el proyecto — misma limitación de verificación en navegador ya documentada en Fases 2/3 (sin backend local actualizado accesible por subdominio en esta sesión).

### Fase 5 — Panel interno de pedidos evolucionado

Estado: **APROBADA**. Commits locales `7bdd124` (backend, mistiq_backend) y `1799481` (frontend, mistiq_tenant), sin push. Backend: `GET /api/ecommerce/orders/:id` (nuevo — antes solo existía el listado bulk y el print-data) devuelve pedido + líneas normalizadas (con fallback a `ItemsJSON` solo para legacy) + historial real, nunca fabricado. `ListOrders` gana filtros combinables (sucursal, búsqueda por nombre/teléfono/N° de pedido, rango de fechas), mismo patrón incremental de `WHERE` que ya usa el resto del backend.

Frontend: `PedidosWebPage`/`PedidoWebDetailModal` (nuevo) dejan de tratar el pedido con el enum legacy de 4 estados y pasan al ciclo real — cierra la **Deuda 5** de abajo. Acciones de transición gateadas por `hasPermission()` reutilizando el patrón ya establecido en el resto del panel (`ORDER_TRANSITIONS` en `orderTransitions.ts` es un espejo EXACTO del backend, `internal/ecommerce/service/order_status.go`, limitado a las transiciones hasta `LISTO_PARA_DESPACHO` — despacho operativo real sigue siendo Fase 8). Picking: checkboxes efímeros de React, sin persistencia por línea. Convertir a venta reutiliza `ConvertOrderModal` sin cambios, mostrando "Venta generada #X" sin implicar cambio de estado del pedido (el desacople de Fase 3 se mantiene intacto).

**Bug real encontrado y corregido durante la auditoría de esta fase**: `Sidebar.tsx` y `AppRouter.tsx` seguían gateando el acceso a "Pedidos web" con el permiso legacy `ecommerce.orders` (deprecado desde la Fase 1, ya no se asigna a tenants nuevos) — Almacenero/Vendedor/Supervisor de cualquier tenant creado después de la Fase 1 jamás habrían podido ver el ítem del menú ni entrar a la ruta, pese a que el backend ya les daba acceso granular correcto. Corregido a `ecommerce.orders_view`. Se agregaron también las etiquetas de los 6 permisos granulares en `permissionLabels.ts` (la pantalla de Roles los mostraba como texto crudo).

Verificación: 6 tests nuevos de backend a nivel de servicio (filtros de listado, detalle con fallback e historial no fabricado) + 3 a nivel de HTTP handler, todos reales y en verde. Frontend: `tsc --noEmit` sin errores; checklist de 18 puntos de la fase verificado por revisión de código (gating por rol, transición sin cambiar status, motivo obligatorio, fallback de ItemsJSON) — sin infraestructura de tests automatizados en el repo, no se simuló ninguna prueba E2E.

### Fase 6 — Notificaciones internas

Estado: **APROBADA**. Commits locales `73fa7f7` (backend, mistiq_backend) y `9948d68` (frontend, mistiq_tenant), sin push.

**Auditoría previa (obligatoria antes de tocar código)**: `TenantNotification` ya existía desde Fase 3 (solo el INSERT en `CreateOrder`, sin API de lectura ni entrega en vivo — el propio comentario del struct ya prescribía esta fase). Se confirmó infraestructura SSE real y reutilizable en `pkg/billingevents` (hub in-memory + Redis pub/sub por tenant, handler con `retry:`/keepalive, middleware `?access_token=` ya genérico en `TenantAuthAPI`) — replicada como paquete independiente `pkg/notificationevents` (mismo patrón, canal Redis propio, nunca mezclado con billing). Se confirmó que `PedidosWebPage.tsx` nunca leía el `?id=` que `CreateOrder` ya escribía en `LinkPath` desde Fase 3 — el link quedaba "muerto"; corregido en esta fase (`useSearchParams`, abre `PedidoWebDetailModal` directo).

**Modelo de lectura (Opción A, confirmada explícitamente antes de implementar)**: `TenantNotification.ReadAt` sigue siendo el estado de lectura SOLO para notificaciones dirigidas (`UserID != nil`). Nueva tabla `TenantNotificationRead(notification_id, user_id, read_at)` con `UNIQUE(notification_id, user_id)` para el estado de lectura POR USUARIO de las broadcast (`UserID = nil`) — verificado con test real que dos usuarios divergen en el estado de lectura de la MISMA fila broadcast, y con MySQL real que la UNIQUE rechaza el duplicado. `ReadAt` de una fila broadcast nunca se toca.

**Migración v141** (`pkg/database/tenantmigrations/v141_notification_reads.go`): crea `tenant_notification_reads` + sus 3 índices, y ADEMÁS completa los índices de `tenant_notifications` que v139 nunca creó para tenants existentes (el struct local que usaba esa migración no llevaba tags gorm — `CreateTable` generó una tabla sin ningún índice real; solo tenants creados después del struct vivo con tags los tenían, vía AutoMigrate del baseline). Verificado contra MySQL 8.0.30 real: se simuló la tabla bare exacta que v139 dejó, se corrió la migración dos veces (idempotencia confirmada), se comprobó que los 3 índices de `tenant_notifications` y los 3 de `tenant_notification_reads` quedan creados, y que la UNIQUE rechaza un INSERT duplicado real.

**Eventos y permisos**: los 4 tipos pedidos, ni uno más — `ecommerce.order.created` (Fase 3, sin segundo INSERT), `ecommerce.order.confirmed` (solo `PENDIENTE→CONFIRMADO`), `ecommerce.order.cancelled` (cubre tanto `CANCELADO` como `RECHAZADO`, diferenciados por Title/Body, no por Type), `ecommerce.order.converted` (`ConvertToSale`). Ninguna otra transición (preparación/empaquetado/despacho) notifica. Mapa `Type → permiso` fijo en código (`internal/notifications/service`, fail-closed: un tipo sin entrada nunca es visible como broadcast) — los 4 apuntan a `ecommerce.orders_view`, sin crear permisos nuevos. `{módulo}.manage` sigue implicando el permiso (mismo criterio que el resto del RBAC).

**Transaccionalidad**: la notificación de confirmación/cancelación/rechazo se crea DENTRO de la misma transacción que `UpdateOrderStatus` ya abría para el cambio de estado + historial. La de conversión se creó envolviendo en una NUEVA transacción el `Update` de `converted_sale_id`/`converted_at` junto con el INSERT de la notificación (antes ese Update corría suelto, fuera de cualquier transacción) — verificado con test que ambos quedan atómicos entre sí. La señal SSE (`notificationevents.PublishChanged`) se dispara DESPUÉS del commit, nunca antes, para que un rollback no dispare un refresco de "algo cambió" que en realidad no pasó.

**SSE**: `pkg/notificationevents` (hub independiente de `pkg/billingevents`, mismo patrón). El payload del evento (`notification.changed`) NUNCA lleva contenido de negocio — solo señala "algo cambió"; el cliente siempre vuelve a pedir `/api/notifications/unread-count` (autenticado, filtrado por usuario) como fuente de verdad real, nunca confía en el evento en sí. Fallback sin polling agresivo: reconexión nativa de `EventSource` + un refetch puntual en `visibilitychange`, nunca un `setInterval` corriendo indefinido.

**API**: `GET /api/notifications` (paginación simple `limit`/`before_id`, mismo idioma que `ListOrders`), `GET /api/notifications/unread-count`, `POST /api/notifications/:id/read`, `POST /api/notifications/read-all`, `GET /api/notifications/events` (SSE). Sin `RequireModule`: el badge debe funcionar para cualquier staff autenticado sin importar qué módulos tenga el tenant, el filtrado real pasa por permisos. Nuevo módulo `internal/notifications` (service/handler/routes) — no existía ninguna infraestructura de lectura previa que reutilizar.

**Frontend**: `notifications.service.ts` + `useNotificationEvents.ts` (mismo patrón que `useBillingEvents.ts`, con una corrección: `enabled` atado a `isAuthenticated` para que la conexión se cierre también en el shell nativo, donde el logout no recarga la página — gap que `useBillingEvents.ts` sigue teniendo, deuda preexistente fuera de alcance). Cuarta sección "Pedidos web" en la campanita ya existente de `Header.tsx` (mismo patrón visual que las 3 secciones previas: plan/facturación/membresías), con botón "Marcar todas". Clic en una notificación marca leída (optimista + llamada real) y navega vía el `LinkPath` real del backend a `PedidosWebPage`, que ahora sí lo consume y abre `PedidoWebDetailModal` — la autorización real sigue viviendo en el endpoint del pedido, no en la notificación.

**Deuda 6 actualizada**: se mantiene sin cerrar (alcance de esta fase fue exclusivamente notificaciones, no una auditoría general del Sidebar).

Verificación: 11 tests nuevos de backend a nivel de servicio (`internal/notifications/service`: listado, aislamiento entre usuarios/tenants, lectura por usuario de broadcast, idempotencia, autorización, unread count, marcar todas) + 5 tests nuevos en `internal/ecommerce/service` (notificación por transición, transiciones que NO notifican, atomicidad de conversión), todos reales y en verde. Migración v141 verificada contra MySQL 8.0.30 real (idempotencia + índices + UNIQUE), no solo sqlite. `go build`/`go vet` limpios en todo el repo. Frontend: `tsc --noEmit` sin errores. **Limitación explícita**: no se realizó verificación E2E en navegador — el proceso backend local en :3000 no fue reiniciado esta sesión (no es un proceso que haya iniciado yo, reiniciarlo está fuera de alcance sin pedírtelo primero) y por lo tanto sigue sirviendo el binario sin las rutas/tablas de esta fase; misma limitación ya documentada en fases anteriores.

### Fase 7 — Preparación/picking operativo

Estado: **APROBADA**. Commits locales `830e509` (backend) y `6320fdd` (frontend), sin push.

**Auditoría previa**: `PedidoWebDetailModal.tsx` YA tenía desde Fase 5 el picking efímero (`Set<string>` en React, clave estable `product_id-presentation_id` para no repetir el bug de `item.id` compartido en legacy), las transiciones `CONFIRMADO→EN_PREPARACION→EMPAQUETADO→LISTO_PARA_DESPACHO` gateadas por `ecommerce.orders_prepare`, y el endpoint `PUT /api/ecommerce/orders/:id/status` ya soportaba todo el flujo desde Fase 1. **El backend no necesitó ningún cambio de producción para Fase 7** — la auditoría confirmó que el contrato ya era correcto; lo que faltaba era prueba explícita de ello (nunca se había testeado `UpdateOrderStatusAPI` a nivel de handler) y mejoras de UX en el frontend (progreso visual, advertencia antes de avanzar con líneas sin revisar, accesibilidad, cantidad más legible).

**Backend**: solo tests nuevos, cero cambios de producción. `order_status_test.go` gana un caso explícito de que ningún estado terminal/fuera de flujo (CANCELADO/RECHAZADO/DESPACHADO/ENTREGADO/DEVUELTO) puede "entrar" a una etapa de preparación. Nuevo `prepare_transition_test.go` (servicio): recorre el flujo completo CONFIRMADO→...→LISTO_PARA_DESPACHO confirmando que stock/kardex quedan intactos y que Fase 7 no agrega notificaciones nuevas (siguen siendo solo las 4 de Fase 6); aislamiento entre tenants con el mismo ID de pedido; conflicto secuencial (un pedido ya cancelado rechaza una transición que asumía un estado anterior, sin locking — explícitamente prohibido esta fase); pedido legacy con `ItemsJSON` sigue leyéndose en estado `EN_PREPARACION`, con cantidad decimal preservada. Nuevo `prepare_transition_handler_test.go` (handler, primera cobertura real de `UpdateOrderStatusAPI`): Almacenero (`orders_prepare` solo) puede preparar pero no cancelar; Vendedor (`orders_manage`+`orders_convert`, sin `orders_prepare`) no puede preparar; `ecommerce.manage` sigue implicando todo; saltar una etapa se rechaza con 422 aunque el usuario tenga todos los permisos; la transición registra `StatusHistory` real con el `UserID` del token.

**Frontend**: `PedidoWebDetailModal.tsx` — barra de progreso + contador "X de Y revisados" visible solo durante `EN_PREPARACION`; checkboxes más grandes (`w-5 h-5`) con `aria-label` describiendo producto y cantidad; fila completa clickeable (`<label>`) en vez de solo el checkbox; cantidad en tipografía grande/bold durante picking (antes era texto gris pequeño inline); advertencia de dos pasos antes de `EMPAQUETADO` si quedan líneas sin marcar ("Hay N producto(s) pendientes de revisión" + botón "Marcar empaquetado de todos modos"/"Revisar antes") — puramente de UI, nunca reemplaza la validación real del backend. `picked` y el estado de la advertencia se reinician explícitamente si cambia `orderId` (antes solo se reiniciaban implícitamente al desmontar/montar el modal). No se tocó `PresentationID`/`Name` — se sigue mostrando el snapshot ya resuelto por el backend, sin reconstruir presentación desde texto.

**No implementado, exactamente como se pidió**: ninguna columna nueva en `TenantEcommerceOrderItem`, ninguna tabla de picking, ningún endpoint nuevo, ningún tipo de notificación nuevo (`ecommerce.order.preparing`/`packed` NO se crearon), ningún movimiento de stock/kardex, ningún locking persistente del pedido, ninguna UI de `DESPACHADO`.

Verificación: 6 tests nuevos de servicio + 5 de handler, todos reales, en verde. `go build`/`go vet` limpios. Frontend: `tsc --noEmit` sin errores. **Limitación explícita, igual que fases anteriores**: no se realizó verificación E2E en navegador — el backend local en :3000 sigue sin reiniciarse esta sesión (no es un proceso iniciado por mí, reiniciarlo está fuera de alcance sin pedirlo primero); el frontend (Vite dev, puerto 5173) sí está corriendo y `tsc` confirma que compila, pero no se pudo ejercitar el flujo de picking contra datos reales de un pedido.

### Fase 8 — Empaquetado y despacho operativo

Estado: **APROBADA**. Commits locales `16ebb4b` (implementación) y `2b38de4` (corrección documental posterior) (backend), y `fd91a82` (frontend), sin push.

**Auditoría previa**: `TenantEcommerceDispatch`/`TenantEcommerceCarrier` NO existían en código — solo se mencionaban en este documento como plan. Sí existían otras entidades tipo "carrier" en el repo, pero de dominios distintos: `TenantGreCarrier` (transportistas para guía de remisión SUNAT, `internal/fleet`, gateado por el módulo "billing") y `TenantDeliveryCompany`/`TenantDeliveryDriver` (delivery de restaurante, `internal/restaurant`). Ninguna era reutilizable sin cruzar dominios que esta fase debía mantener separados.

**Decisión aprobada — Carrier como texto libre**: en vez de crear `TenantEcommerceCarrier` (catálogo + CRUD), `TenantEcommerceDispatch` tiene `CarrierName`/`TrackingCode` como texto libre. Alcance deliberado de Fase 8; un catálogo común reutilizable entre ecommerce/restaurante/GRE queda como posible decisión arquitectónica futura, no de esta fase.

**Modelo `TenantEcommerceDispatch`** (`pkg/database/migrations.go`): `ID, OrderID (uniqueIndex — relación 1:1 con el pedido), Status, CarrierName *string, TrackingCode *string, PackageCount *int, WeightKg/LengthCm/WidthCm/HeightCm *float64, DispatchedAt/DeliveredAt *time.Time, UserID *uint, Notes, CreatedAt, UpdatedAt`. Deliberadamente SIN `BranchID`/`DeliveryMethod`/`DeliveryAddressID` — `Order` sigue siendo la única fuente de verdad de esos datos, el despacho los consulta a través de la relación, nunca los copia. `Status` usa constantes GO SEPARADAS de `OrderStatus*` (`DispatchStatusPendiente/Despachado/EnTransito/Entregado/Devuelto` en `order_status.go`) aunque el valor literal "DESPACHADO" coincida en ambos dominios — nunca se reutiliza la misma constante para los dos ciclos de vida. `DispatchStatusPendiente` ("PENDIENTE_DESPACHO") queda definido para una evolución futura (Fase 9); el único endpoint de esta fase siempre crea el despacho ya en `DispatchStatusDespachado`, nunca en `PENDIENTE_DESPACHO` (decisión confirmada explícitamente, aunque el diagrama de estados del contrato lista `PENDIENTE_DESPACHO` primero).

**Migración v142**: crea `tenant_ecommerce_dispatches` + `UNIQUE(order_id)` + índice en `status`. Verificada contra MySQL 8.0.30 real (idempotencia + índices + UNIQUE rechazando duplicado).

**Corrección de compatibilidad de migración detectada durante la verificación de esta fase (NO es funcionalidad de Fase 8)**: al verificar `v142_ecommerce_dispatch.go` contra MySQL 8.0.30 real, se reprodujo el mismo patrón contra `v141_notification_reads.go` (`pkg/database/tenantmigrations/v141_notification_reads.go`) — se recreó `tenant_notifications` ejecutando la migración `V139TenantNotifications{}` real (no una aproximación con SQL escrito a mano), y `V141TenantNotificationReads{}.Up()` falló con `Error 1170 (42000): BLOB/TEXT column 'type' used in key specification without a key length` al intentar crear `idx_tenant_notifications_type`. Causa: el struct local que usa `V139TenantNotifications` (`v139Notification`) no lleva tags `gorm` de tamaño, así que MySQL tipa la columna `type` como `LONGTEXT` en vez de `VARCHAR` — un índice sobre una columna `TEXT`/`BLOB` requiere una longitud de prefijo explícita en MySQL. Corregido en `v141_notification_reads.go` cambiando la creación de ese índice a `type(60)` (prefijo de 60 caracteres) — verificado que funciona tanto contra la columna `LONGTEXT` real de v139 como contra una `VARCHAR(60)` (la que tendría un tenant nuevo vía el baseline/AutoMigrate, que sí usa el struct con tags), y que la migración sigue siendo idempotente en ambos casos.

**Sobre el estado de despliegue de v141**: no se afirma que la migración nunca se haya ejecutado en ningún entorno. La evidencia disponible en esta sesión es: (a) ningún `git push` se ha realizado en ningún momento de todo este trabajo (`git log`/`git status` de ambos repos, verificable); (b) en las bases de datos de tenant disponibles localmente en este entorno (`127.0.0.1:3306` — `apus_tenant_demo`, `saas_tenant_angel`, `saas_tenant_demomistiq`, `saas_tenant_it_test`, `saas_tenant_sermush`, `saas_tenant_tukifac`, `tenant_inversiones-santibell-sac`, `tenant_mi_escuelta`, `tenantcliente1`, `tenantcliente2`) ninguna tiene una tabla `tenant_notifications` (`SHOW TABLES LIKE 'tenant_notifications'` vacío en las 10). Esto no dice nada sobre un posible entorno de staging/producción fuera de esta máquina (el contrato menciona una IP de producción separada) — no tengo acceso a verificarlo, así que no se afirma nada sobre ese entorno. El bug se corrigió directamente en `v141_notification_reads.go` (sin migración de parche aparte) porque, dentro de lo que puedo verificar, no hay evidencia de que haya corrido contra una base de datos real.

**Endpoints**: `POST /api/ecommerce/orders/:id/dispatch` y `PATCH /api/ecommerce/dispatches/:id`, ambos con `ecommerce.orders_dispatch` fijo a nivel de ruta (a diferencia de `UpdateOrderStatusAPI`, que revalida el permiso específico adentro del handler porque un mismo endpoint sirve varias transiciones — acá el permiso no varía, así que basta el middleware de ruta). `GetOrderAPI`/`GetOrderDetail` (Fase 5) se extendieron para incluir `"dispatch": {...} | null` en la respuesta — sin `GET` nuevo.

**Transacción y concurrencia**: `EcommerceService.CreateDispatch` hace `SELECT ... FOR UPDATE` sobre el pedido (mismo patrón exacto que el fix de stock de Fase 1.5) dentro de una única transacción que también valida `Status == LISTO_PARA_DESPACHO`, verifica que no exista ya un despacho, crea el `Dispatch`, actualiza `Order.Status = DESPACHADO` y registra `StatusHistory`. El lock de fila serializa dos requests concurrentes sobre el MISMO pedido — verificado con un test real contra MySQL (20 goroutines simultáneas, exactamente 1 éxito y 19 rechazos limpios, nunca un error crudo de SQL, nunca 2 despachos). `UNIQUE(order_id)` queda como defensa adicional. `PATCH` nunca puede tocar `Status`/`OrderID`/fechas de auditoría (restricción aprobada) — solo metadata.

**Transportistas / recojo vs. envío**: `RECOJO_TIENDA` nunca exige carrier/tracking (verificado); `ENVIO_DOMICILIO` reutiliza `DeliveryAddressID`/snapshot de invitado ya persistidos, el despacho nunca vuelve a pedir la dirección ni puede modificarla.

**Notificaciones**: sin cambios — se confirmó explícitamente NO agregar `ecommerce.order.dispatched`, siguen siendo exactamente los 4 tipos de Fase 6.

**Confirmaciones explícitas de alcance (verificadas con tests reales, no solo por inspección)**:
- **Stock/kardex**: crear un despacho NO modifica `TenantProductStock` ni registra `TenantStockMovement` — `TestCreateDispatch_NoTocaStockNiKardex`.
- **Reserva/HOLD**: no se implementó ningún concepto de reserva o HOLD de stock — no existe esa infraestructura en el modelo, y el despacho no la introduce.
- **GRE**: no se conectó con `TenantGreCarrier` ni con ninguna guía de remisión SUNAT — queda para una fase específica de documentos, no esta.
- **Etiquetas**: no se implementó ningún PDF logístico, shipping label ni documento de despacho imprimible.
- **Idempotencia**: un segundo intento de despacho sobre el mismo pedido se rechaza limpio (nunca crea un segundo `Dispatch`, nunca un error crudo de SQL) — verificado a nivel de servicio (`TestCreateDispatch_DobleDespacho_RechazadoLimpio`) y a nivel HTTP con un doble click real (`TestCreateDispatchAPI_DobleClick_NuncaRetorna500`, segundo POST responde 400, nunca 500).

Verificación: 15 tests nuevos de servicio (creación exitosa, rechazo desde los 9 estados incompatibles, pedido inexistente, tenant isolation, doble despacho, recojo sin carrier, envío conserva dirección, tracking/peso decimal/bultos, valores negativos rechazados, no toca stock/kardex, conversión a venta independiente, actualización de metadata) + 9 de handler (RBAC de los 4 roles, contrato de respuesta, transición inválida, PATCH solo metadata, doble click sin 500) + 1 test de concurrencia real contra MySQL (20 requests simultáneos sobre el mismo pedido → 1 éxito + 19 rechazos limpios, 1 `Dispatch`, 1 `StatusHistory` — aceptado explícitamente como evidencia de la protección de concurrencia), todos en verde. `go build`/`go vet` limpios en todo el repo (excepto las 4 fallas preexistentes de Greenter XML en billing/prepayment, confirmadas no relacionadas, ver Fase 6/7). Frontend: `tsc --noEmit` sin errores.

**Pendiente para Fase 9**: tracking público, integración con APIs externas de transportistas, webhooks de carriers, tracking automático, ETA, avance de `Dispatch.Status` más allá de `DESPACHADO` (`EN_TRANSITO`/`ENTREGADO`/`DEVUELTO`), y la transición operativa `DESPACHADO→ENTREGADO` del pedido.

**Pendiente para Fase 10**: shipping labels (✅ implementado — ver bitácora de Fase 10). GRE/documentos SUNAT del despacho: decisión de negocio tomada en Fase 10 — sin integración, se emite aparte (ver bitácora de Fase 10).

### Fase 9 — Tracking / transportista / estado de despacho

Estado: **APROBADA**. Commits locales `e37bb8e` (implementación) y `9e7b14c` (tests y documentación) (backend), y `996bde7` (frontend), sin push.

**Auditoría previa**: confirmado que no existía ninguna infraestructura de tracking/carrier/EN_TRANSITO/webhook más allá de lo que dejó Fase 8. `order_status.go` ya tenía desde Fase 1 las filas de transición `Order` `{DESPACHADO, DESPACHADO, orders_dispatch}` (comentario histórico: "actualizar tracking, sin cambio de estado") y `{DESPACHADO, ENTREGADO, orders_dispatch}`, pero nunca conectadas a ningún endpoint real — Fase 9 implementa el ciclo de vida del **Dispatch** (separado del Order) en su lugar, siguiendo la autorización explícita del usuario, que prevalece sobre ese comentario histórico.

**Decisión arquitectónica pre-aprobada — `TenantEcommerceDispatchStatusHistory` (v143)**: tabla nueva, mismo shape/patrón exacto que `TenantEcommerceOrderStatusHistory` (`ID, DispatchID, FromStatus, ToStatus, UserID, Notes, CreatedAt`), separada a propósito: la transición `DESPACHADO→EN_TRANSITO` cambia SOLO `Dispatch.Status` — `Order.Status` se queda en `DESPACHADO` — así que una fila con `ToStatus="EN_TRANSITO"` en el historial del PEDIDO sería incorrecta (ese valor ni siquiera es un `EcommerceOrderStatus` válido). Verificada contra MySQL 8.0.30 real (idempotencia + índice `dispatch_id` + tipo de columna `VARCHAR`, aplicando la lección de v139/v141/v142: el struct local de la migración SÍ lleva tags de tamaño desde el principio).

**Estados de Dispatch implementados**: `DESPACHADO→EN_TRANSITO→ENTREGADO`, con `DEVUELTO` y `PENDIENTE_DESPACHO` definidos en el enum pero sin ningún código que los asigne en esta fase (`PENDIENTE_DESPACHO` es de Fase 8; `DEVUELTO` del Dispatch queda como gap documentado, ver abajo).

**`EcommerceService.MarkDispatchInTransit`** (`DESPACHADO→EN_TRANSITO`, Dispatch): transacción con `SELECT...FOR UPDATE` sobre el Dispatch (mismo patrón que Fase 1.5/8), valida `Status==DESPACHADO`, actualiza SOLO `Dispatch.Status`, escribe `TenantEcommerceDispatchStatusHistory` — `Order.Status` nunca se toca (confirmado con test que además verifica que NO se escribe ninguna fila con `to_status=EN_TRANSITO` en el historial del pedido).

**`EcommerceService.MarkDispatchDelivered`** (`EN_TRANSITO→ENTREGADO`, Dispatch **y** `DESPACHADO→ENTREGADO`, Order): una sola transacción, bloquea Dispatch y Order (`FOR UPDATE` en ambos), valida los dos estados actuales, fija `DeliveredAt` con el reloj del servidor (estructuralmente imposible que el cliente lo envíe — ni `CreateDispatchInput` ni `UpdateDispatchInput` tienen ese campo), y escribe AMBOS historiales (Dispatch y Order) antes de confirmar. Verificado que nunca queda `Dispatch=ENTREGADO` con `Order=DESPACHADO` ni viceversa.

**Endpoint**: `PUT /api/ecommerce/dispatches/:id/status`, body `{status, notes}` — solo `EN_TRANSITO`/`ENTREGADO` son destinos válidos (cualquier otro valor, 422, sin tocar el servicio). Separado a propósito de `PATCH /dispatches/:id` (Fase 8, metadata): ese endpoint nunca tuvo ni tendrá un campo `status` en su body — verificado con test que confirma que enviarlo igual no tiene efecto. Mismo permiso fijo `ecommerce.orders_dispatch` de Fase 8, verificado con test real para los 4 roles (Almacenero/Vendedor rechazados, Supervisor/`ecommerce.manage` aceptados).

**Concurrencia — verificado con MySQL real** (mismo patrón exacto de Fase 1.5/8): 20 requests simultáneos `DESPACHADO→EN_TRANSITO` sobre el MISMO despacho → 1 éxito + 19 rechazos limpios, 1 sola fila de historial. 20 requests simultáneos `EN_TRANSITO→ENTREGADO` → 1 éxito + 19 rechazos limpios, 1 `DeliveredAt`, 1 historial de Dispatch, 1 historial de Order. El `SELECT...FOR UPDATE` es la protección primaria; nunca se devuelve un error crudo de SQL, siempre un rechazo de negocio limpio (verificado también con doble click real vía HTTP).

**Idempotencia**: doble click sobre la misma transición → segundo rechazado limpio (400, nunca 500), sin duplicar historial. Retry después de un `ENTREGADO` exitoso → rechazado, `DeliveredAt` nunca se sobrescribe (verificado comparando el timestamp exacto antes/después del retry).

**Cliente — `CustomerDispatchView`**: `GetCustomerOrder`/`GetCustomerOrderAPI` extendidos con `"dispatch"` (nil si el pedido no tiene despacho) — subconjunto explícitamente reducido de `TenantEcommerceDispatch`: SOLO `status`, `carrier_name`, `tracking_code`, `dispatched_at`, `delivered_at`. Sin `id`, `order_id`, `user_id`, `notes`, bultos/peso/dimensiones — verificado con test end-to-end (HTTP real) que decodifica la respuesta JSON y confirma explícitamente que esos campos internos NUNCA aparecen. Ownership: `customer_account_id` sigue derivándose exclusivamente del JWT (Fase 4), nunca del body/query — un cliente que intenta ver el pedido de otro (con su propio token real, no simulado) recibe el mismo 404 genérico ya establecido en Fase 4.

**Tracking público**: NO se implementó — se mantiene la decisión de Fase 8/9 de que el único acceso al tracking es vía `CustomerAccount` autenticado, nunca un endpoint público por ID secuencial.

**Transportista/tracking_code**: siguen siendo texto libre (decisión de Fase 8, reconfirmada). No se generan URLs a partir del nombre del transportista — el frontend muestra "Transportista"/"Tracking" como texto plano, nunca un link inferido.

**Notificaciones**: NO se agregó `ecommerce.order.dispatched`/`in_transit`/`delivered` — siguen siendo exactamente los 4 tipos de Fase 6.

**Stock/Venta**: verificado con test real que todo el flujo `EN_TRANSITO`/`ENTREGADO` no toca `TenantProductStock` ni `TenantStockMovement`, y que `ConvertedSaleID`/`ConvertToSale` siguen completamente independientes de las transiciones logísticas.

**Frontend interno**: `PedidoWebDetailModal.tsx` — la sección "Despacho" (Fase 8) ahora muestra el estado real del Dispatch con badge propio (`DISPATCH_STATUS_LABEL`/`BADGE`, dominio separado de `ORDER_STATUS_LABEL`), botón "Marcar en tránsito" (visible solo si `Dispatch.Status===DESPACHADO` y el usuario tiene `orders_dispatch`) y "Confirmar entrega" (solo si `EN_TRANSITO`), además de la fecha de entrega cuando existe. El backend sigue siendo la única autoridad — los botones solo se ocultan, nunca reemplazan la validación real.

**Frontend cliente**: `AccountOrderDetailPage.tsx` — barra de progreso simple (Despachado → En tránsito → Entregado) que solo aparece si el pedido ya tiene despacho, con los pasos futuros atenuados (nunca se inventa progreso no confirmado por el backend); muestra transportista/tracking/fechas solo si existen.

**Gap documentado a propósito — Dispatch.Status → DEVUELTO**: el contrato aprobado (§6 de la autorización) pidió NO inventar una decisión de negocio para `DEVUELTO`. La transición `Order` `ENTREGADO→DEVUELTO` (`ecommerce.orders_return`) ya existía en `order_status.go` desde Fase 1 y sigue funcionando sin ningún cambio (verificado con test — es la primera vez que es alcanzable en la práctica, porque antes nada llegaba a `ENTREGADO`). Lo que NO se implementó: sincronizar `Dispatch.Status` a `DEVUELTO` cuando el pedido se marca devuelto — hacerlo automáticamente (como efecto colateral de `UpdateOrderStatus`) o como una acción separada es una decisión de acoplamiento entre los dos ciclos de vida que el contrato no resuelve. Queda pendiente de una decisión explícita antes de implementarse.

Verificación: 12 tests nuevos de servicio (transiciones válidas/inválidas, historial de ambos dominios, atomicidad, idempotencia, tenant isolation, stock intacto, venta independiente, DEVUELTO preexistente) + 2 tests reales de concurrencia contra MySQL + 10 tests de handler (RBAC, contrato HTTP, transición inválida, doble click, separación PATCH/status) + 2 tests de privacidad/ownership del cliente (HTTP real con JWT real) + **1 test E2E integral** (`TestE2E_FlujoCompletoDespachoYTracking`, HTTP real staff+customer, recorre los 17 puntos exactos pedidos en la autorización), todos en verde. `go build`/`go vet` limpios. `go test ./...`: las 4 fallas preexistentes de Greenter XML (billing/prepayment) se reproducen igual sin mis cambios (confirmado); una prueba de concurrencia de `internal/superadmin` (paquete no tocado en ninguna fase de ecommerce) falló UNA vez bajo la carga del `go test ./...` completo y pasó de forma consistente en 2 corridas posteriores (standalone y dentro del suite completo) — flakiness pre-existente bajo carga, no una regresión. Frontend: `tsc --noEmit` limpio, `npm run build` (producción) exitoso.

**Limitación explícita, igual que fases anteriores**: no se ejecutó E2E de navegador real — el backend local en :3000 sigue sin reiniciarse esta sesión (no es un proceso iniciado por mí; reiniciarlo está fuera de alcance sin pedirlo primero, regla explícita de esta fase). En su lugar se construyó un test de integración HTTP real (arriba) que ejercita el contrato completo de rutas/permisos/bodies JSON de principio a fin contra un backend con todo el código de Fases 1-9, con ambos roles (staff autorizado y cliente dueño del pedido) actuando sobre el mismo pedido real — la verificación más rigurosa posible sin un navegador.

### Fase 10 — Etiqueta logística. GRE: sin integración (decisión de negocio)

Estado: **APROBADA**. Commits locales `60af23c` (implementación), `f31052f` (corrección documental posterior) y `72b50ed` (reconciliación previa) (backend), y `4184353` (frontend), sin push.

**Auditoría previa**: confirmado que no existe infraestructura de generación de PDF en el backend — todos los documentos (`salessvc.PrintData`, reutilizado también por `BuildPrintDataForOrder` del pedido web) se devuelven como JSON vía `GET .../print-data`, y el PDF se genera 100% client-side con `jsPDF` (`mistiq_tenant/src/utils/receiptPdf.ts`/`receiptPdfA4.ts`). Confirmado que el GRE (guía de remisión electrónica SUNAT) es un sistema completo y separado: `TenantDespatch` + `internal/billing/service/despatch_payload.go` + `CreateAndSendDespatch`, que SIEMPRE crea una `TenantSale` sintética internamente para llevar la guía por el mismo pipeline fiscal que facturas/boletas, exige `branch_id`/`series_id`/destinatario (RUC+razón social+dirección+ubigeo)/motivo de traslado/modalidad/fechas/peso/bultos/partida-llegada, y trata transportista/vehículo/conductor como **texto libre en el request** — nunca los valida contra `TenantGreCarrier`/`TenantGreDriver`/`TenantGreVehicle` (esas tablas solo existen como catálogo admin de prellenado en `internal/fleet`, gateado por el módulo "billing", sin ninguna FK real hacia la creación de GRE). No existía ninguna cadena "shipping-label"/"shipping_label" en todo el repo — greenfield confirmado.

**Etiqueta logística — IMPLEMENTADA.** Deliberadamente separada de `salessvc.PrintData`/`PrintDespatch` (que sí existen y ya soportan datos de GRE, pero son documento tributario — mezclarlos habría violado la distinción exigida en la autorización de esta fase): nuevo struct `EcommerceLabelData` (`internal/ecommerce/service/label_data.go`) con solo lo que existe realmente en `TenantEcommerceOrder`+`TenantEcommerceDispatch` — número de pedido, cliente, teléfono, dirección real (resuelta desde el snapshot de invitado o desde `TenantEcommerceCustomerAddress` si es un cliente autenticado — el panel hasta ahora solo mostraba un placeholder "dirección guardada del cliente" sin el texto real), transportista, tracking, bultos, peso, sucursal, fecha de despacho. Sin motivo de traslado, modalidad, QR SUNAT ni ningún campo fiscal.

**Endpoint**: `GET /api/ecommerce/orders/:id/label` — se determinó `GET` (no `POST`) porque es exactamente el mismo patrón de `print-data`: solo lectura, nunca muta estado, completamente regenerable. Exige `ecommerce.orders_dispatch`, exige que el pedido YA tenga un `Dispatch` (404 si no), nunca modifica `Order`/`Dispatch`/stock/venta — verificado con test real que reimprimir 3 veces no crea un segundo `Dispatch` ni cambia `Order.Status` ni toca stock.

**Formato físico — decisión de diseño documentada, no un bloqueo**: no existía en el código ningún patrón de "etiqueta de envío" (solo `'a4'` página completa y `'ticket'` rollo angosto de recibo POS, ninguno equivalente a una etiqueta de despacho). Se adoptó **100mm × 150mm** (tamaño estándar de etiqueta térmica de envío) como el formato más simple y reconocible — nuevo archivo `mistiq_tenant/src/utils/shippingLabelPdf.ts`, reutiliza la librería `jsPDF` y los helpers genéricos `downloadBlob`/`openPdfViewer` ya existentes, pero con su propio layout (nunca el de comprobantes). Si el formato real de impresora del negocio es distinto, es un ajuste de una constante (`LABEL_WIDTH_MM`/`LABEL_HEIGHT_MM`), no un rediseño.

**Frontend**: botón "Generar etiqueta" en la sección Despacho de `PedidoWebDetailModal.tsx`, visible en cualquier estado del despacho (no solo `DESPACHADO`), gateado por `hasPermission('ecommerce.orders_dispatch')` — mismo permiso que el backend exige.

**GRE — NO IMPLEMENTADA. Decisión de negocio tomada: sin integración.**

- *Situación actual*: el despacho ecommerce (`TenantEcommerceDispatch`) es completamente independiente de Venta (`TenantSale`) y de GRE (`TenantDespatch`) — ningún código los conecta.
- *Infraestructura existente*: `CreateAndSendDespatch` (billing) exige datos que un pedido web de invitado normalmente NO tiene (RUC/DNI + ubigeo formal del destinatario — el modelo de `TenantEcommerceOrder` solo pide nombre/teléfono), y siempre crea una `TenantSale` sintética propia — no hay forma de emitir una GRE "suelta" sin ese efecto colateral.
- *Opciones evaluadas*: (1) automática al despachar — descartada, riesgo real de compliance (SUNAT podría rechazar una guía con datos inventados/incompletos, y es un documento tributario legal); (2) manual/asistida — un botón en el panel de pedidos que abriera el flujo de Billing prellenado; (3) sin integración — quien necesite una GRE para un envío ecommerce la crea manualmente desde Billing → Guías, sin ningún puente con el panel de pedidos.
- **Decisión del negocio (confirmada explícitamente por el usuario)**: **Opción 3 — sin integración.** Las guías de remisión se emiten aparte, desde el flujo de Billing ya existente, completamente desacopladas del panel de pedidos ecommerce. No es una decisión pendiente ni un bloqueo — es el alcance definitivo de Fase 10 en esta área.
- *Consecuencia técnica*: no se construye ningún puente, botón, prellenado ni endpoint entre `TenantEcommerceDispatch`/`TenantEcommerceOrder` y `TenantDespatch`/`CreateAndSendDespatch`. Si en el futuro el negocio decide lo contrario, es una decisión nueva y explícita, no una continuación implícita de esta fase.

**No implementado, exactamente como se pidió**: TMS/WMS, catálogo de transportistas, GPS/ETA/rutas, webhooks/integraciones externas de carriers, PDF/documento generado en el backend, stock/kardex tocado, `ConvertedSaleID`/conversión automática a venta.

Verificación: 6 tests nuevos de servicio (generación correcta con datos reales de Order+Dispatch, rechazo sin despacho, rechazo de pedido inexistente, resolución real de dirección de envío, tenant isolation, reimpresión sin efectos secundarios) + 3 de handler (RBAC, contrato de reimpresión) + **1 test E2E integral** (`TestE2E_EtiquetaLogistica`: pedido→LISTO_PARA_DESPACHO→DESPACHADO→generar etiqueta→verificar documento→reimprimir→sin segundo Dispatch→sin cambio de Order.Status→sin cambio de stock/kardex), todos en verde. `go build`/`go vet` limpios en todo el repo; `go test ./...` reproduce las mismas 4 fallas preexistentes de Greenter XML (billing/prepayment), confirmadas no relacionadas, sin fallas nuevas. Frontend: `tsc --noEmit` limpio, `npm run build` (producción) exitoso.

**Limitación explícita, igual que fases anteriores**: no se ejecutó E2E de navegador real (mismo motivo — el backend local en :3000 no fue reiniciado esta sesión). El E2E de la etiqueta se hizo vía HTTP real (arriba), la verificación más rigurosa disponible sin navegador.

## Deuda técnica registrada (NO implementar sin autorización explícita)

Puntos identificados durante la auditoría de fases anteriores — registrar acá para que ninguno se pierda entre fases:

1. **Revocación server-side de sesiones de cliente ecommerce** (Fase 4): JWT de 30 días, logout solo client-side, sin `TokenVersion`/columna de sesión. Evaluar más adelante: revocación real, invalidación de sesiones, estrategia de logout server-side, sesiones persistentes/revocables.
2. **Password reset de cliente ecommerce** (Fase 4): no existe infraestructura de recuperación de contraseña para NINGÚN tipo de usuario del sistema (ni siquiera staff) — confirmado por auditoría. Antes de implementar: diseñar token de recuperación, expiración, un solo uso, invalidación, protección contra enumeración, almacenamiento seguro.
3. **Índices UNIQUE de stock** (Fase 1.5): `TenantProductStock(product_id, branch_id)` y `TenantProductPresentationStock(presentation_id, branch_id)` sin constraint UNIQUE — condición de carrera potencial al crear la PRIMERA fila de stock de un producto/sucursal (`FOR UPDATE` no protege esto, no hay fila que bloquear todavía). Requiere auditoría específica de todas las rutas que crean stock + estrategia de migración e INSERT concurrente antes de tocarlo.
4. **`adjustmentInWithSerials`/`adjustmentOutWithSerials`** (Fase 1.5, `internal/inventory/service/inventory_service.go`): mismo patrón sin locking que `RecordMovementTx` tenía antes del fix, pero sin ningún caller en todo el repo — código muerto, mantener documentado, no modificar mientras siga sin uso.
5. ~~**Tipos legacy de `EcommerceOrder.status` en frontend**~~ — **CERRADA en Fase 5**: `EcommerceOrderStatus` ahora refleja el ciclo de vida real (`PENDIENTE...DEVUELTO`), `PedidosWebPage`/`PedidoWebDetailModal` migrados.
6. **Gate de permisos legacy en Sidebar/rutas del panel** (encontrado y corregido en Fase 5 solo para "Pedidos web"): si en fases futuras aparecen otros menús/rutas que todavía referencien permisos deprecados de otros módulos (no confirmado, no auditado fuera de ecommerce), revisar caso por caso — no se hizo una auditoría general de TODO el Sidebar en esta fase, solo del ítem tocado.
7. **`useBillingEvents.ts` no cierra el SSE en logout de shell nativo** (encontrado en Fase 6, fuera de su alcance): `useNotificationEvents.ts` sí lo corrige (atado a `isAuthenticated`), pero el hook de billing original sigue con el gap.
8. **`Dispatch.Status` nunca se sincroniza con `DEVUELTO`** (Fase 9, gap documentado a propósito — ver bitácora de Fase 9): la transición `Order` `ENTREGADO→DEVUELTO` (Fase 1) sigue funcionando, pero nada actualiza `Dispatch.Status` cuando eso ocurre — el despacho queda "ENTREGADO" para siempre aunque el pedido pase a "DEVUELTO". Requiere una decisión explícita: ¿se sincroniza automáticamente como efecto colateral de `UpdateOrderStatus`, o es una acción separada del lado del despacho?
9. ~~**GRE no integrada con despacho ecommerce**~~ — **CERRADA en Fase 10, no es deuda**: decisión de negocio explícita de mantener GRE sin integración; las guías de remisión se siguen emitiendo aparte, desde Billing → Guías. `TenantEcommerceDispatch` no tiene ni tendrá puente con `TenantDespatch`/`CreateAndSendDespatch` mientras esa decisión no cambie explícitamente.
