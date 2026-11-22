# Propuesta E1 — Tienda pública: sucursal de despacho concreta y stock por sucursal

Fecha: 10-nov-2026. **Solo propuesta funcional para decidir. No se modificó código, BD ni producción.**
Base: `DISENO_ECOMMERCE_SUCURSAL.md` (diseño técnico previo), `FASE_3C.md`, `INFORME_DESPLIEGUE.md` §8 (hotfix E0, commit `7aaecb6`)
y el código actual. Rutas relativas a `backend_principal/` salvo que se diga `frontend_tenant/`.

## 0. Prioridades del dueño (se toman como requisitos)

* **(a)** Cada pedido online **nuevo** tiene una sucursal de despacho **concreta** (`branch_id` > 0), nunca nula.
* **(b)** La disponibilidad que ve el cliente y la que valida el servidor al crear el pedido se calculan **exclusivamente** sobre esa sucursal.

Consecuencia de diseño: la sucursal se decide **en el servidor, antes de mostrar nada**, y la misma sucursal se usa para mostrar, validar y guardar.

---

## 1. Qué sigue vigente y qué cambió respecto al diseño previo (verificado en el código de hoy)

| Afirmación del diseño previo | Estado hoy | Evidencia |
|---|---|---|
| `stock_by_branch` y `serials` viajan en la API pública | **Desactualizado: ya corregido (E0, `7aaecb6`).** Se anulan siempre. | `internal/ecommerce/service/ecommerce_service.go:365-377` |
| Con `show_stock=false` se filtraba `stock_by_presentation` (hallazgo 3) | **Corregido.** Sin `show_stock` no hay stock ni por presentación; con `show_stock` se suma entre sucursales y se anonimiza (`branch_id=0`, sin nombre). | `ecommerce_service.go:371-377` y `aggregatePresentationStock` (línea 378+) |
| `stock_total` es la **suma de todas las sucursales** | **Vigente (es lo que falta).** `PublicProducts` sigue llamando `ListReport` sin `BranchID`. Un cliente anónimo sigue viendo "hay 6" aunque la sucursal que despacha tenga 0. | `ecommerce_service.go:321-340` |
| `validateOrderStock` valida contra el total de todas las sucursales | **Vigente.** Sigue con el comentario "todavía no hay sucursal"; suma `tenant_product_stocks` y `tenant_product_presentation_stocks` sin filtrar sucursal. | `internal/ecommerce/service/order_options.go:311-356` |
| `ProductOptions` muestra stock de presentación = suma de todas las sucursales | **Vigente.** | `order_options.go:138-144` |
| Catálogo público ignora `tenant_product_branches` (v162) y precio por sucursal (v148) | **Vigente.** `PriceBounds` y `PublicCategories` consultan `tenant_products` directo, con `sale_price` global. | `ecommerce_service.go:266-289` |
| `TenantEcommerceOrder` sin `branch_id`; settings sin sucursal | **Vigente.** | `pkg/database/migrations.go:1152-1166` y `1104-1133` |
| `ListOrders`, `print-data`, `status` de pedidos sin guarda de sucursal | **Vigente** (E0 solo cubrió la API pública; el hotfix no tocó pedidos). El informe de despliegue lo confirma ("Pendiente de ecommerce"). | `handler/ecommerce_handler.go:233-252, 329-344` |
| La conversión valida acceso del `branch_id` del body | **Vigente**, pero la sucursal sigue siendo elección libre del cajero (modal con selector). | `ecommerce_handler.go:254-299`, `frontend_tenant/src/pages/ecommerce/ConvertOrderModal.tsx:41-49,163` |
| `converted_sale_id` se marca fuera de la transacción de la venta | **Vigente** (riesgo de doble venta ante un fallo entre ambos pasos). `CreateSaleInput.OnCreatedTx` ya existe y está probado (`sale_service.go:206-212, 990`; `sale_idempotency_test.go:169`), así que el arreglo es viable. | `internal/ecommerce/service/convert.go:235-244` |
| `RecordMovementTx` sin bloqueo (carrera de stock) | **Vigente.** Lee saldo con `First` y luego escribe; no hay `FOR UPDATE` ni UPDATE condicional. | `internal/inventory/service/inventory_service.go:154-176, 186-215` |
| La conexión de las rutas públicas no lleva scope de sucursal | **Vigente y relevante:** el scope GORM (3C) solo corre en rutas autenticadas, por eso la sucursal debe filtrarse **explícitamente** en cada consulta pública. | `FASE_3C.md`; `internal/ecommerce/routes.go:44` |
| Migración E1 = V177 | **Desactualizado: V177, V178 y V179 ya están tomadas** (`registry.go:152-153` termina en `V179SingleMainBranchAndOrphans`). La migración de ecommerce será **V180** (tentativa). | `pkg/database/tenantmigrations/registry.go` |
| Tukichef/POS restaurante usa la API de tienda | No: solo `frontend_tenant` consume `/public/ecommerce` y `/ecommerce/orders` (`services/ecommerce.service.ts`, `store/StoreCartDrawer.tsx`). **Las apps instaladas (Tukichef Android/Tauri) no se ven afectadas** por cambios en esta API. | grep en `frontend_restaurant_tenant/src` sin resultados |
| Sucursal principal no garantizada | **Mejoró:** V179 deja exactamente **una** sucursal principal por tenant. Esto permite usar "la principal" como resolución de último recurso sin ambigüedad. | `INFORME_DESPLIEGUE.md` §10 |

