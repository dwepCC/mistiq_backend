package cmd

import (
	"flag"
	"fmt"

	"tukifac/pkg/database"
)

// RunResetMigrationHistory borra la(s) fila(s) de tenant_migration_history de UN tenant y UNA
// versión puntual — para permitir que se reintente una migración/backfill que
// audit-migration-history-errors detectó enmascarada como éxito (bug de 482b922) o que quedó
// realmente incompleta por cualquier otro motivo ya diagnosticado.
//
// Deliberadamente estrecho: exige --tenant y --version explícitos (nunca opera en lote ni con
// --active-only) — este es un comando de reparación quirúrgica, no de mantenimiento rutinario.
// Después de resetear, usar migrate-tenant <slug> (type=schema) o
// migrate-backfill-fleet --version=N --tenant=<slug> (type=backfill) para reintentar.
func RunResetMigrationHistory(args []string) int {
	fs := flag.NewFlagSet("reset-migration-history", flag.ExitOnError)
	slug := fs.String("tenant", "", "tenant por slug (obligatorio)")
	version := fs.Int("version", 0, "versión a resetear (obligatorio)")
	typ := fs.String("type", database.MigrationHistoryTypeSchema, "schema|backfill")
	dryRun := fs.Bool("dry-run", false, "solo mostrar qué se borraría, sin borrar nada")
	_ = fs.Parse(args)

	if *slug == "" || *version <= 0 {
		fmt.Println("uso: reset-migration-history --tenant=<slug> --version=<n> [--type=schema|backfill] [--dry-run]")
		return 1
	}
	if *typ != database.MigrationHistoryTypeSchema && *typ != database.MigrationHistoryTypeBackfill {
		fmt.Printf("✗ --type inválido (use schema o backfill): %s\n", *typ)
		return 1
	}

	var t database.Tenant
	if err := database.CentralDB.Where("slug = ?", *slug).First(&t).Error; err != nil {
		fmt.Printf("✗ tenant %q no encontrado: %v\n", *slug, err)
		return 1
	}

	db, err := database.OpenTenantDBForMigration(t.DBName)
	if err != nil {
		fmt.Printf("✗ abrir conexión: %v\n", err)
		return 1
	}
	defer database.CloseTenantDB(db)

	var rows []database.TenantMigrationHistory
	if err := db.Where("version = ? AND type = ?", *version, *typ).Find(&rows).Error; err != nil {
		fmt.Printf("✗ consultar historial: %v\n", err)
		return 1
	}
	if len(rows) == 0 {
		fmt.Printf("(nada que resetear: %s v%d type=%s no tiene fila de historial)\n", *slug, *version, *typ)
		return 0
	}

	for _, r := range rows {
		errMsg := ""
		if r.Error != nil {
			errMsg = *r.Error
		}
		fmt.Printf("  encontrado: id=%d name=%s success=%v applied_at=%s error=%q\n",
			r.ID, r.Name, r.Success, r.AppliedAt.Format("2006-01-02 15:04:05"), errMsg)
	}

	if *dryRun {
		fmt.Println("(dry-run: no se borró nada)")
		return 0
	}

	res := db.Where("version = ? AND type = ?", *version, *typ).Delete(&database.TenantMigrationHistory{})
	if res.Error != nil {
		fmt.Printf("✗ borrar historial: %v\n", res.Error)
		return 1
	}
	fmt.Printf("✓ %d fila(s) de historial borradas: %s v%d type=%s\n", res.RowsAffected, *slug, *version, *typ)
	return 0
}
