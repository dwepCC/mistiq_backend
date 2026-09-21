# Contrato técnico — Evolución del Ecommerce Mistiq

Estado: **APROBADO — Fase 1, Fase 1.5, Fase 2 y Fase 3 completadas, pendiente de tu revisión antes de Fase 4**. Aprobación general recibida sobre la v2, cerrando las decisiones pendientes: variantes vía `TenantProductPresentation` con nombre compuesto (sin nueva infraestructura), `PaymentStatus` como marcador inerte no editable por el cliente público, y `ecommerce.orders` como permiso legacy deprecado (sin retirar, sin nuevas asignaciones, sin inferencia automática sobre roles personalizados). Ver bitácora de fases al final del documento para el estado real de avance.

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

### 1.3 – 1.7 Sin cambios respecto a v1

`TenantEcommerceOrderStatusHistory`, `TenantEcommerceCustomerAccount` (+ regla de vinculación segura 1.4.1/1.4.2), `TenantEcommerceCustomerAddress`, dominio de despacho (`TenantEcommerceDispatch`/`TenantEcommerceCarrier`), `TenantNotification` — se mantienen exactamente como en v1, sin cambios derivados de esta revisión.

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

Sin cambios respecto a v1, con dos ajustes:

- Se elimina cualquier migración de datos o lógica que dependiera de "cerrar" el pedido al convertir (ya no aplica, sección 4).
- La migración de permisos (`vNNN_ecommerce_permission_seed.go`) se reemplaza por el diseño de la sección 7 (permisos granulares + backfill de `ecommerce.orders` existente).

| Migración | Contenido |
|---|---|
| `vNNN_ecommerce_order_fields.go` | ALTER `tenant_ecommerce_orders`: agregar `branch_id`, `customer_account_id`, `contact_id`, `delivery_address_id`, `delivery_method`, `payment_status`, `subtotal`, `guest_address_line`, `guest_reference`, `guest_ubigeo`. Sin cambios respecto a v1 |
| `vNNN_ecommerce_order_status_expand.go` | Ampliar validación de `status` al nuevo enum (sección 3), con backfill documentado en 1.1 de v1 |
| `vNNN_ecommerce_order_items.go` | CREATE `tenant_ecommerce_order_items` (sin `picked_quantity`/`fulfillment_status`, ver 1.2) |
| `vNNN_ecommerce_order_status_history.go` | CREATE `tenant_ecommerce_order_status_history` |
| `vNNN_ecommerce_customer_accounts.go` | CREATE `tenant_ecommerce_customer_accounts`, `tenant_ecommerce_customer_addresses` |
| `vNNN_ecommerce_dispatch.go` | CREATE `tenant_ecommerce_dispatches`, `tenant_ecommerce_carriers` |
| `vNNN_tenant_notifications.go` | CREATE `tenant_notifications` |
| `vNNN_ecommerce_permission_catalog_v2.go` | **Revisado.** Crea `ecommerce.orders_view`, `ecommerce.orders_prepare`, `ecommerce.orders_manage`, `ecommerce.orders_convert`, `ecommerce.orders_dispatch`, `ecommerce.orders_return`. Backfill: todo rol que ya tenía `ecommerce.orders` recibe `orders_view + orders_manage + orders_convert + orders_dispatch` (el superset que preserva el comportamiento actual, donde `ecommerce.orders` daba acceso indiferenciado a todo). `ecommerce.orders` se mantiene en el catálogo marcado `deprecated` (sigue funcionando por compatibilidad, no se elimina ni se oculta de tenants que ya la tengan asignada), pero deja de ofrecerse para asignación nueva desde la pantalla de Roles — la UI de Roles muestra las 6 nuevas |
| — sin migración de datos de producto — | La recomendación de la sección 1.8 no requiere ninguna migración de esquema; es un cambio de API (exponer `presentations`), no de modelo |

Ninguna migración borra o renombra columnas existentes de `tenant_ecommerce_orders` — todo aditivo, mismo patrón que `v132_permission_catalog_redesign.go`.

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

### 3.3 Dispatch/Fulfillment Status — sin cambios, vive en `TenantEcommerceDispatch.Status`

```
PENDIENTE_DESPACHO → DESPACHADO → EN_TRANSITO → ENTREGADO
```
Alternativo: `DEVUELTO`.

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

## 5. Tabla de transiciones — REVISADA (permisos granulares, punto 3; flujo de despacho corregido, punto 8; DEVUELTO restringido, punto 4)