Hallazgos nuevos al releer el código:

1. Aun con E0, `ListReport` devuelve el producto completo (`database.TenantProduct` embebido): sigue conviniendo una **proyección pública propia** (`PublicProductItem`) en E2 para no depender de ir anulando campos uno a uno.
2. `ListReport` con `BranchID>0` y `RestaurantOnly=false` ya aplica visibilidad por sucursal (`product_service.go:164-216`, con `productbranch.ListFilterSQL`), y su criterio también muestra "sin vincular" (producto sin fila de stock en ninguna sucursal) con stock 0. Para la tienda esto significa que un producto sin stock cargado aparece "Agotado" y no oculto; hay que confirmarlo con una prueba (§9).
3. El PDF del pedido imprime la sucursal por defecto de la empresa o la primera (`print_data.go:96-103`): al tener `branch_id` en el pedido debe imprimir **esa**.

---

## 2. Cómo se garantiza la invariante "pedido nuevo ⇒ `branch_id` concreto"

Cinco capas, de la más débil a la más fuerte:

1. **Resolución en el servidor, nunca en el cliente.** Función única `resolvePublicBranch(db, settings, requestedBranchID) (branchID uint, source string, err error)`. Devuelve siempre `> 0` o un error tipado. Todas las rutas públicas la usan (catálogo, opciones, categorías, límites de precio, pedido).
2. **`CreateOrder` no puede guardar sin sucursal.** Recibe `BranchID` ya resuelto y, antes de `Create`, rechaza `branch_id == 0`. No existe camino público que llame `db.Create(order)` sin pasar por ahí.
3. **NOT NULL lógico a nivel de modelo.** Hook GORM `BeforeCreate` en `TenantEcommerceOrder`: si `BranchID` es nil o 0 → error. Cubre cualquier alta futura hecha por otro código (importadores, pruebas, scripts). La columna en BD queda nullable porque los pedidos anteriores seguirán en NULL.
4. **Restricción de BD solo cuando se pueda.** Un `CHECK (branch_id IS NOT NULL)` o `NOT NULL` en la columna **no es posible mientras existan pedidos antiguos en NULL**. Se deja como paso opcional posterior, cuando el dueño haya asignado o archivado todos los antiguos (§6): entonces V18x podría volverla `NOT NULL`. No se recomienda hoy.
5. **Prueba de contrato** que falla si aparece un alta sin sucursal (§9) y métrica en log: contador de pedidos creados con `branch_source` y alerta si alguno quedara sin sucursal.

### Si la tienda no tiene sucursal configurada

| Opción | Comportamiento | Pros | Contras |
|---|---|---|---|
| **A. Bloquear la tienda** con mensaje al admin | `POST /orders` → 503 `STORE_BRANCH_NOT_CONFIGURED`; la tienda muestra "Tienda en configuración"; el panel exige elegir sucursal | Cero decisiones implícitas | Una tienda que hoy vende **dejaría de vender** el día del deploy si nadie configura; alto costo operativo y de soporte |
| **B. Usar la principal** (V179 garantiza una única) | Se resuelve a la principal activa y se guarda como `store_default` | La tienda sigue vendiendo; el pedido igual lleva sucursal concreta (cumple (a)) | Si la principal no es la que realmente despacha, el stock mostrado sería el de la sucursal equivocada |
| **C. Mixta (recomendada)** | V180 **rellena** `dispatch_branch_id` con la principal en cada tenant (dato explícito, no pedidos). Después: sucursal configurada y activa → se usa; **nunca configurada (NULL, p. ej. tenant nuevo)** → principal activa + aviso al admin; **configurada pero desactivada/eliminada** → **bloquear** con mensaje al admin (cerrar un local no debe mover el stock a otro sin que alguien lo decida); **sin ninguna sucursal activa** → bloquear | Cumple (a) siempre, no corta la venta el día del deploy, y evita cambios silenciosos | Un poco más de reglas; requiere banner "confirma la sucursal de tu tienda" |

**Recomendación: C.** Datos del dueño: ¿acepta que V180 asigne la sucursal principal como despacho inicial de todas las tiendas, con aviso para confirmarlo? (Es un dato de **configuración de la tienda**, no un reasignado de pedidos.)

---

## 3. Decisión 1 — Cómo se determina la sucursal de despacho

