# Propuesta: elegir entre cámara y lector físico de códigos de barras (Android)

Fecha: 19-oct-2026 · Estado: **solo análisis y propuesta, nada implementado** · Alcance: `frontend_tenant` (Tukifac web / Capacitor Android / Tauri) y `frontend_restaurant_tenant` (Tukichef).

> Las rutas y líneas citadas son del código en `main` a la fecha. No hay cambios de backend ni de base de datos en ninguna fase.

## 1. Respuesta corta

**Sí se puede, y una parte ya existe.** Un lector Bluetooth HID o USB/OTG se comporta como un teclado: «escribe» el código y manda Enter. Tukifac ya tiene un modo «lector físico» en el **POS** y en el **formulario de producto** del tenant, accesible con un botón aparte del de cámara. Lo que falta:

1. **No es una preferencia**: hay que volver a pulsar «lector físico» cada vez; no se recuerda por dispositivo.
2. La **cámara es el modo por defecto** en toda la app Android; en varias pantallas no hay ni botón de lector físico.
3. **Tukichef** (restaurante) no tiene opción de lector físico en Android y además reabre la cámara sola al entrar al POS.
4. El campo de captura **no es robusto**: se deshabilita mientras procesa (pierde el foco), no recupera el foco, abre el teclado virtual, y no distingue ráfaga de tecleo humano.

Lo que **no depende del software**: que el lector esté en modo HID (no SPP), que mande Enter como sufijo, y que Android oculte el teclado en pantalla (ajuste del sistema).

## 2. Dónde se usa hoy el escáner (evidencia)

Librería: `html5-qrcode` (`frontend_tenant/package.json:40`), que usa `getUserMedia` dentro del WebView; no hay plugin nativo (ML Kit, etc.). Permiso de cámara vía `@capacitor/camera` (`frontend_tenant/src/lib/camera/requestCameraPermission.ts:7-14`).

Detección de plataforma: `isCapacitorNative()` (`src/lib/platform/detect.ts:17`) → **true solo en la app Android/iOS empaquetada**. Todos los hooks deciden «cámara» con esa condición (`useBarcodeProductScanner.ts:34`, `useBarcodeFieldScanner.ts:18`). Consecuencia: en **web (incluido Chrome en Android) y Tauri el modo es siempre «lector físico/campo de texto»**; la cámara solo existe en la app Capacitor.

| Pantalla | Archivo | Cámara | Botón «lector físico» | Qué hace el resultado |
|---|---|---|---|---|
| POS (tenant) | `pages/pos/POSPage.tsx:497-505, 1079-1114, 1578-1587` | Sí, `variant="compact"`, no se cierra tras escanear | **Sí** (`:1099-1114`, solo si es Capacitor) | Busca en la lista cargada y si no en `GET /api/products/lookup-by-code`; `addToCart` |
| Registro de venta | `pages/sales/SalesRegisterPage.tsx:948-952, 2596-2655, 3528` | Sí, pantalla completa, se cierra tras escanear | **No** (solo `toggleScannerMode`) | Igual; `addProductToItems` |
| Formulario de producto | `pages/products/ProductsPage.tsx:255-260, 1878-1956, 2799` | Sí | **Sí** (`:1929-1945`) | Rellena el campo «Código (barras)» |
| Tukichef POS | `frontend_restaurant_tenant/src/pages/POSPage.tsx:119-127, 213-218, 982-1042, 1574-1641, 2327` | Sí (si Capacitor) | **No** | Igual; `addProduct` |
| Tukichef productos | `frontend_restaurant_tenant/src/pages/ProductosPage.tsx:1275-1295, 1798` | Sí, botón solo en Capacitor | n/a (el campo acepta tecleo directo) | Rellena «Código (barras)» |
| Selector de producto (Ventas, Compras, Ajustes de inventario, Notas, Guías) | `components/sales/ProductPickerModal.tsx:98-109` | No | No hay modo escáner | Es una búsqueda de texto: sin autoenfoque ni manejo de Enter |
| Transferencias | `pages/inventory/InventoryTransfersPage.tsx:433` | No | No | Búsqueda de texto con Enter, sin lógica de escáner |
| Importación de ajuste de stock / precios | `utils/inventoryAdjustmentImport.ts`, `productPriceUpdateImport.ts` | No | No | Es Excel, no escaneo |

