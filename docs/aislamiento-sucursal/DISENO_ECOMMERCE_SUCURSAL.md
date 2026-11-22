# Tienda pública (ecommerce) y sucursal de despacho — Diseño

Fecha: 10-nov-2026. Alcance: `backend_principal/internal/ecommerce`, `internal/products`, `internal/sales`, `pkg/productbranch`,
`frontend_tenant/src/pages/ecommerce` (admin y tienda pública). **Solo diseño: no se modificó código, no se ejecutó nada, no se tocó producción.**
Todas las rutas de archivo son relativas a `backend_principal/` salvo que se indique `frontend_tenant/`.
Contexto previo: `AUDITORIA_Y_DISENO.md` (fila "Ecommerce" de 4.2 y decisión 8: "¿la tienda pública tendrá una sucursal de la tienda?") y `FASE_3C.md`.

---

## 1. Resumen ejecutivo

1. **Hoy un pedido online no tiene sucursal en ningún momento.** `TenantEcommerceOrder` no tiene `branch_id`
   (`pkg/database/migrations.go:1152-1166`). La sucursal aparece recién cuando un empleado convierte el pedido a venta y la elige en el
   modal (`ConvertOrderAPI`, `internal/ecommerce/handler/ecommerce_handler.go:254-299`; `ConvertOrderModal.tsx:49,163`).
2. **La tienda pública calcula y expone el stock de TODAS las sucursales, sin autenticación.** `PublicProducts` llama a
   `ProductService.ListReport` con `BranchID = 0` (`internal/ecommerce/service/ecommerce_service.go:321-340`), por lo que
   `enrichReport` suma el stock de todas las sucursales (`internal/products/service/product_service.go:497-525`) y devuelve
   `stock_by_branch` con **nombre de cada sucursal** (`ProductReportItem.StockByBranch`, `product_service.go:92-94`). Solo se oculta si
   el tenant apaga `show_stock` (`ecommerce_service.go:361-366`).
3. **Fuga adicional encontrada:** con `show_stock=false` el código pone a cero `StockTotal`, `StockByBranch`, `Serials`, `SerialCount`
   (`ecommerce_service.go:361-366`) pero **no** limpia `StockByPresentation` (`product_service.go:99-101`), que trae
   cantidad por presentación **y por sucursal** (`product_service.go:561-590`). Es un hueco del propio ocultamiento.
4. **El stock mostrado no corresponde a ninguna sucursal concreta**, y el filtro de visibilidad por sucursal (`tenant_product_branches`, v162)
   y los precios por sucursal (`TenantProductBranchPrice`, v148) **no se aplican** en la tienda: se muestra un producto aunque esté
   restringido a otras sucursales y siempre con `SalePrice` global.
5. **La validación de stock al crear el pedido compara contra el total de todas las sucursales** (`validateOrderStock`,
   `order_options.go:311-356`, comentario en 313: "todavía no hay sucursal"). Un pedido puede aceptarse con stock repartido (3 en S1 + 3 en S2
   para pedir 5) y luego fallar o no poder despacharse al convertir, porque `SaleService.Create` valida y descuenta por una sola sucursal
   (`sale_service.go:490-512`, `RecordMovementTx` `inventory_service.go:159-172`).
6. **La conversión sí valida acceso** (ya cerrado en Fase 3a: `branch.ResolveWriteBranchID`, `ecommerce_handler.go:276`, prueba
   `handler/branch_isolation_test.go`), pero la sucursal sigue siendo una elección libre del cajero, sin relación con la que el cliente vio.
7. `ListOrdersAPI`/`OrderPrintDataAPI`/`UpdateOrderStatusAPI` **no filtran ni validan sucursal** (`ecommerce_handler.go:233-252, 329-344`);
   `TenantEcommerceOrder` no está en `BranchScopedModels` (`FASE_3C.md`, lista de entidades con scope): hoy cualquier usuario con
   `ecommerce.orders` ve y modifica todos los pedidos del tenant.

**Propuesta en una línea:** agregar `branch_id` nullable al pedido + `dispatch_branch_id` por defecto en la configuración de la tienda; resolver
**una sucursal de despacho en el servidor** (no en el cliente) al armar el catálogo y al crear el pedido; calcular stock, visibilidad y
precio **solo de esa sucursal**; guardar la asignación en el pedido y usarla en la conversión; **no tocar los pedidos existentes**.

---

## 2. Evidencia: estado actual

### 2.1 Modelos