| Opción | Cómo funciona | UX cliente | Carga operativa | Sobreventa |
|---|---|---|---|---|
| **1A. Sucursal por defecto de la tienda** (`dispatch_branch_id`) | El admin elige una sucursal en Ajustes de la tienda; todo se calcula con ella | Sin cambios: no se le pregunta nada | Baja: una sola configuración | Baja: stock de una sola sucursal, coherente de punta a punta |
| **1B. Elige el cliente** (`branch_mode=customer_choice`) | El cliente selecciona entre las sucursales habilitadas (lista opt-in `branches_json` con etiqueta pública); el catálogo se recalcula | Un paso más; útil si hay recojo en varios locales | Media: el tenant mantiene la lista y atiende en varias sucursales | Baja si el carrito se revalida al cambiar de sucursal |
| **1C. La que tenga stock** (automática) | El servidor busca una sucursal con stock suficiente | Aparentemente cómoda | Alta e impredecible: cada pedido puede caer en un local distinto | **Contradice (b):** el stock mostrado no puede ser "de una sola sucursal" si la sucursal se decide después; el catálogo no sabe qué mostrar |

**Compatibilidad.** *Tenants de 1 sucursal*: 1A resuelve a la única sucursal; el selector de Ajustes se oculta; el cliente y el admin no notan nada. *Tenants sin V180*: la tienda sigue en modo actual (E0) hasta migrar (§8). *Apps instaladas*: no consumen esta API.

**Recomendación: 1A como único modo de E1/E2 (obligatorio por defecto) y 1B solo en E4 y solo para quien lo pida.** 1C se descarta como regla principal.
Detalle técnico: el cliente **siempre envía** el `branch_id` que vio (el de `dispatch_branch` en `/settings` o el que eligió); en modo 1A, si no coincide con el vigente en el servidor → **409 `BRANCH_CHANGED`** y la tienda recarga el catálogo (evita que el cliente haya visto precios/stock de una sucursal y se valide otra). Clientes antiguos o bundles en caché que no lo envían se aceptan con la sucursal vigente (compatibilidad).
**Dato que necesito del dueño:** ¿cuántos tenants reales tienen más de una sucursal con tienda activa y qué sucursal debe despachar en cada uno? (El informe de §6 lo obtiene en solo lectura.) ¿Algún cliente exige elegir sucursal ya?

---

## 4. Decisión 2 — Fallback por stock cuando la sucursal de despacho no tiene

| Opción | Efecto | Riesgo |
|---|---|---|
| **2A. Desactivado (aislamiento estricto)** | Agotado en la sucursal de despacho = agotado en la tienda, aunque otra sucursal tenga | El negocio puede "perder" ventas que otra sucursal podría atender (decisión consciente) |
| **2B. Activado por tenant (automático)** | Si la despacho no alcanza, el servidor asigna otra sucursal con stock para **todas** las líneas | **Rompe (b) en lo visible:** el catálogo mostraría "Agotado" (stock de la despacho) pero el servidor aceptaría el pedido; confunde al cliente y al admin. No se divide el pedido |
| **2C. Sugerencia (el cliente decide)** | El catálogo/checkout dice "Sin stock en X; disponible en Y" y, solo en modo `customer_choice`, el cliente cambia explícitamente de sucursal (queda `branch_source=customer`) | Requiere 1B; más UI |

**Recomendación: 2A por defecto y para todos en E1–E3.** Para quien necesite más cobertura: **2C** dentro de `customer_choice` (E4). **2B no se recomienda**; si el dueño insiste, se implementa como interruptor por tenant `allow_stock_fallback` con el pedido guardando la sucursal efectiva y mostrando al cliente la sucursal asignada.
Compatibilidad: 1 sola sucursal → no existe fallback posible (sin efecto). Sin V180 → sin efecto.
**Dato del dueño:** ¿prefiere "perder la venta" a despachar desde otra sucursal sin que el cliente lo sepa?

---

## 5. Decisión 3 — Contrato de la API pública

Reglas (todas con la sucursal resuelta por el servidor):

* **Stock:** `stock_total` pasa a ser **solo el de la sucursal de despacho** (mismo nombre de campo: el frontend actual no cambia). Nunca otras sucursales, ni `stock_by_branch`, ni nombres de otras sucursales (ya garantizado por E0, se mantiene y se amplía a la proyección propia). Con `show_stock=false`: sin stock (igual que hoy). Presentaciones: stock de esa sucursal.
* **Precio:** el efectivo de esa sucursal (override de `TenantProductBranchPrice`, unidades de venta por sucursal, presentaciones); sin override = global. El servidor recalcula el precio del pedido igual que hoy (`resolveOrderItem`); el cliente nunca lo decide.
* **Visibilidad:** solo productos vendibles en esa sucursal (`productbranch.ListFilterSQL`). Categorías y `price-bounds` se calculan sobre ese mismo conjunto.
* **Producto no visible en esa sucursal:** (i) no aparece en `/products`, `/categories`, `/price-bounds`; (ii) `/products/:id/options` y `/products/:id/gallery` responden 404 "producto no disponible" (no se revela que existe en otra sucursal); (iii) `POST /orders` lo rechaza con mensaje "'X' no está disponible para despacho desde <sucursal>" y **no** crea el pedido.
* **Qué sucursal se nombra:** solo la de despacho (`dispatch_branch: {id, name}`, y opcionalmente dirección/teléfono de recojo si el dueño lo decide). Con `customer_choice`, solo las de la lista opt-in. **Nunca** la tabla completa de sucursales.

