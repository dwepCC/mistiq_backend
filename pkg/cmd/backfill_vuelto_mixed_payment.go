package cmd

import (
	"flag"
	"fmt"

	"tukifac/pkg/database"
	"tukifac/pkg/database/engine"
	"tukifac/pkg/database/tenantbackfills"
)

// RunBackfillVueltoMixedPayment revisa (o aplica) el backfill de vuelto en pagos mixtos
// (V146VueltoMixedPaymentBackfill) — corrige tenant_cash_movements/tenant_bank_movements de
// ventas con pago mixto (efectivo + electrónico) en sesión de caja abierta, donde el vuelto se
// había repartido proporcionalmente entre todos los métodos en vez de salir 100% de efectivo.
//
// Mismo patrón que RunBackfillSalePaymentCashSession: --dry-run corre el mismo análisis sin
// escribir nada, útil para confirmar el alcance exacto antes de aplicarlo sobre producción.
func RunBackfillVueltoMixedPayment(args []string) int {
	fs := flag.NewFlagSet("backfill-vuelto-mixed-payment", flag.ExitOnError)
	slug := fs.String("tenant", "", "solo este tenant (por slug); vacío = todos")
	dryRun := fs.Bool("dry-run", false, "solo informar (ventas analizadas/movimientos corregidos/requieren revisión/errores), sin escribir")
	activeOnly := fs.Bool("active-only", false, "omitir tenants no activos")
	_ = fs.Parse(args)

	if !*dryRun {
		summary := engine.RunBackfillFleet(engine.BackfillOptions{
			Version:    tenantbackfills.V146VueltoMixedPaymentBackfill{}.Version(),
			TenantSlug: *slug,
			ActiveOnly: *activeOnly,
		})
		fmt.Printf("backfill-vuelto-mixed-payment ok=%d error=%d\n", len(summary.Success), len(summary.Failed))
		for _, f := range summary.Failed {
			fmt.Printf("  ✗ %-24s %v\n", f.Slug, f.Err)
		}
		if len(summary.Failed) > 0 {
			return 1
		}
		return 0
	}

	tenants, err := database.ListTenantsForMigration(*activeOnly)
	if err != nil {
		fmt.Printf("✗ no se pudo listar tenants: %v\n", err)
		return 1
	}
	if *slug != "" {
		filtered := tenants[:0]
		for _, t := range tenants {
			if t.Slug == *slug {
				filtered = append(filtered, t)
			}
		}
		tenants = filtered
		if len(tenants) == 0 {
			fmt.Printf("✗ tenant %q no encontrado\n", *slug)
			return 1
		}
	}

	fmt.Printf("backfill-vuelto-mixed-payment mode=dry-run tenants=%d\n", len(tenants))
	var totalAnalyzed, totalFixed, totalNeedsReview, totalErrors, failed int
	bf := tenantbackfills.V146VueltoMixedPaymentBackfill{}
	for _, t := range tenants {
		db, err := database.OpenTenantDBForMigration(t.DBName)
		if err != nil {
			failed++
			fmt.Printf("  ✗ %-24s %v\n", t.Slug, err)
			continue
		}
		result, err := bf.Diagnose(db)
		database.CloseTenantDB(db)
		if err != nil {
			failed++
			fmt.Printf("  ✗ %-24s %v\n", t.Slug, err)
			continue
		}
		totalAnalyzed += result.SalesAnalyzed
		totalFixed += result.MovementsFixed
		totalNeedsReview += result.SalesNeedsReview
		totalErrors += result.Errors
		if result.SalesAnalyzed == 0 {
			continue // nada pendiente en este tenant: no ensuciar la salida
		}
		fmt.Printf("  · %-24s ventas_analizadas=%d movimientos_a_corregir=%d requieren_revision=%d errores=%d\n",
			t.Slug, result.SalesAnalyzed, result.MovementsFixed, result.SalesNeedsReview, result.Errors)
	}

	fmt.Printf("\nventas_analizadas=%d movimientos_a_corregir=%d requieren_revision=%d errores=%d\n",
		totalAnalyzed, totalFixed, totalNeedsReview, totalErrors)
	fmt.Println("(dry-run: no se escribió nada)")
	if failed > 0 {
		return 1
	}
	return 0
}