| Tabla / modelo | Campos relevantes | Referencia |
|---|---|---|
| `TenantEcommerceOrder` (`tenant_ecommerce_orders`) | `id, customer_name, customer_phone, items_json, total, status (nuevo/atendido/cerrado/cancelado), notes, converted_sale_id, converted_at`. **Sin `branch_id`.** Items en JSON (no hay tabla de ítems). | `pkg/database/migrations.go:1152-1166` |
| `TenantEcommerceSettings` (fila única id=1) | tema, `enabled`, `show_stock`, `hero_style`, `payment_methods_json`, `whatsapp_number`. **Sin sucursal.** | `migrations.go:1104-1130` |
| `TenantProductBranch` (`tenant_product_branches`, v162) | puente producto↔sucursal. **Sin filas = visible en todas**; con filas = solo en esas. | `pkg/database/product_branches.go:13-18`, `pkg/productbranch/productbranch.go:67-83` |
| `TenantProductBranchPrice` (v148) | override de precio base por sucursal, `active`; sin fila = `SalePrice` global. Único punto de resolución: `loadProductBranchPriceOverrides`. | `migrations.go:1280-1299`, `product_service.go:2126-2137` |
| Otros mecanismos de precio por sucursal | precios de `SaleUnit` por sucursal (`TenantProductSaleUnitBranchPrice`); presentaciones con stock por sucursal (`TenantProductPresentationStock`). | `migrations.go:1280` (comentario), `product_service.go:549-590` |
| Stock | `TenantProductStock(product_id, branch_id, quantity)` y `TenantProductPresentationStock(presentation_id, branch_id, quantity)`. | `product_service.go:497-525, 549-565` |
| Sucursales | `TenantBranch{Name, Address, IsMain, Active, LogoURL, DeletedAt}`; `TenantCompanyConfig.DefaultBranchID` (usada solo para imprimir el PDF del pedido). | `migrations.go:847-863, 867`; `service/print_data.go:96-103` |
| Sin scope GORM | `TenantEcommerceOrder` no está registrada en `database.BranchScopedModels`. | `FASE_3C.md` (lista de entidades con scope) |

### 2.2 Endpoints

**Públicos** (`internal/ecommerce/routes.go:41-55`; sin JWT; gate `RequireTenant` + `RequireEcommerceAvailable`: módulo + tienda activa + suscripción,
`pkg/middleware/ecommerce_gate.go:18-52`). La conexión `tenantDB` de estas rutas **no lleva `branchscope`** (el scope lo agrega
`BranchContextMiddleware`, que solo corre en rutas autenticadas).

| Endpoint | Qué hace hoy | Problema |
|---|---|---|
| `GET /public/ecommerce/settings` | config visual + contacto | Sin sucursal. |
| `GET /categories`, `GET /price-bounds` | `PublicCategories`, `PriceBounds` consultan `tenant_products` sin sucursal (`ecommerce_service.go:266-289`) | Categorías y rango de precio incluyen productos no vendibles en la sucursal de despacho; el rango usa `sale_price` global. |
| `GET /products` | `PublicProducts` → `ListReport(BranchID=0)` (`ecommerce_service.go:321`) | Stock de todas las sucursales + nombres; sin visibilidad por sucursal; precio global; `StockByPresentation` no se limpia (hallazgo 3). |
| `GET /products/:id/options` | `ProductOptions` (`order_options.go:105`): presentaciones con `stock` = `SUM` de todas las sucursales (`order_options.go:138-144`) | Stock agregado entre sucursales; precio de presentación/unidad global. |
| `GET /products/:id/gallery` | imágenes | Sin impacto. |
| `POST /orders` | `CreatePublicOrderAPI` (`ecommerce_handler.go:533-578`) → `CreateOrder` (`ecommerce_service.go:393-441`) | El precio ya lo fija el servidor (`resolveOrderItem`), bien. Sin sucursal; stock validado contra el total. |
| `GET /orders/:id/pdf` | PDF firmado por token | Imprime `DefaultBranchID`/primera sucursal (`print_data.go:96-103`) sin relación con el despacho real. |

**Administración** (`routes.go:14-35`; `ecommerce.orders` gatea pedidos):

| Endpoint | Hoy |
|---|---|
| `GET /ecommerce/orders` | `ListOrders(status, 200)` sin filtro de sucursal (`ecommerce_service.go:443-454`) |
| `GET /ecommerce/orders/:id/print-data` | por ID, sin guarda de sucursal (`ecommerce_handler.go:242-252`) |
| `PUT /ecommerce/orders/:id/status` | por ID, sin guarda (`ecommerce_handler.go:329-344`, `UpdateOrderStatus` `ecommerce_service.go:458-463`) |
| `POST /ecommerce/orders/:id/convert` | `branch_id` del body validado con `ResolveWriteBranchID` (accesible) → `ConvertToSale` (`ecommerce_handler.go:254-299`, `convert.go:70-245`) |

