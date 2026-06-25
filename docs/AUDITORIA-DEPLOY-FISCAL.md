# Auditoría: deploy y sincronización fiscal

**Fecha:** 2026-05-26  
**Alcance:** Backend Go (`backend_principal`) + Facturador Lycet (`facturador_lycet`)  
**Regla de negocio:** Los datos de empresa solo se modifican por acción explícita del usuario. Ningún deploy, reinicio, migración, health check ni warmup debe alterar credenciales ni configuración fiscal de empresas existentes.

---

## 1. Resumen ejecutivo

| Área auditada | ¿Modifica datos en deploy? | Riesgo previo | Estado |
|---------------|----------------------------|---------------|--------|
| `deploy/scripts/deploy.sh` | No | Bajo | OK |
| `.github/workflows/deploy-production.yml` | No | Bajo | OK |
| `deploy/docker-entrypoint.sh` (Go) | No | Bajo | OK |
| `facturador_lycet/docker/docker-entrypoint.sh` | No | Bajo | OK |
| `composer/PostInstall.php` (Lycet) | Solo archivos `data/` si `LYCET_BETA=1` | Bajo | OK |
| `migrate-central` / `migrate-fleet` | Solo esquema ERP (salvo migraciones históricas con UPDATE) | Medio | Documentado |
| Migraciones Lycet `Version20260528/29` | UPDATE de metadatos fiscales legacy (una vez) | Medio | Histórico |
| **`SyncFiscalToFacturador` fallback MODDATOS** | Sí, en cada sync sin credenciales | **Crítico** | **Corregido** |
| **`PUT /api/company/config` sync automático** | Sí, en cada guardado con SUNAT activo | **Alto** | **Corregido** (solo con logo) |
| Workers / cron fiscal | No tocan `empresa` | Bajo | OK |
| `app:empresas:import-from-json` | Sí, si se ejecuta manualmente | Alto | Bloqueado en deploy estándar |

**Causa raíz del incidente `RUCMODDATOS`:** el backend aplicaba `solUser = cfg.RUC + "MODDATOS"` cuando la petición no traía usuario SOL, y Lycet sobrescribía `sol_user` en cada `company-sync`.

---

## 2. Hallazgos detallados

### 2.1 Crítico — Fallback `RUC+MODDATOS` en sync fiscal

| Campo | Valor |
|-------|-------|
| **Archivo** | `backend_principal/internal/company/service/fiscal_sync.go` |
| **Riesgo** | Crítico |
| **Comportamiento** | Cualquier `company-sync` sin `sol_user` en el body enviaba credenciales de prueba SUNAT y pisaba producción. |
| **Disparadores** | `PUT sunat-config`, `POST sync-facturador`, `PUT company/config` (con logo), guardado panel central sin reingresar SOL. |
| **Corrección** | Eliminado el fallback. `SOL_USER` / `SOL_PASS` solo se incluyen en el payload JSON si vienen explícitamente en la petición (`omitempty`). Lycet mantiene valores persistidos en actualizaciones parciales. |

### 2.2 Alto — Sync automático en `PUT /api/company/config`

| Campo | Valor |
|-------|-------|
| **Archivo** | `backend_principal/internal/company/handler/company_api.go` |
| **Riesgo** | Alto |
| **Comportamiento** | Cada guardado de datos de empresa (dirección, teléfono, etc.) con SUNAT habilitado disparaba `company-sync` hacia Lycet. Combinado con el fallback MODDATOS, reseteaba credenciales tras acciones no fiscales. |
| **Corrección** | Sync con Lycet **solo cuando hay logo en la petición** (acción explícita de actualizar logo). Guardar dirección/razón social ya no toca el facturador. |

### 2.3 Medio — `UpdateSunatConfig` siempre sincroniza

| Campo | Valor |
|-------|-------|
| **Archivo** | `backend_principal/internal/superadmin/handler/tenant_handler.go` |
| **Riesgo** | Medio (mitigado con fix 2.1) |
| **Comportamiento** | `PUT /api/superadmin/tenants/:id/sunat-config` llama siempre a `SyncFiscalToFacturador`. Es acción explícita del usuario al guardar configuración SUNAT. Sin credenciales en el body, antes pisaba SOL; ahora solo actualiza metadatos (modo, IGV, flags). |
| **Estado** | Aceptable: acción explícita. Credenciales protegidas por fix 2.1. |

### 2.4 Medio — Doble sync en panel central

