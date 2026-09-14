package tenantbackfills

import "testing"

// TestIsRepeatable_AsyncSchemaDependentBackfillsAreRepeatable: V031, V032 y V036 comparten el
// mismo defecto — dependen de una columna/tabla que el cron de migración despliega de forma
// incremental y asíncrona por tenant (hasColumn(...) → error si el schema aún no está listo) —
// así que los tres deben eximirse del candado run-once genérico.
func TestIsRepeatable_AsyncSchemaDependentBackfillsAreRepeatable(t *testing.T) {
	for _, bf := range []TenantBackfill{
		V031BranchBackfill{},
		V032RestaurantOrdersBackfill{},
		V036SalePaymentCashSessionBackfill{},
	} {
		if !IsRepeatable(bf) {
			t.Fatalf("%s debe ser Repeatable: depende de schema desplegado de forma incremental "+
				"por tenant; el candado run-once genérico puede trabarlo con un resultado "+
				"parcial para siempre (mismo bug encontrado en producción 2026-09-06 para V036, "+
				"extendido a V031/V032 al auditarlos: comparten el mismo hasColumn(...) → error)",
				bf.Name())
		}
	}
}

func TestIsRepeatable_DefaultFalseForOtherBackfills(t *testing.T) {
	for _, bf := range []TenantBackfill{
		V033FinalizeOrphanTableOrders{},
		V034ProductCodes{},
		V035MergeDuplicateContacts{},
	} {
		if IsRepeatable(bf) {
			t.Fatalf("%s no debería ser Repeatable: no depende de schema desplegado de forma "+
				"asíncrona (sin hasColumn/hasTable gate), así que el candado run-once sigue "+
				"siendo correcto para este backfill", bf.Name())
		}
	}
}
