package cmd

import (
	"fmt"
	"os"
)

// Execute despacha subcomandos CLI. Retorna código de salida.
func Execute(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 1
	}

	if err := InitDatabase(); err != nil {
		fmt.Fprintf(os.Stderr, "database connection failed: %v\n", err)
		return 1
	}

	switch args[0] {
	case "migrate":
		return RunMigrate()
	case "migrate-central":
		return RunMigrateCentral()
	case "migrate-init-versions":
		return RunMigrateInitVersions()
	case "migrate-bump-target":
		return RunMigrateBumpTarget()
	case "migrate-fleet":
		return RunMigrateFleet(args[1:])
	case "migrate-fleet-cron":
		return RunMigrateFleetCron(args[1:])
	case "migrate-fleet-resume":
		return RunMigrateFleetResume()
	case "migrate-backfill-fleet":
		return RunMigrateBackfillFleet(args[1:])
	case "migrate-tenants":
		return RunMigrateTenants()
	case "migrate-tenant":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tukifac-api migrate-tenant <slug>")
			return 1
		}
		return RunMigrateTenant(args[1])
	case "migrate-backfill-branch":
		return RunMigrateBackfillBranch()
	case "repair-tenant-migrations":
		return RunRepairTenantMigrations(args[1:])
	case "backfill-product-codes":
		return RunBackfillProductCodes(args[1:])
	case "backfill-sale-payment-cash-session":
		return RunBackfillSalePaymentCashSession(args[1:])
	case "audit-migration-history-errors":
		return RunAuditMigrationHistoryErrors(args[1:])
	case "reset-migration-history":
		return RunResetMigrationHistory(args[1:])
	case "diagnose-tenant-permissions":
		return RunDiagnoseTenantPermissions(args[1:])
	case "audit-tenant-permission-catalog":
		return RunAuditTenantPermissionCatalog(args[1:])
	case "reseed-tenant-permissions":
		return RunReseedTenantPermissions(args[1:])
	case "payment":
		return RunPayment(args[1:])
	case "validate-prepayment-phase0":
		return RunValidatePrepaymentPhase0(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		printUsage()
		return 1
	}
}

func printUsage() {
	fmt.Println(`Tukifac API — comandos disponibles:

  serve                      Inicia el servidor HTTP (sin argumentos)
  migrate                    Solo BD central (deploy producción)
  migrate-central            Solo BD central
  migrate-init-versions      Bootstrap tenant_schema_versions V30 (una vez)
  migrate-bump-target        target_version = V31 en central (post-deploy)
  migrate-fleet              Migración incremental fleet [--limit=100] [--workers=4]
  migrate-fleet-cron         Cron seguro: bump + fleet + backfill (lock Redis/DB)
  migrate-fleet-resume       Cierra circuit breaker del fleet
  migrate-backfill-fleet     Backfills run-once [--version=31] [--workers=4] [--tenant=slug]
  migrate-tenant <slug>      Bootstrap AutoMigrate un tenant (emergencia)
  migrate-tenants            LEGACY migrate-all bootstrap (no producción)
  migrate-backfill-branch    Alias backfill V31 fleet
  repair-tenant-migrations   Reconciliar drift [--slug=] [--limit=50] [--dry-run] [--reconcile-only]
  backfill-product-codes     Código a productos sin él + snapshot de ventas [--tenant=slug] [--dry-run] [--active-only]
  backfill-sale-payment-cash-session  cash_session_id histórico en tenant_sale_payments [--tenant=slug] [--dry-run] [--active-only]
  audit-migration-history-errors  Detecta fallos de migración/backfill mal registrados como éxito [--active-only]
  reset-migration-history   Reparación puntual: borra historial de un tenant/versión para reintentar [--tenant=slug] [--version=n] [--type=schema|backfill] [--dry-run]
  diagnose-tenant-permissions  Compara catálogo/asignación de permisos de un tenant contra el código vigente [--ruc=n | --slug=s] [--role=nombre]
  audit-tenant-permission-catalog  Escanea la flota buscando tenants con catálogo de permisos incompleto [--active-only]
  reseed-tenant-permissions  Completa catálogo de permisos de un tenant y reasigna todo al Administrador [--tenant=slug]
  payment audit|repair|verify [--slug=tenant]  Dominio financiero (métodos/condiciones/tributario)
  validate-prepayment-phase0 [--slug=demo]   E2E Fase 0: boleta+factura anticipo SUNAT Beta

Deploy producción:
  ./tukifac-api migrate
  docker compose up -d --force-recreate backend-go
  ./tukifac-api migrate-bump-target
  ./tukifac-api migrate-fleet --workers=4 --limit=100
  ./tukifac-api migrate-backfill-fleet --workers=4 --limit=100`)
}