No hay escaneo en: kardex, cotizaciones (reutiliza el registro de venta), toma de stock, catálogo de producto salvo lo anterior.

Backend: `GET /api/products/lookup-by-code` (`internal/products/routes.go:24`, `handler/product_handler.go:682-695`) hace coincidencia exacta con variantes EAN-13/UPC-A. El front también normaliza caracteres de control (`utils/barcodeLookup.ts:1-21`). **No hace falta tocar el backend.**

## 3. ¿Ya hay soporte para lector físico?

**Sí, parcial y frágil.**

Lo que funciona:
- Con el modo escáner activo, Enter en el campo dispara la búsqueda (`useBarcodeProductScanner.ts:162-170`, `useBarcodeFieldScanner.ts:67-76`).
- Tras agregar, se vuelve a enfocar el campo solo en modo físico (`useBarcodeProductScanner.ts:80-82`).
- Botón «Usar lector físico» en POS y producto, solo en Capacitor (`POSPage.tsx:1099-1114`; `ProductsPage.tsx:1929-1945`), vía `activatePhysicalScanner` (`useBarcodeProductScanner.ts:143-148`).

Lo que estorba o falta:
1. **El campo se deshabilita al procesar**: `disabled={barcodeScan.scanProcessing}` (`POSPage.tsx:1073`; `SalesRegisterPage.tsx:2633`; Tukichef `POSPage.tsx:1598`). Un input deshabilitado pierde el foco; si el lector manda la siguiente lectura durante esos ms, **se pierde o cae en otro campo**. La recuperación del foco es manual (`focusScanInput`) y no cubre el caso «producto con presentaciones» que abre un modal (`POSPage.tsx:488-492`).
2. **Se pisan los modos**: `toggleScannerMode` siempre pone `forcePhysicalScanner=false` (`useBarcodeProductScanner.ts:119-136`); es decir, tocar el botón de cámara tras elegir lector reabre la cámara. No se persiste nada.
3. **Teclado virtual**: ningún campo de captura usa `inputMode="none"` (el único `inputMode` del repo es numérico/decimal en campos de cantidad/precio). Al enfocar el campo, Android puede abrir el teclado y tapar el POS.
4. **Sin detección de ráfaga**: se depende 100 % de Enter. Si el lector está configurado con sufijo Tab, sin sufijo, o con prefijo, no funciona (el prefijo se enviaría al buscar). Solo se limpian caracteres de control (`barcodeLookup.ts:3`).
5. **Sin atajos globales**: no hay ningún listener `keydown` global de escaneo (los `keydown` del repo son de cerrar modales y de inactividad, `components/ui/Modal.tsx:71`, `hooks/useIdleLogout.ts:17`). Si el foco está en otro lado, el lector «escribe» en ese campo.
6. **Búsqueda sin debounce en el selector** (`ProductPickerModal.tsx:69-71, 105-108`): cada carácter del lector dispara una petición; sin Enter ni autoenfoque. Funciona «por casualidad» si el campo ya tiene foco.
7. **Tukichef reabre la cámara solo**: el modo escáner se persiste en `localStorage` (`POSPage.tsx:119-127, 982-986`) y en Capacitor el efecto de `:992-994` abre la cámara al entrar al POS. No hay alternativa de lector físico en Android.
8. **Tukichef y tenant duplican** `PosBarcodeScannerModal` / `BarcodeScannerModal`, y la copia de Tukichef usa `busy` directo en el callback (`PosBarcodeScannerModal.tsx:116`), sin el fix de refs del tenant (`BarcodeScannerModal.tsx:56-69`). Riesgo de doble lectura en Tukichef.

## 4. Android / WebView específicamente

