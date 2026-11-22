# Fase 3B — BranchContext, permisos `branches.*` y backfill

Fecha: 10-nov-2026.

## Qué cambia
- **Permisos RBAC** (catálogo `tenant_permissions`, módulo `branches`): `view_all` (consulta consolidada, solo lectura),
  `switch` (cambiar entre las sucursales asignadas) y `manage` (crear/editar sucursales, conceder acceso global).
- **"Ver todas" = permiso `branches.view_all`**, no el nombre del rol ni cuántas sucursales tenga asignadas
  (`pkg/branch/perms.go: ResolveViewAll / RoleViewsAll`). El POS por PIN nunca recibe acceso global.
- **Compatibilidad de despliegue:** mientras un tenant no tenga V176 (no existe la fila `branches.view_all`), se conserva la
  regla anterior (rol "Administrador") y NO se bloquea a usuarios sin sucursal. Orden de despliegue: backend → migrate-fleet V176.
- **BranchContext** (`pkg/branch.Context`, `branch.FromCtx(c)`): `{UserID, ActiveBranchID, AllowedBranchIDs, CanViewAll, CanSwitch, Mode}`
  armado por el middleware en cada solicitud (usuario activo, sucursal asignada, permisos).
- **Modo ALL_BRANCHES:** solo con cabecera explícita `X-Branch-Mode: all` **y** `branches.view_all`. Es de solo lectura: POST/PUT/PATCH/DELETE
  responden 403 `BRANCH_READ_ONLY`. Sin la cabecera (o sin permiso) el modo es `SINGLE_BRANCH`. "Sin sucursal" nunca equivale a global.
- **Cambio de sucursal:** puede quien ve todas, quien tiene `branches.switch`, o quien tiene 2+ sucursales asignadas (solo entre ellas).
  El PIN solo con `branches.switch` explícito. El flag `can_switch_branch` ya no concede nada en tenants con V176.
- **Usuarios sin sucursal:** bloqueados (403 `BRANCH_REQUIRED`) en tenants con V176; V176 hace el backfill previo.
- **Anti-escalada** (`internal/users/handler/branch_escalation.go`): para asignar un rol con `view_all`/`manage`, otorgar esos permisos a un rol,
  o asignar una sucursal propia ajena, hace falta `branches.view_all` o `branches.manage`. Crear/eliminar sucursales: `view_all` o `manage`.
- `login`, `session/context` y `switch-branch` devuelven `can_view_all`.

## V176 (aditiva, idempotente, sin borrar nada)
1. Siembra los 3 permisos. 2. Administrador recibe los 3; los roles con usuarios `can_switch_branch=true` reciben `branches.switch`.
3. Todo usuario sin filas en `tenant_user_branches` recibe su `home_branch_id`, luego `branch_id`, o la sucursal principal.

## Pruebas
`pkg/branch` (perms, switch), `pkg/middleware` (permiso vs nombre de rol, multi-sucursal ≠ global, modo ALL solo lectura, PIN,
usuario sin sucursal, tenant sin V176, usuario inactivo), `pkg/database/tenantmigrations` (V176), `internal/users/handler` (anti-escalada).
Verificado en vivo contra el tenant demo local.

## Pendiente / riesgos
- Los frontends aún no envían `X-Branch-Mode` (el modo ALL existe en el backend y no se usa).
- El JWT lleva los permisos: un cambio de permisos aplica al renovar el token (el middleware no relee permisos por solicitud).
- `can_switch_branch` y `SyncUserBranchFields` siguen escribiéndose por compatibilidad (informativos).
- Roles personalizados con `company.edit` pierden crear/eliminar sucursales salvo que reciban `branches.manage`.
