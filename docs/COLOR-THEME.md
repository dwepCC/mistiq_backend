# Sistema de colores — paleta corporativa y temas por tenant

Este documento es la referencia canónica de qué color va dónde. Si vas a tocar un
color en cualquiera de los frontends (`mistiq_tenant`, `mistiq_restaurant_tauri`,
`mistiq_central`) o en las plantillas server-side de este backend, empieza acá.

## 1. Las dos identidades de marca

El proyecto tiene **dos apps con paleta propia**, no relacionadas entre sí:

| App | Paleta | Uso |
|---|---|---|
| `mistiq_tenant` (panel ERP web, "Tukifac") | Navy / teal — **configurable por tenant** | Sidebar, botones primarios, header |
| `mistiq_restaurant_tauri` (POS restaurante, "Tukichef") | Rojo tomate `#c9393b` — **fija, no configurable** | Sidebar, nav POS, botones de acción |

`mistiq_central` (panel interno superadmin) no tiene paleta configurable; usa
colores Tailwind estándar hardcodeados (`blue-600`, `slate-*`) sin sistema de
temas.

## 2. Paleta corporativa Tukifac (`mistiq_tenant`) — tema por defecto

```
#8ecae6  accent / p300
#219ebc  secondary / p500
#023047  primary / p900 (sidebar)
#ffb703  warning
#fb8500  brand-orange
```

Esta es la paleta **por defecto** (`color_theme = "blue"`). El sistema soporta
más temas seleccionables por tenant (verde, violeta, esmeralda, rosa, ámbar,
gris) pero **ninguno de ellos reemplaza al default** — un tenant nuevo siempre
arranca en la paleta navy/teal de arriba salvo que la cambie explícitamente
desde su panel.

### Dónde vive cada pieza

- **Backend — dueño del dato**: `pkg/database/migrations.go`, columna
  `TenantCompanyConfig.ColorTheme` (`gorm:"default:'blue'"`). Se edita desde
  `internal/company/service/company_service.go`.
- **Backend — temas para vistas server-side** (PDFs, HTML renderizado con
  `PassLocalsToViews`): `pkg/utils/themes.go` (mapa `themes`, función
  `GetTheme`) + `pkg/middleware/theme.go` (`TenantTheme()` inyecta el tema
  activo como local `Theme`). Fallback si el tenant no tiene tema: `"blue"`.
- **Frontend tenant — picker en runtime**: `src/contexts/ThemeContext.tsx`
  define las paletas como CSS variables (`--p50`...`--p900`, `--sidebar-bg`) y
  las aplica a `document.documentElement` al cargar la app, leyendo
  `companyService.getConfig().color_theme` (default `"blue"` si no hay sesión
  o el tenant no configuró nada).
- **Frontend tenant — tokens Tailwind**: `tailwind.config.js`, token `primary`
  (apunta a las CSS vars, así cambia con el tema) + tokens estáticos que NO
  varían por tenant: `secondary`, `accent`, `warning`, `brand-orange`,
  `success`, `danger`.
- **Frontend tenant — valores por defecto antes de que cargue JS**:
  `src/index.css`, bloque `:root` — deben coincidir con el tema `"blue"` de
  `ThemeContext.tsx` para evitar parpadeo de color en el primer render.

Si agregás un tema nuevo, tocá los **tres** lugares a la vez: `themes.go`
(backend, para PDFs/vistas server-side), `ThemeContext.tsx` (frontend) y el
selector de tema en el panel de configuración del tenant.

## 3. Paleta Tukichef (`mistiq_restaurant_tauri`) — fija

```
rest-600  #c9393b   (rojo tomate del logo)
```

Escala completa (50–950) en `tailwind.config.js`, token `rest`. A diferencia
del tenant, **no hay picker**: todos los restaurantes usan el mismo rojo. No
existe `ThemeContext` en este repo ni columna `color_theme` que lo controle.

Usar siempre la clase `rest-*` (nunca un color Tailwind literal como
`green-*` o `red-*`) para cualquier elemento que represente la marca: nav
activo, botón de acción principal en POS/Caja, foco de inputs de login/PIN,
badges de estado "listo para servir".

## 4. Convención: color de marca vs. color semántico

Este es el punto que más se presta a confusión y el motivo de este documento.

**Color de marca** (`primary-*` / `rest-*`, según la app): identifica la
marca — sidebar, nav activo, botón principal, foco de formularios. Es lo que
cambia si el tenant elige otro tema (solo en `mistiq_tenant`).

**Color semántico** (Tailwind estándar: `green-*`, `red-*`, `amber-*`,
`blue-*`, etc.): comunica un estado — ingreso/positivo, error, pendiente,
info — y es **independiente de la marca**. Ejemplos que existen a propósito y
no deben "corregirse" a color de marca:

- Montos de ingreso en caja/reportes → `text-green-700`
- Badges "activo" / "aceptado" (SUNAT, clientes, métodos de pago) →
  `bg-green-100 text-green-700`
- Estado de orden "ready"/"delivered" en comandas → `green-*` / `emerald-*`
- Errores y cancelaciones → `red-*`

Antes de cambiar un `green-*` a `rest-*` o `primary-*` (o viceversa), verificá
si el elemento representa **la marca** (sidebar, botón principal, foco de
login) o **un estado** (dinero, activo/inactivo, aceptado/rechazado). Solo lo
primero debe seguir el token de marca.

## 5. Historial: por qué existe este documento

En junio–agosto 2026 se agregó el sistema de temas por tenant a
`mistiq_tenant` (antes la paleta navy/teal era fija, igual que en
`mistiq_restaurant_tauri`). Durante ese desarrollo el *default* del tema quedó
en verde (`color_theme = "green"`) en vez de mantenerse en la paleta navy/teal
original, y en `mistiq_restaurant_tauri` el token `rest` se reemplazó
temporalmente por la escala verde de Tailwind. Ambos se revirtieron a la
paleta original (este documento) sin tocar ninguna funcionalidad nueva del
mismo lote de cambios (ecommerce, cotizaciones, flota, guía de remisión,
etc.). El bug de compilación en `pkg/cron/saas_scheduler.go` (símbolos
duplicados con `expiration.go`) encontrado durante esta revisión también se
corrigió — no estaba relacionado con colores, pero impedía compilar el
backend.