### 2.3 Cómo se calcula el stock mostrado y cómo se descuenta

* **Mostrado:** `ProductReportItem.StockTotal = Σ stock de todas las sucursales` (`product_service.go:497-525`); para productos con variantes se recalcula desde
  `tenant_product_presentation_stocks` sumando sucursales (`product_service.go:542-565`). El frontend de la tienda decide "Agotado / Quedan N" con
  `stock_total` (`EcommerceStorePage.tsx:466-471`, `ProductDetailModal.tsx:120-138`). **`stock_by_branch` no se usa en la tienda**: viaja pero
  no se muestra (`grep` en `frontend_tenant/src/pages/ecommerce` solo encuentra el tipo en `products.service.ts:92`). Es fuga pura.
* **Al crear pedido:** `validateOrderStock` suma `tenant_product_stocks` de todas las sucursales (`order_options.go:340-353`); no reserva ni descuenta.
* **Al convertir:** `ConvertToSale` arma líneas con el precio guardado y llama `SaleService.Create` con `BranchID` (`convert.go:215-230`). Ahí:
  1. `productbranch.EnsureAvailable(producto, sucursal)` (`sale_service.go:245`);
  2. validación de stock de **esa** sucursal (`sale_service.go:490-512`);
  3. descuento con `RecordMovementTx(... BranchID ...)` dentro de la transacción (`sale_service.go:890-909`), con guarda
     `newBalance < 0 → "stock insuficiente"` (`inventory_service.go:166-172`).
  Observación de concurrencia: `RecordMovementTx` lee el saldo con un `First` simple, **sin `SELECT … FOR UPDATE` ni update atómico condicional**
  (`inventory_service.go:149-158`). El comentario del propio `sale_service.go:498-500` lo llama "guard definitivo", pero dos conversiones/ventas
  simultáneas del mismo producto y sucursal pueden leer el mismo saldo (riesgo preexistente del sistema, no exclusivo de ecommerce; ver 8).
* **Precio al convertir:** se usa el del pedido (`PriceAuthorized: true`, `convert.go:166-171`), correcto y debe mantenerse; pero ese precio hoy
  es el **global** (el catálogo público ignora `TenantProductBranchPrice`).

---

## 3. Diseño propuesto

### 3.1 (a) Modelo de datos (aditivo)

**Pedido** (`tenant_ecommerce_orders`):

| Columna | Tipo | Nulo | Significado |
|---|---|---|---|
| `branch_id` | `uint` índice | **NULL** | Sucursal de despacho asignada. NULL = pedido anterior a esta función o sin resolver (ver 3.4). |
| `branch_source` | `varchar(20)` | NULL | Cómo se resolvió: `customer` (eligió el cliente), `store_default` (default de la tienda), `stock_fallback` (regla de stock), `staff` (reasignada por personal). Auditoría. |
| `branch_assigned_by` | `uint` | NULL | Usuario que reasignó (solo `staff`). |
| `branch_assigned_at` | `datetime` | NULL | Momento de la asignación. |

**Configuración de la tienda** (`tenant_ecommerce_settings`, fila única):

| Columna | Tipo | Nulo | Significado |
|---|---|---|---|
| `dispatch_branch_id` | `uint` | NULL | Sucursal por defecto de la tienda. NULL = no configurada. |
| `branch_mode` | `varchar(20)` | NOT NULL, default `'default'` | `default` (todo se despacha desde `dispatch_branch_id`), `customer_choice` (el cliente elige entre las sucursales habilitadas). Valor inicial seguro: `default`. |
| `branches_json` | `text` | NULL | Opcional, solo con `customer_choice`: lista de sucursales habilitadas para la tienda con etiqueta pública (`[{branch_id,label,pickup}]`). Evita exponer todas las sucursales y sus direcciones por omisión. |

Se mantiene `converted_sale_id` como está. **No se agrega `branch_id` a los ítems** (están en `items_json`): el pedido completo se despacha desde
una sola sucursal (decisión de simplicidad: ver Pregunta 3).

Registro en scope: tras tener `branch_id`, agregar `TenantEcommerceOrder` a `database.BranchScopedModels` **no es trivial** porque el
callback de Create fija la sucursal activa del contexto y la ruta pública no tiene contexto de usuario (ver 3.3 y riesgo R-2). Se propone
guardas explícitas (como `branchguard`) primero y evaluar el scope después.

#### Reglas de resolución de la sucursal de despacho (servidor, en este orden)

1. **Sucursal elegida por el cliente** — solo si `branch_mode = customer_choice` **y** la sucursal está en `branches_json`, existe y está activa.
   El cliente envía `branch_id`; el servidor lo valida contra la lista habilitada (nunca confía en cualquier id). → `branch_source=customer`.
