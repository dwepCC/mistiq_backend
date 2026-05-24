# Deploy — Tukifac Backend

Archivos de despliegue en el VPS (`/opt/tukifac/`):

| Archivo / carpeta | Origen en repo |
|-------------------|----------------|
| `docker-compose.production.yml` | `backend_go/docker-compose.production.yml` |
| `.env` | copiar de `.env.production.example` (no commitear) |
| `deploy/scripts/*` | esta carpeta |
| `data/uploads`, `data/storage` | creados por `vps-init-dirs.sh` |

## Comandos rápidos (en el VPS)

```bash
cd /opt/tukifac

# Deploy completo (migrate-central → restart → health; sin migrate en entrypoint)
bash deploy/scripts/deploy.sh

# Primera vez con Migration v2 (baseline V30 + target V31)
bash deploy/scripts/migrate-init.sh

# Fleet de tenants (manual o cron cada 5 min)
bash deploy/scripts/migrate-fleet.sh   # flock + migrate-fleet-cron (lock Redis/DB)

# Solo BD central
bash deploy/scripts/migrate.sh

bash deploy/scripts/health-check.sh
bash deploy/scripts/rollback.sh
```

## Migraciones SaaS (importante)

**El deploy ya no migra todos los tenants.** Solo BD central.

Los tenants se migran en background con `migrate-fleet-cron` (cron recomendado).

- **Schema DDL:** `tenantmigrations` registry → `target_version` = `MaxVersion()` del binario.
- **Backfills datos:** registry aparte (`tenantbackfills`, hoy V31/V32). El cron itera solo backfills registrados, **no** el schema target.

Documentación completa: **[docs/MIGRATIONS-SaaS.md](../docs/MIGRATIONS-SaaS.md)**

### Cron recomendado

```bash
chmod +x /opt/tukifac/deploy/scripts/migrate-fleet.sh
mkdir -p /var/log/tukifac
```

```cron
*/5 * * * * /opt/tukifac/deploy/scripts/migrate-fleet.sh >> /var/log/tukifac/cron-migrate.log 2>&1
```

### Panel operativo

Super Admin → **Fleet Migrations** (`/fleet-migrations`)

## CI/CD

GitHub Actions: `.github/workflows/deploy-production.yml`  
Imagen: `ghcr.io/<org>/<repo>:<sha>` y `:latest`

Documentación VPS: [docs/DEPLOY-VPS-UBUNTU.md](../docs/DEPLOY-VPS-UBUNTU.md)
