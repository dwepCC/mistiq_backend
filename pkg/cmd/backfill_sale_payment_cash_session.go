package cmd

import (
	"flag"
	"fmt"

	"tukifac/pkg/database"
	"tukifac/pkg/database/engine"
	"tukifac/pkg/database/tenantbackfills"
)

// RunBackfillSalePaymentCashSession revisa (o aplica) el backfill de
// tenant_sale_payments.cash_session_id (V036SalePaymentCashSessionBackfill).
//
// Mismo patrón que RunBackfillProductCodes: la corrección real vive en tenantbackfills.
// V036SalePaymentCashSessionBackfill (también corre desde el panel central y el cron); este
// comando solo agrega el informe previo — cuántos pagos se resolverían, cuántos quedarían
// ambiguos/sin candidato — sin escribir nada, útil antes de decidir ejecutar el backfill real
// sobre datos de producción.
func RunBackfillSalePaymentCashSession(args []string) int {
	fs := flag.NewFlagSet("backfill-sale-payment-cash-session", flag.ExitOnError)
	slug := fs.String("tenant", "", "solo este tenant (por slug); vacío = todos")
	dryRun := fs.Bool("dry-run", false, "solo informar (analizados/resueltos/ambiguos/sin candidato/errores), sin escribir")
	activeOnly := fs.Bool("active-only", false, "omitir tenants no activos")
	_ = fs.Parse(args)

	if !*dryRun {
		summary := engine.RunBackfillFleet(engine.BackfillOptions{
			Version:    tenantbackfills.V036SalePaymentCashSessionBackfill{}.Version(),
			TenantSlug: *slug,
			ActiveOnly: *activeOnly,
		})
		fmt.Printf("backfill-sale-payment-cash-session ok=%d error=%d\n", len(summary.Success), len(summary.Failed))
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

	fmt.Printf("backfill-sale-payment-cash-session mode=dry-run tenants=%d\n", len(tenants))
	var totalAnalyzed, totalResolved, totalAmbiguous, totalNoCandidate, totalErrors, failed int
	bf := tenantbackfills.V036SalePaymentCashSessionBackfill{}
	for _, t := range tenants {
		result, err := diagnoseTenantSalePaymentCashSession(t.DBName, bf)
		if err != nil {
			failed++
			fmt.Printf("  ✗ %-24s %v\n", t.Slug, err)
			continue
		}
		totalAnalyzed += result.Analyzed
		totalResolved += result.Resolved
		totalAmbiguous += result.Ambiguous
		totalNoCandidate += result.NoCandidate
		totalErrors += result.Errors
		if result.Analyzed == 0 {
			continue // nada pendiente en este tenant: no ensuciar la salida
		}
		fmt.Printf("  · %-24s analizados=%d resueltos=%d ambiguos=%d sin_candidato=%d errores=%d\n",
			t.Slug, result.Analyzed, result.Resolved, result.Ambiguous, result.NoCandidate, result.Errors)
	}

	fmt.Printf("\nanalizados=%d resueltos=%d ambiguos=%d sin_candidato=%d errores=%d\n",
		totalAnalyzed, totalResolved, totalAmbiguous, totalNoCandidate, totalErrors)
	fmt.Println("(dry-run: no se escribió nada)")
	if failed > 0 {
		return 1
	}
	return 0
}

// diagnoseTenantSalePaymentCashSession corre el mismo análisis de correlación que el backfill real
// (V036SalePaymentCashSessionBackfill.Diagnose), pero solo abre la conexión y no escribe nada.
func diagnoseTenantSalePaymentCashSession(dbName string, bf tenantbackfills.V036SalePaymentCashSessionBackfill) (tenantbackfills.SalePaymentCashSessionBackfillResult, error) {
	db, err := database.OpenTenantDBForMigration(dbName)
	if err != nil {
		return tenantbackfills.SalePaymentCashSessionBackfillResult{}, err
	}
	defer database.CloseTenantDB(db)

	return bf.Diagnose(db)
}