2. **Sucursal por defecto de la tienda** — `dispatch_branch_id`, si existe y está activa. → `store_default`.
3. **Sin configuración** (NULL): comportamiento de transición: tomar `TenantCompanyConfig.DefaultBranchID`; si tampoco, la sucursal principal
   (`IsMain`). Es lo mismo que ya hace el PDF (`print_data.go:96-103`) y evita romper tiendas ya activas el día del deploy. Se registra
   como `store_default` y se **avisa en el panel** ("tu tienda despacha desde X: confírmalo").
4. **Sucursal con stock (fallback, no por omisión).** Solo si el pedido no puede cumplirse en la sucursal resuelta (1-3) y el tenant activó
   `allow_stock_fallback` (decisión del dueño, Pregunta 2): se elige, entre las sucursales habilitadas para la tienda, la que tenga stock suficiente
   para **todas** las líneas (desempate: la default, luego menor id). Si ninguna alcanza → rechazo del pedido con el mensaje actual de stock.
   **No se divide un pedido entre sucursales.** → `stock_fallback`. Se propone **desactivado por defecto**: sin él, un producto agotado en la
   sucursal de despacho se muestra "Agotado" aunque otra sucursal tenga stock, que es justo el aislamiento que se pide.

Invariante: una vez guardado `branch_id`, **el servidor nunca recalcula** la sucursal de un pedido (ni al cambiar la configuración de la tienda,
ni al editar productos). Solo se cambia por reasignación explícita de personal autorizado (3.3).

### 3.2 (b) Endpoints públicos

Principio: **la sucursal de despacho se resuelve en el servidor en cada petición pública** (`resolvePublicBranch(db, settings, requested)`),
y todo el catálogo se calcula con ella. El cliente no recibe ningún `stock_by_branch`.

| Endpoint | Cambio |
|---|---|
| `GET /settings` | Agrega `dispatch_branch: {id, name}` (solo el nombre/etiqueta pública) y, con `customer_choice`, `branches: [{id,label}]` desde `branches_json`. Nunca lista todas las sucursales del tenant. |
| `GET /products` | Acepta `?branch_id=` (solo con `customer_choice`; si no, se ignora). Llama `ListReport` con `BranchID = resuelta` **y** `RestaurantOnly=false` para activar el filtro de visibilidad por sucursal (`product_service.go:204-216`, incluye `productbranch.ListFilterSQL`) y el precio por sucursal (`loadProductBranchPriceOverrides`, `product_service.go:2126`). Después se **construye la respuesta pública con una proyección propia** (`PublicProductItem`) que solo contiene `stock_qty` de **esa** sucursal (y `stock_by_presentation` solo de esa), nunca `stock_by_branch`. Con `show_stock=false` se omite todo el stock (incluida la presentación: cierra el hallazgo 3). |
| `GET /products/:id/options` | Con la misma sucursal: `PublicPresentation.Stock` = stock de esa sucursal (en vez de `SUM` global, `order_options.go:138-144`); precio de presentación/unidad con override por sucursal si existe; 404 si el producto no es vendible en esa sucursal (`productbranch.IsAvailable`). |
| `GET /categories`, `GET /price-bounds` | Filtrar por productos vendibles en la sucursal resuelta (mismo `ListFilterSQL`) y `price-bounds` con el precio efectivo (override o global). |
| `POST /orders` | Resuelve sucursal con las reglas 1-4; `resolveOrderItem` toma el precio efectivo para esa sucursal; `validateOrderStock` valida contra **esa** sucursal (`stock WHERE branch_id = resuelta`, presentaciones y unidades de venta incluidas); verifica `productbranch.IsAvailable`; guarda `branch_id`, `branch_source`. La respuesta pública devuelve el nombre de la sucursal (no el id interno ni otras). |
| `GET /orders/:id/pdf` | Imprime la sucursal asignada al pedido (con fallback al comportamiento actual si NULL). |

Cambio de contrato: se elimina `stock_by_branch` de la respuesta pública. El frontend de la tienda no lo usa (verificado), así que no rompe
`EcommerceStorePage.tsx`/`ProductDetailModal.tsx`; **sí** hay que confirmar que ningún tenant embebido consume la API pública fuera de esta app.
Se sugiere devolver `stock_total` con el stock de la sucursal para no cambiar el frontend en la fase 1.

Caché/consistencia: el mismo `branch_id` debe usarse al mostrar y al validar el pedido. Si el catálogo se muestra para `customer_choice` y el
cliente cambia de sucursal con el carrito lleno, el frontend debe **revalidar o vaciar el carrito** (el stock y el precio dependen de la sucursal).

