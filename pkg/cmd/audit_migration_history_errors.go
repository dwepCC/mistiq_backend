package cmd

import (
	"flag"
	"fmt"

	"tukifac/pkg/database"
)

// RunAuditMigrationHistoryErrors escanea tenant_migration_history en cada tenant buscando filas
// con success=true pero un error no vacío — la firma inequívoca del bug corregido en 482b922
// (TenantMigrationHistory.Success con tag `default:true`: GORM omitía el campo del INSERT
// cuando se le pasaba `false` explícito, escribiendo `true` en su lugar). Cualquier fila así es,
// en realidad, un intento FALLIDO que quedó registrado como éxito — y por lo tanto bloqueado
// para siempre de cualquier reintento (schema o backfill, cualquier versión).
//
// Solo lee: nunca escribe nada. Pensado para decidir, con evidencia y no por sospecha, qué
// tenants/versiones necesitan que se les reinicie el historial y se reintente.
func RunAuditMigrationHistoryErrors(args []string) int {
	fs := flag.NewFlagSet("audit-migration-history-errors", flag.ExitOnError)
	activeOnly := fs.Bool("active-only", false, "omitir tenants no activos")
	_ = fs.Parse(args)

	tenants, err := database.ListTenantsForMigration(*activeOnly)
	if err != nil {
		fmt.Printf("✗ no se pudo listar tenants: %v\n", err)
		return 1
	}

	fmt.Printf("audit-migration-history-errors tenants=%d\n", len(tenants))
	total := 0
	byVersion := map[string]int{}
	failedTenants := 0
	for _, t := range tenants {
		db, err := database.OpenTenantDBForMigration(t.DBName)
		if err != nil {
			fmt.Printf("  ✗ %-24s abrir conexión: %v\n", t.Slug, err)
			continue
		}

		var rows []database.TenantMigrationHistory
		queryErr := db.Where("success = ? AND error IS NOT NULL AND error != ?", true, "").
			Find(&rows).Error
		database.CloseTenantDB(db)
		if queryErr != nil {
			fmt.Printf("  ✗ %-24s consultar historial: %v\n", t.Slug, queryErr)
			continue
		}
		if len(rows) == 0 {
			continue
		}
		failedTenants++
		for _, r := range rows {
			total++
			key := fmt.Sprintf("%s/v%d/%s", r.Type, r.Version, r.Name)
			byVersion[key]++
			errMsg := ""
			if r.Error != nil {
				errMsg = *r.Error
			}
			fmt.Printf("  ✗ %-24s type=%-9s version=%-4d name=%-40s error=%q\n",
				t.Slug, r.Type, r.Version, r.Name, errMsg)
		}
	}

	fmt.Printf("\ntotal_filas_afectadas=%d tenants_afectados=%d\n", total, failedTenants)
	if total > 0 {
		fmt.Println("\npor versión/tipo:")
		for k, n := range byVersion {
			fmt.Printf("  %-40s %d\n", k, n)
		}
	}
	fmt.Println("\n(solo lectura: no se escribió nada)")
	return 0
}