| Estado actual | Acción | Estado siguiente | Permiso requerido | Efecto colateral |
|---|---|---|---|---|
| — | Cliente finaliza checkout | `PENDIENTE` | Público (sin auth) | Crea `TenantEcommerceOrder` + `TenantEcommerceOrderItem[]`; emite `TenantNotification` tipo `ecommerce.order.created` |
| `PENDIENTE` | Staff confirma y asigna sucursal | `CONFIRMADO` | `ecommerce.orders_manage` | Setea `BranchID`; registra en `StatusHistory` |
| `PENDIENTE`/`CONFIRMADO` | Staff rechaza | `RECHAZADO` | `ecommerce.orders_manage` | Requiere `Notes` con motivo |
| `CONFIRMADO` | Almacenero inicia picking | `EN_PREPARACION` | `ecommerce.orders_prepare` | — (sin persistencia por línea, ver 1.2) |
| `EN_PREPARACION` | Almacenero termina de empacar | `EMPAQUETADO` | `ecommerce.orders_prepare` | — |
| `EMPAQUETADO` | Staff marca listo para despacho | `LISTO_PARA_DESPACHO` | `ecommerce.orders_prepare` | El Almacenero **puede** ejecutar esta transición — es la última que le corresponde según su alcance (ver §7) |
| `LISTO_PARA_DESPACHO` | **Botón "Despachar"**: crea `TenantEcommerceDispatch` (transportista/tracking/bultos) | `DESPACHADO` | `ecommerce.orders_dispatch` | Crea el registro de despacho; opcionalmente imprime etiqueta. **El Almacenero NO tiene este permiso** — coincide exactamente con el alcance definido en el punto 3 del usuario |
| `DESPACHADO` | Actualización de tracking | `DESPACHADO` (sin cambio de order status) | `ecommerce.orders_dispatch` | Actualiza `TenantEcommerceDispatch.Status` |
| `DESPACHADO` | Confirmación de entrega | `ENTREGADO` | `ecommerce.orders_dispatch` | Setea `TenantEcommerceDispatch.DeliveredAt` |
| `ENTREGADO` | Reclamo/devolución | `DEVUELTO` | **`ecommerce.orders_return`** (nuevo, exclusivo Administrador/Supervisor) | Registra en `StatusHistory`; no crea todavía flujo de devolución en inventario/ventas — ver sección de riesgos §16 |
| Cualquiera antes de `DESPACHADO` | Cancelación | `CANCELADO` | `ecommerce.orders_manage` | Requiere `Notes` con motivo; si ya se convirtió a venta, bloquear cancelación del pedido (la venta se anula por el flujo de ventas existente, no aquí — sin cambios respecto a v1) |
| Cualquier estado `>= CONFIRMADO`, salvo `CANCELADO`/`RECHAZADO` | Conversión a venta | Sin cambio de `Status` (ver §4) | `ecommerce.orders_convert` | Setea `ConvertedSaleID`/`ConvertedAt` únicamente |

---

## 6. Contrato de API — permisos actualizados

### 6.1 Público — sin cambios respecto a v1

(catálogo, checkout, auth de cliente, cuenta — igual que v1, sección 6.1 original, sin permisos de tenant involucrados)

### 6.2 Panel tenant — permisos revisados

| Method | Path | Permiso | Notas |
|---|---|---|---|
| GET | `/api/ecommerce/orders` | `ecommerce.orders_view` | — |
| GET | `/api/ecommerce/orders/:id` | `ecommerce.orders_view` | — |
| PATCH | `/api/ecommerce/orders/:id/status` | Depende de la transición solicitada — el servicio valida contra la tabla §5 y exige el permiso correspondiente a esa transición específica (no un permiso único para "cambiar estado a lo que sea") | Este es el punto central del punto 3 del usuario: el mismo endpoint puede ser llamado por distintos roles, pero cada transición exige su propio permiso, evaluado server-side |
| PATCH | `/api/ecommerce/orders/:id/branch` | `ecommerce.orders_manage` | — |
| POST | `/api/ecommerce/orders/:id/convert` | `ecommerce.orders_convert` | Ya no fuerza `status` (sección 4) |
| POST | `/api/ecommerce/orders/:id/dispatch` | `ecommerce.orders_dispatch` | — |
| PATCH | `/api/ecommerce/dispatches/:id` | `ecommerce.orders_dispatch` | — |
| GET | `/api/ecommerce/orders/:id/shipping-label` | `ecommerce.orders_dispatch` | Generar la etiqueta es parte del flujo de despacho, no de preparación |
| CRUD | `/api/ecommerce/carriers` | `ecommerce.manage` | Configuración, no operación diaria |
| GET/PATCH | `/api/notifications*` | Cualquier usuario autenticado con al menos uno de los permisos `ecommerce.orders_*` ve notificaciones `ecommerce.*` | — |

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