| Campo | Valor |
|-------|-------|
| **Archivo** | `frontend_central/src/pages/tenants/TenantsPage.tsx` |
| **Riesgo** | Medio (mitigado) |
| **Comportamiento** | Al guardar con certificado/logo: `syncFacturador` + `updateSunatConfig` (dos `company-sync` seguidos). El segundo podía pisar credenciales si el formulario no tenía SOL. |
| **Estado** | Mitigado en backend; no se modificó frontend (fuera de alcance funcional adicional). |

### 2.5 Bajo — Deploy y startup

| Proceso | Archivo | ¿Modifica empresa/Lycet? |
|---------|---------|--------------------------|
| Deploy backend | `deploy/scripts/deploy.sh`, `.github/workflows/deploy-production.yml` | No. Solo `migrate-central` + restart + health. |
| Entrypoint Go | `deploy/docker-entrypoint.sh` | No. `exec "$@"` sin hooks fiscales. |
| Entrypoint Lycet | `facturador_lycet/docker/docker-entrypoint.sh` | No. Solo arranca PHP-PM. |
| `migrate-fleet-cron` | `pkg/cmd/migrate.go` | No sync fiscal. Solo DDL/DML de esquema tenant ERP. |
| Health check | `curl /health` | No. |
| Workers `app:fiscal:worker` | Lycet | Procesan cola de documentos; no modifican `empresa`. |

### 2.6 Medio — Migraciones con UPDATE de datos (históricas)

**Lycet** (`facturador_lycet/migrations/`):

- `Version20260528000000`: normaliza `send_mode`, `provider`, `pse_token`, `connection_status`.
- `Version20260529000000`: mapeo legacy `send_mode` / `provider`.

**ERP** (`pkg/database/tenantmigrations/`): varias migraciones con `UPDATE` (p. ej. `v041_automatic_send`, `v054_modifier_group_kind`). Afectan tablas ERP, no `empresa` de Lycet.

**Política:** no se reescriben migraciones ya aplicadas. Nuevas migraciones deben limitarse a esquema/índices/constraints.

### 2.7 Alto — Comando manual `app:empresas:import-from-json`

| Campo | Valor |
|-------|-------|
| **Archivo** | `facturador_lycet/src/Command/ImportEmpresasFromJsonCommand.php` |
| **Riesgo** | Alto si se ejecuta en pipeline |
| **Comportamiento** | Sobrescribe `sol_user`/`sol_pass` desde `data/empresas.json`. |
| **Estado** | No está en deploy estándar. Solo ejecución manual explícita. |

### 2.8 Informativo — `FiscalCompanySyncService` marca `connected` tras sync

Tras cada `company-sync` exitoso, Lycet fuerza `connection_status = 'connected'` sin validar emisión real. No altera credenciales; puede dar falsa sensación de conexión OK. Fuera del alcance de esta corrección.

---

## 3. Cambios aplicados

| Archivo | Cambio |
|---------|--------|
| `internal/company/service/fiscal_sync.go` | Eliminado fallback `RUC+MODDATOS`. `SOL_USER`/`SOL_PASS` condicionales. |
| `internal/company/handler/company_api.go` | Sync a Lycet solo si `logoBase64 != ""`. |
| `internal/company/service/fiscal_sync_test.go` | Tests: payload sin SOL no contiene `MODDATOS` ni `SOL_USER`. |

---

## 4. Evidencia: deploy no modifica credenciales

### 4.1 Deploy scripts

Revisión estática: ningún script de deploy invoca `company-sync`, `sync-facturador`, `import-from-json` ni escribe en tabla `empresa`.

### 4.2 Test automatizado (regresión)

```bash
cd backend_principal
go test ./internal/company/service/ -run TestFiscalSyncPayload -v
```

**Resultado esperado:** `TestFiscalSyncPayload_omitsSOLWhenNotProvided` y `TestFiscalSyncPayload_includesSOLWhenProvided` en PASS.

El test demuestra que un sync sin credenciales explícitas **no serializa** `SOL_USER` ni `MODDATOS` en el JSON enviado a Lycet.

### 4.3 Verificación manual post-deploy (checklist operativo)

Antes y después de `deploy.sh` o workflow de producción:

```sql
-- En BD del facturador (tabla empresa)
SELECT ruc, sol_user, LEFT(sol_pass, 3) AS pass_prefix, updated_at
FROM empresa
WHERE ruc IN ('10401387302', '20604903824', '20615181022');
```