Alternativas para el producto no visible: *Opción A (recomendada)* ocultarlo; *B* mostrarlo "No disponible en tu sucursal" (revela que existe, más ruido, riesgo de ser tomado como "agotado"); *C* mostrarlo igual y rechazar al pedir (mala UX). **Recomendación: A.**

Impacto de negocio (R-1 del diseño previo): tiendas con stock repartido verán productos que hoy aparecen disponibles pasar a "Agotado" o a desaparecer si estaban restringidos a otras sucursales. Mitigación: informe previo por tenant (§6) y comunicarlo antes del cambio.

### Ejemplo JSON antes / después

`GET /api/public/ecommerce/settings` (agrega, con el mismo resto):

```json
{
  "store_name": "Bodega Central",
  "show_stock": true,
  "branch_mode": "default",
  "dispatch_branch": { "id": 3, "name": "Sucursal Miraflores" }
}
```

`GET /api/public/ecommerce/products` — **antes** (hoy, tras E0; stock = suma de TODAS las sucursales):

```json
{ "data": [ { "id": 41, "name": "Aceite 1L", "sale_price": 9.50, "stock_total": 6,
              "stock_by_branch": null, "serials": null, "has_sale_units": false } ], "total": 1 }
```

**Después** (mismo campo `stock_total`, calculado solo sobre la sucursal 3, con precio efectivo de esa sucursal; la proyección pública no contiene campos internos):

```json
{ "data": [ { "id": 41, "name": "Aceite 1L", "sale_price": 9.90, "stock_total": 2,
              "has_sale_units": false } ],
  "total": 1,
  "branch": { "id": 3, "name": "Sucursal Miraflores" } }
```

`POST /api/public/ecommerce/orders` — **antes**:

```json
{ "customer_name": "Ana", "customer_phone": "999111222",
  "items": [ { "product_id": 41, "quantity": 5, "unit_price": 9.5 } ] }
→ 201 { "data": {...}, "order_number": 87, "pdf_token": "..." }   // acepta (hay 6 entre todas las sucursales)
```

**Después**:

```json
{ "customer_name": "Ana", "customer_phone": "999111222", "branch_id": 3,
  "items": [ { "product_id": 41, "quantity": 5 } ] }
→ 400 { "error": "no hay stock suficiente de 'Aceite 1L': pediste 5 y hay 2 en Sucursal Miraflores",
        "code": "INSUFFICIENT_STOCK" }

con cantidad 2:
→ 201 { "data": { "id": 88, "total": 19.8 }, "order_number": 88, "pdf_token": "...",
        "branch": { "name": "Sucursal Miraflores" } }       // branch_id guardado: 3, source: store_default
```

Errores nuevos: `409 BRANCH_CHANGED` (el cliente vio otra sucursal que la vigente), `503 STORE_BRANCH_NOT_CONFIGURED`, `404/400 PRODUCT_NOT_IN_BRANCH`.
**Compatibilidad del contrato:** campos existentes conservan nombre; solo cambia su valor (stock/precio de la sucursal) y se agrega `branch`/`dispatch_branch`. `branch_id` en el body es opcional (clientes antiguos siguen funcionando).

---

## 6. Decisión 4 — Pedidos existentes sin sucursal

Regla firme: **ningún UPDATE masivo, ninguna reasignación automática.** Los pedidos antiguos quedan con `branch_id = NULL` para siempre salvo acción explícita.

| Opción | Qué es | Pros | Contras |
|---|---|---|---|
| **4A. Dejarlos NULL y mantener el flujo actual** | Los antiguos siguen convirtiéndose eligiendo sucursal en el modal | Cero riesgo de datos | Seguirán sin aislamiento visible; conviven dos mundos en el listado |
| **4B. Informe de solo lectura + asignación explícita, pedido por pedido** (recomendada) | Informe por tenant; luego el personal autorizado asigna caso a caso (reuso del endpoint de reasignación, §7) | Control total, auditoría completa | Trabajo manual si hay muchos abiertos |
| **4C. Asignación por lote con confirmación** | Una pantalla (o script con lista aprobada) propone sucursal por pedido abierto, el humano revisa la lista y confirma | Escala a tenants con muchos pedidos | Más desarrollo; riesgo si se confirma sin revisar → exige vista previa y confirmación tipeada |