Evidencia de `frontend_tenant/android` y config:
- `capacitor.config.ts:22-25`: `Keyboard.resize: 'body'`; `AndroidManifest.xml:11`: `configChanges` incluye `keyboard|keyboardHidden|navigation`, por lo que **conectar o desconectar un teclado/lector no recrea la actividad** (bien). No hay `windowSoftInputMode` declarado (grep en `android/app/src`: sin resultados).
- Orientación: `screenOrientation="portrait"` en el manifest y `ScreenOrientation.lock('portrait')` en `orientationPolicy.ts:13` (con `unlock` en tablet, `:11`).
- `minWebViewVersion: 60` (`capacitor.config.ts:10`): `inputMode="none"` requiere WebView ≥ 66; en versiones anteriores se ignora (degradación aceptable).
- Permisos: ya existen `BLUETOOTH*` (para la ticketera, `AndroidManifest.xml:39-49`). **Un lector HID no requiere ningún permiso de la app**: Android lo trata como teclado del sistema. No se necesita plugin ni cambio nativo.

Qué es de Tukifac y qué del sistema/lector:

| Aspecto | Dueño |
|---|---|
| Que el campo reciba foco y procese Enter | **Tukifac** |
| Que el teclado virtual no aparezca | **Android** (ajuste «Teclado físico → Mostrar teclado en pantalla»). Tukifac solo puede ayudar con `inputMode="none"` y blur/refocus |
| Modo del lector: HID vs SPP | **Lector** (hardware/configuración; SPP no es un teclado y no llega al WebView) |
| Sufijo Enter/Tab, prefijo, idioma del teclado (el lector escribe con layout US; con teclado Android en español puede alterar símbolos) | **Lector** y **Android** |
| Emparejado Bluetooth, adaptador OTG, energía del puerto | **Equipo del cliente** |

## 5. Diseño propuesto

Principio: **un solo mecanismo, fuente de verdad única, por dispositivo.**

### 5.1 Preferencia por dispositivo
- Clave en `localStorage`: `tukifac.scanner.mode` ∈ `camera` | `reader` | `auto`; **no** va al backend ni a BD (es propia del dispositivo; la misma empresa puede tener un celular con cámara y una tablet con lector). Envuelta en `try/catch`, como ya hace Tukichef (`POSPage.tsx:122-126`).
- Valor por defecto: `auto` = comportamiento actual (Capacitor → cámara; web/Tauri → lector) para no cambiar nada a quien no configure.
- `auto` mejorado (opcional): si se detecta una ráfaga de teclas (ver 5.3) en una pantalla de ventas, ofrecer «¿Cambiar a lector físico?» una vez.
- Un selector visible en **Ajustes del dispositivo** (nueva tarjeta «Escáner de códigos») y un conmutador compacto en POS/Venta (reemplaza el par de botones actual cámara/teclado). Los botones actuales pasan a ser un solo control tipo ciclo `Cámara ⇄ Lector`.

### 5.2 Hook unificado
Reemplazar `useBarcodeProductScanner` y `useBarcodeFieldScanner` por una base común `useBarcodeInput({ onCode, mode })` + `getScannerMode()`; ambos hooks actuales quedan como envoltorios finos para no tocar a los consumidores. Mismo código para tenant y Tukichef (hoy duplicado).
- Modo `reader`: nunca se abre la cámara ni se pide permiso; la cámara solo se abre desde su botón explícito.
- Modo `camera`: igual que hoy.
- **Campo de captura tolerante**: el input no se deshabilita nunca (`readOnly` + estado visual de «procesando» en lugar de `disabled`), se reenfoca tras cada lectura, al cerrar modales y al volver a la pestaña (`visibilitychange`), con `inputMode="none"` + `enterKeyHint`. Si la pantalla permite escribir manualmente, un botón «⌨ teclado» alterna `inputMode` a `text` para tecleo humano.
- **Cola de lecturas**: si llega una lectura mientras se procesa otra, encolar (máx. ~5) en vez de descartar o perder.
- **Doble lectura**: ignorar el mismo código dentro de una ventana configurable (por defecto 400 ms para lector; la cámara ya tiene 1800 ms, `BarcodeScannerModal.tsx:20`). Un segundo escaneo intencional del mismo producto a los pocos segundos sí cuenta (suma 1).