### 3.3 (c) Conversión pedido→venta y acceso del usuario

Cambios en `ConvertOrderAPI`/`ConvertToSale`:

1. **La sucursal sale del pedido**, no del body. Si `order.branch_id` está informado → es la sucursal de la venta. El campo `branch_id` del body
   pasa a ser opcional y solo se acepta cuando `order.branch_id IS NULL` (pedidos antiguos) o para una **reasignación explícita** (ver 4).
2. **Guarda de acceso** (patrón de `internal/sales/branchguard`, 404 si no accesible): el usuario puede ver/convertir/cambiar estado solo si
   `branch.RecordWritable(c, order.BranchID)` (sucursal **activa**, o sin sucursal). Un cajero de S2 no convierte ni ve un pedido de S1.
   `ViewAll`/ALL_BRANCHES: lectura sí (listado consolidado), escritura no (coherente con `RecordWritable`, `pkg/branch/context.go:126-133`).
3. `ListOrdersAPI`: filtrar por `ResolveReadBranchFilter` + incluir pedidos con `branch_id IS NULL` (del tenant, sin dueña) visibles a quien tenga
   `ecommerce.orders` **y** `branches.view_all`, o según Pregunta 4. `OrderPrintDataAPI` y `UpdateOrderStatusAPI`: guarda con `RecordReadable`/`RecordWritable`.
4. **Doble defensa de stock en conversión:** como el catálogo ya mostró stock solo de la sucursal asignada, el flujo normal no cambia
   (`SaleService.Create` valida y descuenta por sucursal: `sale_service.go:245, 490-512, 890-909`). Además, antes de crear la venta, se vuelve a
   comprobar `productbranch.EnsureAvailable` y el stock con mensaje claro "el pedido #N se despacha desde <sucursal>".
5. **Serie:** la serie destino debe pertenecer a la sucursal del pedido: ya lo valida `docseries.ValidateForBranch` en `SaleService.Create`
   (`sale_service.go:295`). El modal `ConvertOrderModal.tsx` deja de ofrecer selector de sucursal (muestra la asignada, solo lectura) y filtra
   series por esa sucursal; si el usuario tiene la sucursal activa distinta se le pide cambiar de sucursal (no se convierte "a distancia").
6. **Reasignación explícita** (`POST /ecommerce/orders/:id/assign-branch {branch_id}`), nuevo permiso o reuso de `ecommerce.manage`: solo si el pedido
   no está convertido ni cancelado, la sucursal destino es accesible/activa y vende todos los productos con stock suficiente; guarda
   `branch_source='staff'`, `branch_assigned_by`, `branch_assigned_at`. Es el **único** camino para dar sucursal a un pedido antiguo.
7. Atomicidad: marcar `converted_sale_id` hoy ocurre **después** de crear la venta y fuera de la transacción de la venta (`convert.go:235-244`); un fallo
   entre ambos deja venta creada y pedido sin marcar (reintento = doble venta). Mitigación propuesta: condicionar el `UPDATE … WHERE converted_sale_id IS NULL`
   y usar `OnCreatedTx` (`CreateSaleInput.OnCreatedTx`, `sale_service.go:~950`) para marcar el pedido dentro de la misma transacción. Se incluye en la fase 3.

### 3.4 (d) Pedidos existentes sin sucursal (no reasignar automáticamente)

* **Ningún UPDATE masivo.** Los pedidos previos quedan con `branch_id = NULL` y siguen convirtiéndose como hoy (el usuario elige sucursal en el modal;
  ese flujo **se mantiene solo para `branch_id IS NULL`**).
* **Informe previo (solo lectura, antes de cualquier decisión)**, por tenant:
  1. Conteo de pedidos por estado (`nuevo/atendido/cerrado/cancelado`) y cuántos están convertidos.
  2. Para los **convertidos**: sucursal real = `tenant_sales.branch_id` vía `converted_sale_id`. Se puede **documentar** (no escribir) la sucursal efectiva
     para análisis; un backfill de esos es seguro técnicamente (la venta ya existe) pero se propone **no** hacerlo hasta que el dueño lo pida.
  3. Para los **abiertos** (`nuevo/atendido`): cuántos, antigüedad, y por cada uno si el stock actual alcanza en cada sucursal.
  4. Cantidad de tenants con ecommerce habilitado y con más de una sucursal activa (donde el problema es real).
  5. Productos con restricción en `tenant_product_branches` que están publicados en el catálogo digital (los que cambiarían de visibilidad).
* Salida: un reporte (CSV/JSON) que el dueño revisa; la asignación se hace después, por pedido, con el endpoint de reasignación (3.3-6), o un script
  explícito con lista aprobada. Los cerrados/cancelados/convertidos **no se tocan**.