**Informe previo (solo lectura, antes de cualquier decisión)** por tenant con ecommerce activo: pedidos por estado y cuántos convertidos; para los convertidos, la sucursal real = `tenant_sales.branch_id` vía `converted_sale_id` (se **documenta**, no se escribe); para los abiertos (`nuevo`/`atendido`): cantidad, antigüedad y, por cada uno, en qué sucursales alcanza hoy el stock; tenants con tienda y más de una sucursal activa; productos publicados en la tienda restringidos a otras sucursales (los que cambiarían de visibilidad).

**Qué ve el admin mientras tanto.** Los pedidos NULL se muestran en el listado con la etiqueta "Sin sucursal (anterior)" y un filtro propio; **siguen visibles para quien tiene `ecommerce.orders`** (igual que hoy, para no esconder pedidos vigentes el día del deploy). Se pueden **convertir** como hoy (el modal pide sucursal, limitada a la activa del usuario). Los cerrados/cancelados/convertidos no se tocan ni se les pide nada. En tenants de 1 sucursal la etiqueta no se muestra y el modal preselecciona la única sucursal.

**Recomendación: 4A + 4B de inmediato; 4C solo si el informe muestra tenants con muchos pedidos abiertos.** No se hace backfill de los convertidos (aunque sería técnicamente seguro) hasta que el dueño lo pida.
**Dato del dueño:** ¿quién puede ver los NULL? (todos con `ecommerce.orders`, como hoy / solo quien ve todas las sucursales). Recomendado lo primero durante la transición.

---

## 7. Decisión 5 — Conversión pedido → venta

| Aspecto | Opción A (recomendada) | Opción B | Opción C |
|---|---|---|---|
| Sucursal de la venta | **La del pedido**; el `branch_id` del body se ignora/valida igual al del pedido; solo se acepta del body si el pedido es NULL (antiguo) | La del usuario siempre | La del pedido salvo override libre del cajero (como hoy) |
| Cajero en otra sucursal | **409 `BRANCH_MISMATCH`**: "Este pedido se despacha desde <X>; cambia de sucursal para atenderlo". No se convierte "a distancia" | Se convierte en la sucursal del cajero (el stock sale de otra sucursal que la mostrada: **viola (b)**) | Igual que B |
| Guarda de acceso (listado, detalle, estado, PDF, convertir) | `RecordReadable` para ver; `RecordWritable` (sucursal activa) para convertir/cambiar estado; "Todas"/ver-todas = lectura sí, escritura no (coherente con 3B/3C). Vendedor de S2 no ve pedidos de S1 (404) | Solo se filtra el listado | Sin guarda |
| Reasignación | `POST /ecommerce/orders/:id/assign-branch {branch_id, reason}`: permiso `ecommerce.manage` (o nuevo `ecommerce.assign_branch`); solo si no está convertido ni cancelado; destino activo, accesible al actor y que venda todos los productos con stock suficiente; guarda `branch_source='staff'`, `branch_assigned_by/at` y deja registro de la sucursal anterior (columna o tabla de bitácora liviana) | Edición libre | No permitida |
| Stock | Descuento de la sucursal del pedido vía `SaleService.Create` (`sale_service.go:245, 490-512, 890-909`), ya por sucursal | — | — |
| Doble conversión | `OnCreatedTx` marca el pedido **dentro** de la transacción de la venta con `UPDATE … WHERE id=? AND converted_sale_id IS NULL` y verifica `RowsAffected==1`, si no → rollback de la venta; opcional: clave de idempotencia `ecommerce-order:<id>` si el mecanismo de idempotencia de ventas la admite (verificar en implementación) | Como hoy (hueco) | — |

Serie: debe pertenecer a la sucursal del pedido (ya lo valida `docseries.ValidateForBranch`, `sale_service.go:295`); `ConvertOrderModal` deja de ofrecer selector de sucursal cuando el pedido la trae (la muestra en solo lectura) y filtra series por ella.
Si la sucursal del pedido se desactivó después: la conversión falla con mensaje claro y se ofrece reasignar.
**Recomendación: A completa.** Compatibilidad: 1 sola sucursal → la sucursal del pedido siempre coincide con la activa, ningún mensaje nuevo; frontends/bundles antiguos que envían `branch_id` siguen funcionando (si coincide con el del pedido; si no, 409 claro).
**Datos del dueño:** ¿quién puede reasignar y hasta qué estado? (propuesta: `ecommerce.manage`, solo `nuevo`/`atendido`); ¿el cajero que no está en la sucursal del pedido debe poder cambiar de sucursal él mismo, o solo un administrador?

---

## 8. Cambios de esquema (solo aditivos) — V180 tentativa

Archivo previsto: `pkg/database/tenantmigrations/v180_ecommerce_order_branch.go` (patrón de `v106_ecommerce_order_conversion.go`: structs privados + `HasTable`/`HasColumn` + `AddColumn`, idempotente; sin tabla → `nil`).

