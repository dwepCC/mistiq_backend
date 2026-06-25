# Tukifac ERP — API Panel Central (Super Admin)

> **Base URL:** `https://tu-dominio.com`
> **Autenticación:** `Authorization: Bearer <token>` (excepto login)
> **Content-Type:** `application/json`

---

## Autenticación

### POST `/api/superadmin/login`

Genera un token JWT para el Super Admin.

**Request body:**
```json
{
  "email": "admin@tukifac.com",
  "password": "secreto123"
}
```

**Response 200:**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires_in": 28800,
  "user": {
    "id": 1,
    "email": "admin@tukifac.com",
    "role": "superadmin"
  }
}
```

**Response 401:**
```json
{ "error": "Credenciales inválidas" }
```

> El token expira en **8 horas** (28800 segundos).
> Envía el token en todas las peticiones posteriores: `Authorization: Bearer <token>`

---

## Tenants (Empresas)

### GET `/api/superadmin/tenants`

Lista todos los tenants. Soporta filtros opcionales.

**Query params:**
| Parámetro | Tipo | Descripción |
|-----------|------|-------------|
| `q` | string | Búsqueda por nombre o slug |
| `status` | string | `active` \| `suspended` \| `inactive` |

**Response 200:**
```json
{
  "data": [
    {
      "id": 1,
      "name": "Mi Empresa SAC",
      "slug": "mi-empresa",
      "email": "contacto@mi-empresa.com",
      "phone": "999000111",
      "plan": "pro",
      "status": "active",
      "db_name": "saas_tenant_mi-empresa",
      "created_at": "2025-01-15T10:00:00Z"
    }
  ]
}
```

---

### GET `/api/superadmin/tenants/:id`

Retorna el detalle de un tenant.

**Response 200:**
```json
{
  "id": 1,
  "name": "Mi Empresa SAC",
  "slug": "mi-empresa",
  "email": "contacto@mi-empresa.com",
  "phone": "999000111",
  "plan": "pro",
  "status": "active",
  "db_name": "saas_tenant_mi-empresa",
  "created_at": "2025-01-15T10:00:00Z",
  "updated_at": "2025-03-01T09:30:00Z"
}
```

**Response 404:**
```json
{ "error": "Tenant no encontrado" }
```

---

### POST `/api/superadmin/tenants`

Crea un nuevo tenant y provisiona su base de datos.

**Request body:**
```json
{
  "name": "Restaurante El Sol SAC",
  "slug": "restaurante-el-sol",
  "email": "admin@restaurante-el-sol.com",
  "phone": "987654321",
  "plan": "starter",
  "admin_email": "admin@restaurante-el-sol.com",
  "admin_password": "Pass1234!"
}
```

**Campos requeridos:** `name`, `slug`, `email`, `admin_email`, `admin_password`

**Response 201:**
```json
{
  "success": true,
  "data": {
    "id": 5,
    "name": "Restaurante El Sol SAC",
    "slug": "restaurante-el-sol",
    "status": "active",
    "db_name": "saas_tenant_restaurante-el-sol"
  }
}
```

**Response 400:**
```json
{ "error": "el slug ya existe" }
```

> Al crear un tenant se ejecutan automáticamente todas las migraciones GORM y se inserta la data semilla (sucursal principal, series por defecto, rol Administrador, usuario admin).

---

### PUT `/api/superadmin/tenants/:id`

Actualiza los datos de un tenant.

**Request body:**
```json
{
  "name": "Restaurante El Sol EIRL",
  "email": "nuevo@restaurante-el-sol.com",
  "phone": "955444333",
  "plan": "pro"
}
```

**Response 200:**
```json
{ "success": true }
```

---

### PATCH `/api/superadmin/tenants/:id/status`

Cambia el estado de un tenant.

**Request body:**
```json
{ "status": "suspended" }
```

**Valores válidos:** `active` | `suspended` | `inactive`

**Response 200:**
```json
{ "success": true }
```

> Un tenant con `status = "suspended"` no puede autenticar usuarios (el middleware `TenantAuthAPI` rechaza peticiones con HTTP 401).

---

## Módulos por Tenant

### POST `/superadmin/tenants/:id/modules` *(requiere cookie sa_token)*

Activa o desactiva un módulo para un tenant específico.

**Request body:**
```json
{
  "module_key": "restaurant",
  "enabled": true
}
```

**Módulos disponibles:**
| module_key | Nombre |
|------------|--------|
| `inventory` | Inventario |
| `purchases` | Compras |
| `products` | Productos |
| `billing` | Facturación Electrónica |
| `restaurant` | Restaurante |
| `ecommerce` | Ecommerce |

**Response 200:**
```json
{ "success": true }
```

---

## Reglas de Negocio

1. **Slug único:** El slug identifica al tenant y se usa como subdominio. Debe ser único, sin espacios, letras minúsculas y guiones.
2. **Provisioning automático:** Al crear un tenant se crea la base de datos `saas_tenant_{slug}` y se ejecutan las migraciones GORM automáticamente.
3. **Tenant suspendido:** Los usuarios del tenant no pueden iniciar sesión ni consumir la API si el tenant está suspendido.
4. **Módulos:** Cada tenant puede tener habilitados o deshabilitados módulos específicos. El frontend debe consultar los módulos activos para mostrar la navegación correcta.

---

## Errores comunes

| HTTP | Significado |
|------|-------------|
| 400 | Datos de entrada inválidos |
| 401 | Token faltante, inválido o expirado |
| 403 | No tiene permiso (rol insuficiente) |
| 404 | Recurso no encontrado |
| 500 | Error interno del servidor |

---

## Flujo típico de integración

```
1. POST /api/superadmin/login           → obtener token
2. GET  /api/superadmin/tenants         → listar empresas
3. POST /api/superadmin/tenants         → crear nueva empresa
4. PATCH /api/superadmin/tenants/:id/status → suspender/activar
5. POST /superadmin/tenants/:id/modules → habilitar módulo restaurant
```
