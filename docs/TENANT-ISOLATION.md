# Aislamiento multi-tenant — Web + App móvil/desktop

## Flujos soportados

### Web (panel tenant SPA)

```
https://demo.tukifac.com
        ↓ same-origin
https://demo.tukifac.com/api/*
        ↓ nginx proxy (Host: demo.tukifac.com)
backend Go :3000
```

- Frontend tenant **no** debe apuntar rutas autenticadas a `api.bendey.cloud`.
- Resolver: `frontend_tenant/src/config/apiBaseUrl.ts`.
- `X-Tenant-Slug` **ignorado** para resolver (solo validación si se envía).
- Header ≠ subdominio → **403** `TENANT_ISOLATION_VIOLATION`.

### Panel central

```
https://app.tukifac.com → https://api.bendey.cloud/api/*
```

(Superadmin; frontend en `frontend_central`.)

### Tukichef (Android / Tauri)

```
1. RUC → GET api.bendey.cloud/api/public/tenant-by-ruc
2. Respuesta: { slug, api_url: "https://empresa1.tukifac.com", subdomain, tenant_version }
3. Guardar api_url en localStorage (tenantApiUrl)
4. Login y API → https://empresa1.tukifac.com/api/*
5. Header X-Tenant-Slug: empresa1 (redundancia; debe coincidir con host)
```

### Dev localhost

```
X-Tenant-Slug o cookie dev_tenant
```

## Validación backend (cadena)

| Capa | Qué valida |
|------|------------|
| TenantResolver | Host → slug; mismatch header en prod |
| TenantAuthAPI | JWT con tenant_id, tenant_slug, tenant_db, tenant_version ≥ 1 |
| ValidateTenantBinding | host slug = JWT slug = tenant DB = tenant_id |

## JWT

Tokens nuevos incluyen:

```json
{
  "tenant_id": 45,
  "tenant_slug": "empresa1",
  "tenant_db": "saas_tenant_empresa1",
  "tenant_version": 1
}
```

Tokens sin `tenant_id` o `tenant_version` (prod) → **401** `TOKEN_TENANT_INVALID`.

## Redis

Prefijo: `tukifac:tenant:{slug}:*`

Invalidación: `InvalidateTenantCache(slug)` tras cambios de plan/permisos/suspensión.

## Checklist post-deploy

1. **Forced relogin** — usuarios con tokens viejos deben volver a login.
2. **Purge Redis selectiva** — `SCAN tukifac:tenant:{slug}:*` si hubo incidente.
3. **Monitoreo** — alertas en logs `tenant_security_violation`.
4. **Postman** — JWT tenant A + Host empresa2 → 403.
5. **App** — verificar que peticiones van a `https://{slug}.tukifac.com`, no solo a `api.bendey.cloud`.
6. **Nginx** — wildcard `*.tukifac.com`: SPA en `/`, proxy `/api` al Go con `Host` preservado (ver abajo).

## Nginx Proxy Manager (producción tukifac.com)

Tres proxy hosts (orden de especificidad):

| Host | Destino | Notas |
|------|---------|-------|
| `api.bendey.cloud` | backend Go `:3000` | Panel central / superadmin / bootstrap |
| `app.tukifac.com` | SPA `frontend_central` | Sin proxy `/api` al Go |
| `*.tukifac.com` | SPA tenant + proxy `/api` | Excluir `api` y `app` con hosts dedicados |

**Custom location** en wildcard tenant (`demo.tukifac.com`, etc.):

```nginx
location /api/ {
    proxy_pass http://127.0.0.1:3000;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Origin $http_origin;
}

# Imágenes y archivos tenant (sin esto, /uploads cae en index.html de la SPA → login)
location /uploads/ {
    proxy_pass http://127.0.0.1:3000;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}

location /storage/ {
    proxy_pass http://127.0.0.1:3000;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

**Alternativa (recomendada si no quieres proxy /uploads en cada subdominio tenant):** el frontend resuelve imágenes contra `https://api.bendey.cloud/uploads/...` (host API). Las URLs en BD siguen siendo relativas (`/uploads/tenants/{RUC}/...`).

Crítico: `proxy_set_header Host $host` para que el backend reciba `demo.tukifac.com` y resuelva `subdomain=demo`.

Validación en logs tras deploy:

```json
{ "host": "demo.tukifac.com", "subdomain": "demo" }
```

Sin `missing_resolved_tenant` ni 403 en `/api/session/context`.

## Tests

```bash
go test ./pkg/middleware/... -count=1
# Linux CI:
go test -race ./pkg/middleware/...
```

## Riesgos residuales

- `/uploads/*` sin auth (archivos por URL).
- Login legacy en `api.bendey.cloud` + header (deprecado; log `tenant_resolve_central_host_header`).