### 5.3 Ráfaga frente a tecleo humano
En el campo de captura, medir el intervalo entre `keydown`: ráfaga = ≥ 6 caracteres con ≤ 35 ms entre teclas y terminada en Enter (o Tab, o silencio > 80 ms). Esto permite:
- Aceptar lectores **sin sufijo Enter** (cierre por silencio) o con **Tab**.
- No ejecutar la búsqueda por Enter si el usuario escribió a mano (en modo escáner, hoy un Enter manual también busca; se mantendría ese comportamiento, y la ráfaga solo añade tolerancia).
- Constantes en un solo archivo, ajustables.

### 5.4 Prefijo y sufijo
Dos campos opcionales en el ajuste del dispositivo: «Prefijo a quitar» y «Sufijo adicional» (p. ej. algunos lectores envían `STX`/`ETX` o un prefijo de símbolo). Se aplican antes de `barcodeLookupCandidates`. Por defecto vacíos. Persisten en `localStorage` junto a la preferencia.

### 5.5 Retroalimentación
- Éxito: beep corto (ya existen sonidos de carrito, `playCartRemoveSound`; reutilizar el mecanismo) + `navigator.vibrate(30)` donde exista.
- No encontrado: vibración doble + `toast.error` (ya existe, `useBarcodeProductScanner.ts:84`), y **el campo se mantiene enfocado** con el código visible para corregir.
- Respetar el ajuste de sonidos del usuario si existe.

### 5.6 Selector de producto y otras pantallas
Pantallas con campo de búsqueda donde conviene aceptar lector sin activar nada: `ProductPickerModal` (Ventas/Compras/Inventario/Guías/Notas) y transferencias. Propuesta: autoenfoque al abrir, debounce de 250 ms, y **Enter con coincidencia exacta de código → agregar directamente** (reutilizando `lookupByBarcode`), sin cambiar el comportamiento de búsqueda por nombre.

### 5.7 Tukichef
Aplicar el mismo hook; **dejar de reabrir la cámara al entrar** (el modo persistido solo debe activar el campo, no la cámara, salvo que el modo del dispositivo sea `camera`), y añadir el botón/ajuste de lector físico.

### 5.8 Alternativas descartadas
- **Plugin nativo de captura (BroadcastReceiver / intent de SDK del fabricante)**: sirve para pistolas Zebra/Honeywell/Newland integradas con «DataWedge», que ya entregan como teclado o intent. Es esfuerzo alto, específico por marca, y la vía teclado ya las cubre. Solo se justifica si un cliente tiene un equipo concreto que no actúe como teclado.
- **Ajuste en BD por empresa**: no, la elección depende del equipo físico, no de la empresa.
- **Forzar a Android a ocultar el teclado**: no es posible de forma fiable desde el WebView.

## 6. Riesgos y límites (qué el software no puede garantizar)

1. Lector en **modo SPP/serial** (no HID): no escribe como teclado; no funciona sin un plugin Bluetooth serie (no existe en el proyecto, a diferencia de la ticketera). Habría que cambiar el lector a HID (normalmente se configura escaneando un código de su manual).
2. **Teclado virtual**: el comportamiento depende del ajuste de Android y del fabricante; `inputMode="none"` ayuda en la mayoría de WebViews modernos pero no está garantizado (WebView < 66, algunos teclados de terceros).
3. **Layout de teclado**: lectores con layout US sobre un Android configurado en otro idioma pueden alterar caracteres (`-`, `/`, `'`). Los EAN numéricos no se ven afectados; los Code 128/39 con letras o símbolos sí podrían.
4. **Latencia de emparejado Bluetooth** y desconexión por ahorro de energía del lector: tras reposo, la primera lectura puede perderse.
5. Lectores con **sufijo Tab**: sin el manejo de ráfagas (5.3), el Tab movería el foco al siguiente control.
6. Tablet con **teclado en pantalla y lector a la vez**: el comportamiento es el del fabricante.
7. En app Android el campo siempre necesita tener el foco; si el usuario toca fuera, las lecturas van a otro lado. Mitigación parcial con refoco, no total.