`tenant_ecommerce_orders`: `branch_id` (NULL, índice), `branch_source` varchar(20) NULL (`customer`/`store_default`/`staff`), `branch_assigned_by` NULL, `branch_assigned_at` NULL.
`tenant_ecommerce_settings`: `dispatch_branch_id` NULL, `branch_mode` varchar(20) NOT NULL default `'default'`, y (solo E4) `branches_json` text NULL y, si se aprobara 2B, `allow_stock_fallback` bool default false.
Datos: **rellenar `dispatch_branch_id` con la principal activa** (única tras V179) en cada tenant que tenga la fila de settings; **sin tocar ningún pedido**. Tenants de 1 sucursal: queda su única sucursal.
Estructuras Go: actualizar los dos structs en `migrations.go`; V180 es idempotente tras `ApplyBaselineSchema`.
Regla del proyecto: sin AutoMigrate en producción → **migrate-fleet V180 antes de desplegar el frontend** (incidente 09-sep-2026).
**Compatibilidad sin V180:** el código nuevo usa un guard cacheado `HasColumn` (como `productbranch.Ready`). Si el tenant no tiene V180 → ruta pública en modo previo (E0: sin sucursal, stock total) y log de aviso; así no hay "unknown column" ni pedidos sin sucursal por error, solo "aún no aislado". Es la única excepción a (a), temporal y limitada a tenants aún sin migrar.

---

## 9. Flujo del cliente, paso a paso (modo por defecto, 1A)

1. Abre la tienda → `GET /settings` trae `dispatch_branch {id,name}` (la tienda guarda en memoria el `branch_id` visto).
2. Catálogo: `GET /products|categories|price-bounds?branch_id=<visto>`; el servidor ignora cualquier valor no válido y filtra por la sucursal vigente: ve solo lo vendible allí, con el stock y precio de esa sucursal.
3. Detalle/opciones de un producto: `GET /products/:id/options` (stock de la sucursal; 404 si no se vende allí).
4. Carrito: cantidades limitadas visualmente por el `stock_total` de esa sucursal (si `show_stock`); mensaje fijo: "Se despachará desde <sucursal>. La disponibilidad se confirma al atender".
5. *(Solo `customer_choice`, E4)* antes del paso 2 el cliente elige sucursal; al cambiarla con carrito lleno, el frontend **revalida o vacía el carrito** (`store/storeCart.ts`), porque stock y precio dependen de la sucursal.
6. Confirmar: `POST /orders` con `branch_id` visto. El servidor: resuelve sucursal → verifica que coincide (si no, 409 y recarga) → recalcula precios efectivos de esa sucursal → verifica visibilidad por producto → valida stock **solo de esa sucursal** (presentaciones y unidades de venta incluidas) → crea el pedido con `branch_id`, `branch_source`.
7. Respuesta: número de pedido, total, nombre de la sucursal y enlace al PDF firmado, que imprime esa sucursal.

---

## 10. Concurrencia de stock

Hoy: ni reserva ni descuento al pedir; se vuelve a validar y descontar al convertir (por sucursal). Dos riesgos distintos:

1. **Entre el pedido y la conversión** el stock puede agotarse (el cliente ya recibió confirmación).
2. **Carrera en el descuento** (dos ventas simultáneas leen el mismo saldo), preexistente en toda la plataforma (`RecordMovementTx`).

| Opción | Qué es | Costo | Efecto |
|---|---|---|---|
| **C1. Sin reserva (mínimo)** | Validar por sucursal al pedir y otra vez al convertir; informar "disponibilidad se confirma al atender" | Bajo | Posible pedido que no se puede atender; el cajero reasigna o contacta al cliente |
| **C2. C1 + descuento atómico** | Cambiar el descuento a `UPDATE … SET quantity = quantity - ? WHERE … AND quantity >= ?` (o `FOR UPDATE`) con verificación de filas | Medio; **toca todo el sistema de ventas**, requiere prueba de concurrencia | Elimina saldos negativos por carrera |
| **C3. Reserva con vencimiento** | Tabla de reservas por pedido/sucursal con TTL; el stock mostrado resta lo reservado | Alto (limpieza, estados, UX) | Casi elimina sobreventa online; innecesario si el volumen es bajo |

**Recomendación mínima: C1 en E2/E3 y programar C2 como cambio independiente** (con test de concurrencia que hoy probablemente falle), porque protege también al POS. C3 solo si el informe muestra sobreventa real.
**Dato del dueño:** ¿se aprueba tocar el descuento de stock global (C2) o solo se mide primero?

---

## 11. Plan de pruebas

Backend (SQLite como `handler/branch_isolation_test.go`; migraciones como `tenantmigrations/*_test.go`):

