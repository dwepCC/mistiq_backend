package tenantbackfills

import "gorm.io/gorm"

// V033FinalizeOrphanTableOrders finaliza pedidos de mesa que quedaron 'active' en sesiones
// ya terminadas (billed/closed).
//
// Al facturar cerrando la sesión, el flujo dejaba la sesión en 'billed' pero nunca marcaba
// sus tenant_table_orders como cerrados: quedaban 'active' para siempre. La regla de
// eliminación de mesas contaba esos pedidos activos y bloqueaba la mesa aunque estuviera
// libre y ya cobrada. El código ya finaliza los pedidos al facturar; esto limpia los que
// quedaron huérfanos antes del arreglo.
//
// Solo toca sesiones terminadas: nunca 'open' ni 'billing' (operaciones en curso).
// Idempotente: al no quedar pedidos 'active' en sesiones terminadas, correrlo de nuevo no
// cambia nada.
type V033FinalizeOrphanTableOrders struct{}

func (V033FinalizeOrphanTableOrders) Version() int { return 33 }
func (V033FinalizeOrphanTableOrders) Name() string { return "finalize_orphan_table_orders" }

func (b V033FinalizeOrphanTableOrders) Run(db *gorm.DB) error {
	// Tenants sin módulo restaurante no tienen estas tablas: nada que hacer.
	if !db.Migrator().HasTable("tenant_table_orders") || !db.Migrator().HasTable("tenant_table_sessions") {
		return nil
	}
	// Subconsulta (no JOIN) para que sea portable: MySQL/MariaDB en producción y SQLite en
	// tests. La subconsulta es sobre OTRA tabla, así que MySQL no se queja de leer y escribir
	// la misma. CURRENT_TIMESTAMP existe en ambos motores.
	return db.Exec(`
		UPDATE tenant_table_orders
		SET status = 'closed', updated_at = CURRENT_TIMESTAMP
		WHERE status = 'active'
		  AND session_id IN (
		      SELECT id FROM tenant_table_sessions WHERE status IN ('billed', 'closed')
		  )
	`).Error
}
