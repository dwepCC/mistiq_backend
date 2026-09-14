package tenantbackfills

import "gorm.io/gorm"

// TenantBackfill migración de datos run-once (no DDL masivo en Up).
type TenantBackfill interface {
	Version() int
	Name() string
	Run(db *gorm.DB) error
}

// Described backfill que explica en castellano qué corrige.
//
// Opcional para no tocar los ya escritos. El panel central lo muestra al elegirlo: un nombre
// como «product_codes» no alcanza para decidir si ejecutarlo sobre datos de producción.
type Described interface {
	Description() string
}

// DescriptionOf texto del backfill, o vacío si no describe nada.
func DescriptionOf(b TenantBackfill) string {
	if d, ok := b.(Described); ok {
		return d.Description()
	}
	return ""
}

// Repeatable backfill cuyo propio Run() ya es seguro de ejecutar múltiples veces — normalmente
// porque solo actúa sobre filas que siguen cumpliendo su propia condición WHERE (p.ej.
// "cash_session_id IS NULL"), así que repetirlo nunca reescribe lo ya resuelto ni inventa nada
// nuevo, solo reevalúa lo que sigue pendiente.
//
// Por qué existe: tenant_migration_history (vía IsBackfillApplied) trata a todo TenantBackfill
// como "una sola vez para siempre" — la primera corrida sin error, aunque resuelva 0 filas,
// bloquea cualquier reintento posterior. Eso es correcto para un backfill que hace una
// transformación pesada de una sola vez (p.ej. V031/V032), pero es activamente dañino para uno
// cuya elegibilidad depende de OTRO schema (columnas/tablas) que se despliega de forma
// incremental y asíncrona por tenant (cron de migración cada 5 min): si este backfill corre
// mientras ese schema dependiente todavía está a medio desplegar para ese tenant, puede
// "tener éxito" resolviendo poco o nada y quedar marcado aplicado para siempre, sin que el
// motor lo vuelva a intentar nunca — encontrado en producción para
// V036SalePaymentCashSessionBackfill: ~8300 pagos históricos en ~190 tenants quedaron
// resolubles y sin escribir tras marcarse "ya aplicado" prematuramente.
//
// Opcional (misma convención que Described): un TenantBackfill que no la implementa mantiene el
// comportamiento run-once de siempre.
type Repeatable interface {
	Repeatable() bool
}

// IsRepeatable true si el backfill se declaró explícitamente seguro de re-ejecutar siempre.
func IsRepeatable(b TenantBackfill) bool {
	r, ok := b.(Repeatable)
	return ok && r.Repeatable()
}