## 7. Plan de pruebas (con lector real)

Lo que **no se puede probar aquí**: ningún lector físico, ni Android real, ni el WebView del dispositivo; solo se pueden ejecutar pruebas unitarias del detector de ráfagas y de los hooks, y verificar en navegador de escritorio simulando teclas rápidas.

En un Android real:
- A. Lector **Bluetooth HID** + **USB/OTG HID**, con sufijo Enter, con sufijo Tab y sin sufijo (cada uno ya es una variante de configuración del lector).
- B. Celular (pantalla pequeña) y tablet (≥ 600 px de lado corto, `detect.ts:45-49`), vertical y horizontal.
- C. Por pantalla: POS, Registro de venta, Formulario de producto, Selector de producto (Compras / Inventario), Tukichef POS y productos.
- D. Casos: lectura rápida consecutiva del mismo producto (doble lectura vs. dos unidades), código inexistente, producto con presentaciones (abre modal y se pierde el foco), lectura durante el procesamiento, reposo y reconexión del lector, teclado virtual visible/oculto, cambio de modo sin cerrar la app, Chrome en Android (no es Capacitor) y Tauri.
- E. Códigos: EAN-13, EAN-8, UPC-A, Code 128 con letras, Code 39, QR.
- F. Regresión de cámara: el modo `auto` debe seguir abriendo cámara en Capacitor como hoy.

## 8. Mapa de impacto, fases y esfuerzo

| Fase | Contenido | Archivos | Esfuerzo |
|---|---|---|---|
| 1 | Preferencia por dispositivo + hook base con campo robusto (sin `disabled`, refoco, `inputMode="none"`, cola, anti-doble-lectura); botones POS/Venta/Producto con un único control cámara/lector | `hooks/useBarcode*`, `POSPage`, `SalesRegisterPage`, `ProductsPage`, nuevo `lib/scanner/*`, ajuste de dispositivo | 2–3 días |
| 2 | Detección de ráfaga (Tab / sin sufijo), prefijo/sufijo configurables, beep y vibración | hook base + ajuste | 1–2 días |
| 3 | Tukichef: mismo hook, dejar de reabrir la cámara, botón lector, unificar modal de cámara | `frontend_restaurant_tenant` | 1–2 días |
| 4 | Selector de producto: autoenfoque, debounce, Enter exacto; transferencias | `ProductPickerModal`, `InventoryTransfersPage` | 1 día |
| 5 | Pruebas con lectores reales y ajuste de constantes | — | 1–2 días (requiere hardware) |

Total estimado: 6–10 días. **Sin backend, sin migraciones, sin cambios nativos de Android.** Cada fase es entregable y reversible sola; la fase 1 resuelve el 80 % del problema.

## 9. Decisiones abiertas (respuesta sí/no o una opción)

1. ¿La elección es **por dispositivo** (en el navegador/app) y no por empresa ni por usuario? (recomendado: sí)
2. ¿El valor por defecto debe seguir siendo **Cámara en la app Android** (actual) o pasamos a **Auto con sugerencia de cambiar** cuando se detecte un lector? (recomendado: Auto con sugerencia)
3. ¿Se acepta **lector HID como único soporte** y se descarta SPP (lectores en SPP se cambian a HID)? (recomendado: sí)
4. ¿Incluimos **Tukichef** en la misma entrega? (recomendado: sí, fase 3)
5. ¿Queremos **prefijo/sufijo configurables** desde la primera entrega o solo si algún cliente lo pide? (recomendado: fase 2, bajo demanda)
6. ¿El **Enter exacto en el selector de producto** de Compras/Inventario debe agregar directamente el producto? (recomendado: sí)
7. ¿Tienes un **lector Bluetooth y uno USB/OTG** para las pruebas, o los provee un cliente piloto? ¿Qué marca/modelo?