```
PEDIDOS (/sales/pedidos-web)
  → CONFIRMAR (asigna sucursal si falta)               [ecommerce.orders_manage]    → CONFIRMADO
  → PREPARAR (Almacenero, vista de picking)             [ecommerce.orders_prepare]   → EN_PREPARACION
  → EMPAQUETAR                                          [ecommerce.orders_prepare]   → EMPAQUETADO
  → MARCAR LISTO PARA DESPACHO                          [ecommerce.orders_prepare]   → LISTO_PARA_DESPACHO
  → DESPACHAR (crea TenantEcommerceDispatch, imprime etiqueta) [ecommerce.orders_dispatch] → DESPACHADO
  → ACTUALIZAR TRACKING                                 [ecommerce.orders_dispatch]  → (DESPACHADO, sin cambio de order status)
  → CONFIRMAR ENTREGA                                   [ecommerce.orders_dispatch]  → ENTREGADO
  → (excepcional) REGISTRAR DEVOLUCIÓN                  [ecommerce.orders_return]    → DEVUELTO
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

Estado: **completada, pendiente de revisión del usuario**. Commit local `bce9625` (sin push). Entregado: migraciones v134-v138, `TenantEcommerceOrderItem`/`TenantEcommerceOrderStatusHistory`, máquina de estados con permiso por transición ([order_status.go](../internal/ecommerce/service/order_status.go)), `ConvertToSale` desacoplado del `Status` del pedido, RBAC granular (`ecommerce.orders_{view,prepare,manage,convert,dispatch,return}`). 14 tests nuevos, todos en verde; sin regresiones en el resto del backend. Reporte de cierre completo entregado en el chat de la sesión de implementación (no duplicado acá para no mantener dos copias de la misma información — este documento es el contrato de diseño, el reporte de fase es el registro de ejecución).

No implementado en esta fase (explícitamente fuera de alcance, ver §17 de v1 y el punto 7 de la aprobación): picking persistente por línea, `TenantEcommerceDispatch`/`TenantEcommerceCarrier` (Fase 8), endpoint `PATCH /orders/:id/branch` para reasignar sucursal fuera de la confirmación (Fase 5), cualquier UI de frontend.

### Fase 1.5 — Fix de locking de inventario (independiente del ecommerce)

Estado: **completada, pendiente de revisión del usuario**. Commit local `191403f` (sin push). `RecordMovementTx` ([internal/inventory/service/inventory_service.go](../internal/inventory/service/inventory_service.go)) ahora usa `SELECT ... FOR UPDATE` sobre la fila de stock antes de validar/actualizar el saldo — confirmado como race condition real (no teórica) contra MySQL 8 real antes del fix (30 salidas concurrentes contra un stock de 10 pasaban las 30) y corregido (10/30 tras el fix, stock final en 0). Test de concurrencia real (no simulado) en `internal/inventory/service/inventory_concurrency_test.go`, mismo patrón que `pkg/saas/docusage/concurrency_test.go` (DSN de MySQL opcional por variable de entorno, se salta si no está configurada — sqlite no puede reproducir esta race, serializa escrituras a nivel de archivo completo).

Hallazgo documentado, no corregido (fuera de alcance de este fix): `adjustmentInWithSerials`/`adjustmentOutWithSerials` (mismo archivo) tienen el mismo patrón de lectura-sin-lock, pero no tienen ningún caller en todo el repo — código muerto, confirmado por grep. Además, `TenantProductStock`/`TenantProductPresentationStock` no tienen índice único en (product_id, branch_id)/(presentation_id, branch_id) — dos transacciones concurrentes creando la PRIMERA fila de stock de un producto/sucursal (caso raro: solo pasa antes de que exista cualquier movimiento previo) podrían crear filas duplicadas. `FOR UPDATE` no protege contra esto (no hay fila que bloquear todavía). Es un problema distinto (duplicación en la creación inicial, no sobreventa en movimientos), de menor probabilidad real, y su fix correcto requeriría una migración de esquema (constraint único) — se deja documentado como hallazgo pendiente de decisión, no se implementa sin autorización explícita. **Deuda técnica separada, confirmado en la aprobación de Fase 1.5: no se agrega la migración de índice único hasta auditar todas las rutas que crean filas de stock y diseñar la estrategia de INSERT concurrente correspondiente.**

### Fase 2 — Variantes en catálogo público

Estado: **completada, pendiente de revisión del usuario**. Commits locales `11259bd` (backend, mistiq_backend) y `ce4e9cc` (frontend, mistiq_tenant), sin push. `ProductReportItem` gana `Presentations []PresentationOption` (id/name/sale_price/stock), poblado en `enrichReport` ([internal/products/service/product_service.go](../internal/products/service/product_service.go)) reutilizando exactamente `TenantProductPresentation`/`TenantProductPresentationStock` — mismas tablas que ya consumen POS e Inventario, sin infraestructura nueva. El catálogo público (`EcommerceService.PublicProducts`) expone esto solo para productos con variantes, respetando "Mostrar stock" (oculta el número, conserva la identidad de la presentación). En el frontend, `ProductDetailModal` agrega el selector de presentación (lista plana, sin cruces de atributos) y `storeCart` pasa a identificar líneas por `(product_id, presentation_id)` en vez de solo `product_id`, para que dos presentaciones del mismo producto no se fusionen. No se tocó `OrderItemInput`/`CreatePublicOrderAPI` (contrato de creación de pedidos), por instrucción explícita — la identidad de la presentación viaja embebida en el nombre de línea hacia WhatsApp/pedido mientras esa pieza no se construye en una fase futura.

Verificación: 8 tests nuevos de backend (Go, reales, en verde) cubriendo producto simple/una presentación/múltiples presentaciones/alcance por sucursal/"Mostrar stock"/aislamiento entre tenants. `tsc --noEmit` del frontend sin errores. **Limitación documentada, no se simuló**: no se hizo verificación end-to-end en navegador contra el backend real — el proceso Go que ya corría localmente en el puerto 3000 fue iniciado fuera de esta sesión (por el usuario) y no incluye este código (no hay hot-reload en Go), y reiniciar un proceso que no inicié yo sin preguntar no correspondía; verificar en navegador además requiere resolución de tenant por subdominio, no disponible con `localhost` plano. La verificación de este frontend se apoya en TypeScript + revisión manual del flujo exacto, no en una prueba en vivo.

### Fase 3 — Checkout + dirección + WhatsApp

Estado: **completada, pendiente de revisión del usuario**. Commits locales `55b78e6` (backend, mistiq_backend) y `dd084f1` (frontend, mistiq_tenant), sin push. `EcommerceService.CreateOrder` reescrito por completo: el cliente público solo puede enviar `product_id`/`presentation_id`/`quantity` por línea — nombre, precio, subtotal y total se resuelven SIEMPRE contra el catálogo real del tenant vigente en el momento de crear el pedido (nunca lo que el frontend tenía cacheado), dentro de una única transacción que no deja nada a medias si cualquier línea es inválida. `TenantEcommerceOrderItem.PresentationID` pasa de ser solo informativo a ser el dato estructurado real (FK verdadera a `TenantProductPresentation`, validada contra producto/tenant/estado activo). Se agregan `delivery_method` (obligatorio) y dirección (snapshot de invitado o FK autenticada, esta última soportada a nivel de servicio para cuando exista login de cliente en Fase 4). Se persiste la fila de creación en `TenantEcommerceOrderStatusHistory` y una `TenantNotification` (tabla nueva, migración v139 — solo la fila; el hub SSE/badge que la entrega en vivo al panel sigue siendo Fase 6). En el frontend, `StoreCartDrawer` pide método de entrega/dirección y arma el mensaje de WhatsApp con los ítems YA RESUELTOS que devuelve el backend después de persistir el pedido, nunca con datos del carrito del navegador.

Verificación: 20 tests nuevos de backend (17 a nivel de servicio + 3 a nivel de HTTP handler), todos reales y en verde — incluye una prueba de seguridad explícita que envía un body con `unit_price`/`subtotal`/`total` falsos y confirma que el pedido se persiste al precio REAL del catálogo, no al inventado por el cliente. `tsc --noEmit` del frontend sin errores; cadena `presentation_id` verificada por revisión estática de código (carrito → checkout → request), no por prueba en navegador — misma limitación de infraestructura ya documentada en Fase 2 (backend local desactualizado respecto al código de esta sesión, resolución de tenant por subdominio no disponible en `localhost`).