* Mientras `branch_id` sea NULL, las guardas tratan al pedido como "sin sucursal" (visible a quien tenga `ecommerce.orders`, como hoy), para no ocultar
  pedidos vigentes a nadie el día del deploy.

### 3.5 (e) Migración aditiva por versión de tenant

* **V177** (`pkg/database/tenantmigrations/v177_ecommerce_order_branch.go`, siguiente a `V176BranchPermissionsAndAssignments`,
  último registrado en `registry.go`). Si V176 aún no se desplegó, V177 igual va después; no hay dependencia de datos entre ambas.
* Contenido (idempotente, patrón de `v106_ecommerce_order_conversion.go` y `v127_ecommerce_show_stock.go`: structs privados + `HasTable`/`HasColumn` + `AddColumn`):
  1. `tenant_ecommerce_orders`: `branch_id` (NULL, índice `idx_tenant_ecommerce_orders_branch_id`), `branch_source`, `branch_assigned_by`, `branch_assigned_at`.
  2. `tenant_ecommerce_settings`: `dispatch_branch_id` (NULL), `branch_mode` (default `'default'`), `branches_json` (NULL).
  3. **Sin backfill de pedidos.** Para settings: `dispatch_branch_id` se deja NULL (la regla 3 lo resuelve en lectura); opcionalmente rellenar con
     `tenant_company_configs.default_branch_id` **solo** en tenants con una única sucursal activa (caso sin ambigüedad). Cualquier otro caso queda NULL.
  4. Si falta la tabla (`!HasTable`) retorna `nil`, igual que v106.
* Estructuras en código: actualizar `TenantEcommerceOrder` y `TenantEcommerceSettings` en `migrations.go` (el baseline `ApplyBaselineSchema`
  usa los structs actuales: `tenant_baseline_schema.go:41-43`; V177 con `HasColumn` es idempotente tras el baseline).
* **Regla de la memoria del proyecto:** `backend_principal` no usa AutoMigrate en producción; todo campo nuevo de un struct `tenant_*` **necesita su
  migración versionada ANTES de desplegar** (incidente 09-sep-2026). Orden: migrate-fleet V177 → deploy backend → deploy frontend.
* Compatibilidad hacia atrás: mientras V177 no esté aplicada en un tenant, el código nuevo **no debe** seleccionar/insertar las columnas nuevas
  (guard `HasColumn` cacheado, como `productbranch.Ready`, `pkg/productbranch/productbranch.go:33-46`) o fallará con "unknown column". Es una
  condición de diseño obligatoria.

### 3.6 (f) Pruebas necesarias

Backend (Go, SQLite como `handler/branch_isolation_test.go`, `tenantmigrations/*_test.go`):

1. **Migración V177:** crea columnas; es idempotente (2 ejecuciones); no modifica pedidos existentes; sin tabla no falla; no pisa `dispatch_branch_id`.
2. **Resolución de sucursal:** tabla de casos (cliente elige habilitada / no habilitada / inexistente / inactiva; default configurado / NULL con
   `DefaultBranchID` / NULL con solo `IsMain`; fallback de stock activado/desactivado; ninguna sucursal con stock).
3. **Aislamiento de stock público:** 2 sucursales con stocks distintos; `/products` devuelve solo el stock de la despacho; **ningún** campo
   `stock_by_branch` ni nombre de otra sucursal en el JSON (aserción de esquema, incluido `stock_by_presentation` con `show_stock=false`);
   producto restringido a S2 **no** aparece si la despacho es S1; precio con override de S2 no aparece en S1.
4. **Checkout:** pedido con 5 unidades, stock 3 en S1 + 3 en S2 → rechazado con despacho S1; con fallback activado y sin sucursal que alcance → rechazado;
   guarda `branch_id`/`branch_source`; el cliente no puede forzar una sucursal no habilitada (`branch_id` manipulado en el body); precio manipulado ignorado (ya existe `convert_price_test.go`/`order_options_test.go`, ampliar).
5. **Conversión:** usa la sucursal del pedido; un vendedor de otra sucursal obtiene 404/403 (`RecordWritable`); body `branch_id` ignorado si el pedido
   tiene sucursal; pedido legado (`NULL`) conserva el flujo actual; descuenta stock de la sucursal correcta (movimiento en `tenant_stock_movements`);
   serie de otra sucursal rechazada; doble conversión no genera dos ventas.