```bash
# Logs del facturador: no debe haber company-sync durante el deploy
grep "company-sync" /ruta/var/log/prod.log
# Ventana: solo durante pull/migrate/restart (sin peticiones HTTP de sync)
```

```bash
# Backend: health no dispara sync
curl -sf http://127.0.0.1:3000/health
# Repetir consulta SQL — sol_user/sol_pass idénticos
```

---

## 5. Endpoints autorizados para modificar configuración fiscal

### Backend Go → Lycet (vía `company-sync`)

| Método | Endpoint ERP | Acción usuario requerida |
|--------|--------------|--------------------------|
| `PUT` | `/api/superadmin/tenants/:id/sunat-config` | Guardar configuración SUNAT (panel central) |
| `POST` | `/api/superadmin/tenants/:id/sync-facturador` | Sincronizar certificado/logo/credenciales (panel central) |
| `PUT` | `/api/company/config` | Solo si incluye **logo** y SUNAT habilitado (tenant restaurante/ERP) |

### Lycet (API directa, token `CLIENT_TOKEN`)

| Método | Endpoint | Uso |
|--------|----------|-----|
| `POST` | `/api/v1/fiscal/company-sync` | Invocado por backend Go (no por deploy) |
| `POST` | `/api/v1/empresas` | Registro/actualización directa (integración explícita) |
| `POST` | `/api/v1/configuration/` | Subir certificado/logo por RUC |
| `PATCH` | `/api/v1/empresas/{ruc}/ambiente` | Cambiar pruebas/producción (panel: `PatchSunatEnv`) |

### Endpoints que NO modifican credenciales Lycet

| Método | Endpoint | Notas |
|--------|----------|-------|
| `PUT` | `/api/company/sunat` | Solo IGV/régimen en ERP tenant |
| `PUT` | `/api/company/config` | Sin logo: solo ERP |
| `PATCH` | `/api/superadmin/tenants/:id/sunat-env` | Solo `ambiente` vía `PatchAmbiente` |
| `POST` | `/api/superadmin/tenants/:id/fiscal-test-connection` | Solo lectura/prueba |

### Endpoint desregistrado (código legacy)

`POST /api/company/sync-facturador` — handler existe en `company_api.go` pero **ruta eliminada** en `internal/company/routes.go`. No accesible desde tenant.

---

## 6. Procesos bloqueados / que no deben modificar datos automáticamente

| Proceso | ¿Modifica `empresa`? | Acción |
|---------|----------------------|--------|
| `deploy.sh` / GitHub Actions deploy | No | Sin cambios necesarios |
| `docker-entrypoint.sh` (Go y Lycet) | No | Sin cambios |
| `migrate-central` | No (solo BD central) | OK |
| `migrate-fleet` / `migrate-fleet-cron` | No (solo esquema tenant ERP) | OK |
| Health check `/health` | No | OK |
| `app:fiscal:worker` | No (cola documentos) | OK |
| `cache:warmup` / `cache:clear` | No | OK |
| `composer/PostInstall.php` | Solo `data/cert.pem` en beta | OK |
| **`SyncFiscalToFacturador` sin credenciales** | **Antes: sí (MODDATOS)** | **Bloqueado** |
| **`PUT company/config` sin logo** | **Antes: sí (sync parcial)** | **Bloqueado** |

---

## 7. Matriz de riesgo residual

| Riesgo | Nivel | Mitigación |
|--------|-------|------------|
| Usuario guarda SUNAT en central sin reingresar SOL | Bajo | Credenciales no se envían; Lycet conserva valores |
| Ejecutar `import-from-json` en producción | Alto | Prohibir en pipeline; solo manual con backup |
| Migraciones Lycet con UPDATE legacy | Medio | Ya aplicadas; no repetibles |
| Doble sync panel central | Bajo | Backend ya no pisa SOL en segundo sync |
| `connection_status=connected` post-sync | Bajo | Documentado; no afecta credenciales |

---

## 8. Referencias de código

- Fallback eliminado: `internal/company/service/fiscal_sync.go`
- Guard logo-only sync: `internal/company/handler/company_api.go`
- Merge parcial Lycet: `facturador_lycet/src/Service/EmpresasService.php` (líneas 233-238)
- Validación credenciales existentes: `facturador_lycet/src/Service/Fiscal/FiscalCompanySyncService.php` (`validateByMode`)