* **Migración V180:** crea columnas; idempotente (2 corridas); no modifica pedidos; sin tabla no falla; rellena `dispatch_branch_id` con la principal y no pisa uno ya configurado.
* **Resolución de sucursal:** tabla de casos (default válido; NULL → principal; configurada inactiva → bloqueo; sin sucursales activas → bloqueo; `branch_id` manipulado en el cuerpo/consulta; `BRANCH_CHANGED`; cliente antiguo sin `branch_id`).
* **Invariante (a):** hook `BeforeCreate` rechaza alta sin sucursal; ningún camino de `POST /orders` crea pedido con `branch_id` NULL/0 (incluye reintentos y entradas inválidas).
* **Aislamiento de stock (b):** dos sucursales con stocks distintos: `/products` devuelve solo el stock de la despacho; `options` también; pedido de 5 con 3+3 repartido → rechazado; ningún campo ni nombre de otra sucursal en el JSON (aserción de esquema, incluida `stock_by_presentation`); extiende `TestPublicProducts_DoesNotExposeBranchStockOrSerials` (`public_stock_exposure_test.go`).
* **Visibilidad y precio:** producto restringido a S2 no aparece, no se puede pedir, 404 en `options`; override de precio de S2 no aparece en S1; categorías y `price-bounds` coherentes.
* **Tenant con 1 sucursal:** salida del catálogo idéntica a la actual (mismos campos y valores), pedido guarda esa sucursal, modal de conversión sin cambios, ningún mensaje nuevo.
* **Tenant sin V180:** ruta pública funciona en modo previo, sin error de columna; log de aviso.
* **Conversión:** usa la sucursal del pedido; cajero de otra sucursal → 409/404; `branch_id` del body distinto del pedido rechazado; pedido NULL conserva flujo actual; el movimiento de stock queda en la sucursal del pedido; serie de otra sucursal rechazada; **doble conversión** (dos solicitudes concurrentes) → una sola venta y un solo descuento.
* **Reasignación:** permiso, estados permitidos, destino sin stock/visibilidad rechazado, auditoría escrita.
* **Listado/estado/PDF:** vendedor S1 no ve ni modifica pedidos de S2; "Todas" solo lectura; NULL visibles según la regla elegida; el PDF imprime la sucursal del pedido.
* **Concurrencia:** N goroutines pidiendo/convirtiendo el mismo producto con stock 1 en una sucursal; hoy probable fallo (hallazgo a corregir con C2).
* **Regresión:** `ListReport` con `BranchID>0` para POS/Tukichef sin cambios (pruebas de `product_service`).

Frontend (`frontend_tenant`): la tienda no pierde stock/precio; revalidación de carrito en 409 y (E4) en cambio de sucursal; `ConvertOrderModal` sin selector si el pedido trae sucursal; `PedidosWebPage` muestra sucursal y etiqueta "Sin sucursal (anterior)"; ajuste "Sucursal de la tienda" oculto en tenants de 1 sucursal.
En vivo (MySQL, tenant demo, 2 sucursales): pedido desde la tienda, conversión con cajero de la misma y de otra sucursal, ALL_BRANCHES.

---

## 12. Orden de despliegue y rollback

**Orden (condicionado a que el lote del aislamiento por sucursal V172–V179 ya esté desplegado):**
1. Backup de BD central y muestra de tenants.
2. **Informe de pedidos existentes** (solo lectura) y decisión del dueño sobre la sucursal de despacho por tenant con varias sucursales.
3. **migrate-fleet V180** (aditiva; rellena `dispatch_branch_id` con la principal). Como el código es compatible con tenants sin V180, el orden backend/migración es indistinto, pero lo seguro es **V180 antes del frontend**.
4. **Backend** (catálogo por sucursal, pedido con sucursal, guardas admin, conversión). Compatible con tenants sin V180 (modo previo).
5. **Frontend Tukifac** (ajuste de sucursal de la tienda, columna sucursal, modal sin selector, manejo de 409). Nunca antes de V180.
6. Aviso al admin de cada tienda con varias sucursales para **confirmar** la sucursal de despacho (el banner de V180).
7. E4 (`customer_choice`) solo donde se pida.

**Rollback:** todo aditivo (columnas NULL/default); basta volver a la imagen/build anterior: las columnas se ignoran. Los pedidos ya creados con sucursal siguen siendo válidos. No se recomienda un interruptor que vuelva a permitir pedidos con `branch_id` NULL (rompería (a) sin que nadie lo note); el rollback es el binario anterior. Las conversiones hechas con la lógica nueva no requieren revertir nada. La reasignación y los `branch_source` quedan como histórico.

## 13. Esfuerzo por fase