6. **Listado/detalle/estado:** vendedor S1 no ve pedidos de S2; ALL_BRANCHES solo lectura; pedidos `NULL` visibles según la regla elegida.
7. **Concurrencia (ver R-3):** N goroutines convirtiendo/pidiendo el mismo producto con stock 1 en la misma sucursal → exactamente una tiene éxito y el
   saldo nunca queda negativo (hoy esta prueba probablemente falle sin bloqueo de fila: si falla, es un hallazgo a corregir en `RecordMovementTx`).
8. **Regresión Tukichef/POS:** el resto de `ListReport` con `BranchID>0` no cambia (pruebas existentes de `product_service`).

Frontend: la tienda no pierde stock/precio al no recibir `stock_by_branch`; cambio de sucursal (modo `customer_choice`) revalida/vacía carrito
(`storeCart.ts`); `ConvertOrderModal` sin selector de sucursal cuando el pedido la trae; `PedidosWebPage` muestra la sucursal por pedido.

### 3.7 (g) Riesgos, orden de despliegue y rollback

| # | Riesgo | Mitigación |
|---|---|---|
| R-1 | Tiendas activas hoy con stock repartido: tras el cambio, productos que se veían disponibles pasan a "Agotado" (solo cuenta la despacho). | Informe previo por tenant (3.4-5 + stock por sucursal); `allow_stock_fallback` disponible; aviso en el panel; desplegar `branch_mode=default` con la regla 3 (misma sucursal que hoy imprime el PDF). |
| R-2 | Agregar `TenantEcommerceOrder` a `BranchScopedModels` rompería `POST /orders` público (Create sin contexto de usuario; `FASE_3C.md`: auditoría/estricto con `ErrScopeMissing`). | No registrar el modelo en esta etapa; guardas explícitas en handlers admin; evaluar scope en una fase posterior con `branchscope.System` solo en el alta pública. |
| R-3 | Sobreventa por carrera: `RecordMovementTx` lee saldo sin bloqueo (`inventory_service.go:149-158`). La tienda pública sin login aumenta la probabilidad. | Fuera del alcance directo, pero la prueba 7 lo hace visible; corrección recomendada: update atómico `SET quantity = quantity - ? WHERE … AND quantity >= ?` o `FOR UPDATE`. Decisión del dueño (Pregunta 5). |
| R-4 | Cambio de contrato de la API pública (se quita `stock_by_branch`). | Verificado que la tienda propia no lo usa; mantener `stock_total` (de la sucursal); anunciar. |
| R-5 | Columnas nuevas antes de migrar → error "unknown column" en tenants sin V177. | Guard `HasColumn` cacheado; orden: migrate-fleet primero. |
| R-6 | Sucursal por defecto desactivada/eliminada después de asignar. | Al resolver se valida activa; el pedido ya creado conserva su `branch_id` (FK lógica); la conversión falla con mensaje claro y se ofrece reasignar. |
| R-7 | Modo `customer_choice` expone direcciones/nombres de sucursales. | Solo etiquetas definidas en `branches_json` (nunca la tabla completa) y la lista es opt-in. |
| R-8 | Catálogo cacheado por CDN/navegador con otra sucursal. | La sucursal resuelta debe formar parte de la clave de caché; hoy el PDF usa `Cache-Control: private` (`ecommerce_handler.go:603`); los endpoints JSON no declaran caché pública: mantenerlo. |

**Orden de despliegue:** (1) código backend con guards `HasColumn` (compatible sin V177) → (2) `migrate-fleet` V177 → (3) frontend Tukifac (admin: columna
sucursal, modal sin selector, ajuste "Sucursal de la tienda"; tienda pública) → (4) configurar `dispatch_branch_id` por tenant (informe previo) →
(5) activar `branch_mode=customer_choice` / `allow_stock_fallback` solo donde se pida. Nunca el frontend antes que V177 (misma advertencia que el lote 10-nov).

**Rollback:** como la migración es solo aditiva (columnas NULL/default), basta revertir el binario/frontend: las columnas quedan ignoradas sin daño. No
se requiere `DROP COLUMN`. Un interruptor de emergencia propuesto: `ECOMMERCE_BRANCH_ISOLATION=0` (variable de entorno) que haga
que `resolvePublicBranch` devuelva 0 (= comportamiento actual) sin redeploy de datos. Los pedidos creados con sucursal siguen siendo válidos.

### 3.8 (h) Esfuerzo por fase