| Fase | Contenido | Esfuerzo | Riesgo |
|---|---|---|---|
| **E0** | Hecho (`7aaecb6`): sin `stock_by_branch`/series, sin stock por presentación con `show_stock=false` | — | — |
| **E0b** | Guardas de lectura/escritura en listado, `print-data` y estado de pedidos (según §7, con NULL visibles a `ecommerce.orders`) — **depende de V180** | 0,5 día | Bajo |
| **E1** | V180 + structs + guard `HasColumn` + `resolvePublicBranch` + hook de invariante + ajuste "Sucursal de la tienda" + informe de pedidos existentes | 2 días | Bajo-medio |
| **E2** | Catálogo público por sucursal (proyección pública propia, visibilidad, precio, stock, categorías, price-bounds, options) + `validateOrderStock` por sucursal + `branch_id` en `POST /orders` + `BRANCH_CHANGED` | 3 días | Medio |
| **E3** | Conversión desde la sucursal del pedido, `BRANCH_MISMATCH`, reasignación con auditoría, marcado atómico con `OnCreatedTx`, PDF con la sucursal del pedido, UI de admin | 2 días | Medio |
| **E4** | `customer_choice` (selector, lista opt-in, revalidación de carrito) y sugerencia 2C | 2–3 días | Medio (opcional) |
| **C2** | Descuento atómico en `RecordMovementTx` + prueba de concurrencia (afecta todo el sistema) | 1–2 días | Medio-alto |

Total E0b–E3 ≈ 7,5 días; con E4 y C2 ≈ 11–13 días.

## 14. Riesgos principales

* Tiendas con stock repartido verán "Agotado" donde hoy ven disponible (es el efecto buscado; hay que avisarlo).
* Tienda sin sucursal confirmada despacha desde la principal (aviso en el panel).
* Carrera de stock preexistente (§10).
* Scope GORM no aplica a rutas públicas: la sucursal debe filtrarse **explícitamente** en cada consulta (no registrar `TenantEcommerceOrder` en `BranchScopedModels` en esta etapa, para no romper el alta pública; usar guardas de handler).
* Cachés: no declarar caché pública en los endpoints JSON (dependen de la sucursal resuelta).

---

## 15. Tabla de decisiones

| # | Decisión | Recomendación | Qué cambia si eliges otra |
|---|---|---|---|
| 1 | Cómo se determina la sucursal | **1A** sucursal por defecto de la tienda (V180 la rellena con la principal); `customer_choice` solo en E4 y bajo demanda; "la que tenga stock" descartada | **1B** como modo base: +2-3 días (E4 antes), lista opt-in y selector, carrito revalidable. **1C** rompe (b): el catálogo no puede mostrar stock de una sucursal que aún no se decidió |
| 2 | Fallback por stock | **2A desactivado** (estricto); **2C** sugerencia solo dentro de `customer_choice` | **2B automático**: catálogo y servidor discrepan (muestra Agotado pero acepta), más soporte; requiere `allow_stock_fallback` y mostrar al cliente la sucursal asignada |
| 3 | Contrato público | `stock_total` y precio **solo de la sucursal**; productos no vendibles allí **ocultos** (404 en options); solo se nombra la sucursal de despacho; proyección pública propia | Mostrar "no disponible en tu sucursal" revela existencia y confunde; devolver stock agregado mantiene la fuga lógica de (b) |
| 4 | Pedidos existentes | **NULL, informe de solo lectura, asignación explícita pedido por pedido** (lote con confirmación solo si hace falta); NULL visibles a `ecommerce.orders` y convertibles como hoy | Backfill automático: riesgo de asignar mal (se descarta por regla del dueño); ocultarlos: pedidos vigentes desaparecen del panel |
| 5 | Conversión | **Sucursal del pedido**, guarda de acceso, `409` si el cajero está en otra sucursal, reasignación explícita con auditoría, marcado atómico con `OnCreatedTx` | Permitir que el cajero elija libremente: el stock saldría de una sucursal distinta a la mostrada al cliente (viola (b)); sin atomicidad: riesgo de doble venta |
| — | Tienda sin sucursal configurada | **Mixta (C)**: V180 rellena con la principal; NULL → principal + aviso; configurada pero inactiva → bloquear con aviso | Bloquear siempre: tiendas dejan de vender el día del deploy; usar siempre la principal: cambio silencioso al desactivar un local |
| — | Concurrencia | **C1** (validar por sucursal al pedir y al convertir, sin reserva) + **C2** atómico como cambio aparte | Sin C2: sobreventa posible por carrera; C3 reserva: +esfuerzo alto (tabla, TTL, UX) |
| — | Invariante `branch_id` | Resolución servidor + `CreateOrder` + `BeforeCreate` + prueba de contrato; `NOT NULL` real solo tras asignar/archivar los antiguos | Solo validación en handler: otro camino de alta podría crear pedidos sin sucursal |

**Datos que necesito del dueño:** (1) ¿V180 puede rellenar la sucursal de despacho con la principal y avisar para confirmar? (2) ¿Hay clientes que exijan elegir sucursal ya? (3) ¿Prefiere perder la venta antes que despachar desde otra sucursal? (4) ¿Quién ve los pedidos sin sucursal y quién puede reasignar? (5) ¿Aprueba tocar el descuento de stock global (C2) o solo medir? (6) ¿Qué datos de la sucursal ve el público (nombre, dirección, teléfono)?