| Fase | Contenido | Esfuerzo | Riesgo |
|---|---|---|---|
| **E0** | Hotfix independiente: no exponer `stock_by_branch`/`StockByPresentation` (proyección pública) y cerrar el hueco con `show_stock=false`; guardas `RecordReadable/Writable` en `ListOrders/print-data/status`. Sin migración. | 0,5–1 día | Bajo |
| **E1** | V177 + structs + guard `HasColumn` + `resolvePublicBranch` + ajuste "Sucursal de la tienda" en `EcommerceGeneralSettings` + informe de pedidos existentes (solo lectura). | 1,5–2 días | Bajo-medio |
| **E2** | Catálogo público por sucursal (visibilidad, precio override, stock, categorías, price-bounds, options) + `validateOrderStock` por sucursal + guardar `branch_id` en `POST /orders`. | 2–3 días | Medio |
| **E3** | Conversión desde la sucursal del pedido, guarda de acceso, modal sin selector, reasignación explícita, marcado atómico del pedido. | 1,5–2 días | Medio |
| **E4** | `customer_choice` (selector en la tienda, `branches_json`, revalidación de carrito) y `allow_stock_fallback`. Opcional, bajo demanda. | 2–3 días | Medio |
| **E5** | Pruebas (matriz de 3.6, incluida concurrencia) y, si se decide, corrección de `RecordMovementTx` (R-3). Se escriben junto con cada fase. | +1–2 días | Medio |

Total estimado E0–E3 ≈ 6–8 días; con E4 y concurrencia ≈ 10–13 días. Recomendación: **E0 ya** (cierra la fuga sin esperar decisiones).

---

## 4. Preguntas abiertas para el dueño

1. **¿Una sucursal fija por tienda o el cliente elige?** Recomendación: empezar con sucursal fija (`branch_mode=default`) y dejar `customer_choice` para E4.
2. **Fallback por stock:** si la sucursal de despacho no tiene stock pero otra sí, ¿se rechaza (aislamiento estricto) o se despacha desde la otra? Recomendación: rechazar por defecto, con opción por tenant.
3. **¿Un pedido se despacha siempre desde una sola sucursal** (no se divide entre varias)? Recomendación: sí.
4. **Pedidos sin sucursal (los actuales y los que queden NULL):** ¿quién debe verlos? ¿cualquier usuario con `ecommerce.orders` (como hoy) o solo quien ve todas las sucursales? ¿Se asignan a mano, o prefiere un informe y luego decidir?
5. **Sobreventa:** ¿se acepta corregir el descuento de stock para que sea atómico (afecta a todo el sistema de ventas, no solo a la tienda) o solo se mide primero?
6. **¿Reservar stock al crear el pedido?** Hoy no se reserva; entre el pedido y la conversión el stock puede agotarse. Recomendación: no reservar en v1; informar al cliente que la disponibilidad se confirma al atender.
7. **Reasignación:** ¿quién puede cambiar la sucursal de un pedido (`ecommerce.manage`, `branches.manage`, o nuevo permiso) y hasta qué estado?
8. **Precio por sucursal en la tienda:** ¿el cliente debe ver el precio de la sucursal de despacho (override de `TenantProductBranchPrice`)? Recomendación: sí (es el precio que se cobrará).
9. **Datos que ve el público de la sucursal:** ¿nombre, dirección, teléfono de recojo? (afecta `branches_json`).
10. **Productos con `show_in_digital_catalog` hoy restringidos a otras sucursales:** ¿se dejan de mostrar en la tienda (comportamiento nuevo, correcto con el aislamiento) o se prefiere avisar antes por tenant?
11. **¿Conviene que Tukichef/POS restaurante use esta misma sucursal de tienda** (carta online)? No se investigó (fuera de alcance).

---

## 5. Las 5 decisiones clave que requieren aprobación

1. **Sucursal de despacho única por pedido, resuelta en el servidor** con orden: elegida por el cliente (solo si el tenant lo habilita) → default de la tienda (`dispatch_branch_id`) → `DefaultBranchID`/sucursal principal; el cliente nunca decide libremente. Pedido no se divide.
2. **Fallback por stock:** desactivado por defecto (aislamiento estricto: agotado en la sucursal de despacho = agotado en la tienda); activable por tenant.
3. **Contrato público:** eliminar `stock_by_branch` (y limpiar `stock_by_presentation` con `show_stock=false`) y calcular stock, visibilidad y precio solo de la sucursal de despacho; incluye hotfix E0 inmediato.
4. **Pedidos existentes: no migrar ni reasignar.** Quedan `branch_id = NULL`, conservan el flujo actual de conversión (el usuario elige sucursal); se entrega un informe de solo lectura y la asignación se hace explícitamente por pedido.
5. **Conversión desde la sucursal del pedido** con guarda de acceso (solo usuarios con la sucursal activa; ALL_BRANCHES solo lectura), `branch_id` del body ignorado cuando el pedido ya tiene sucursal, y reasignación explícita con auditoría; y, aparte, decidir si se corrige la **atomicidad del descuento de stock** (R-3) que afecta a toda la plataforma.
