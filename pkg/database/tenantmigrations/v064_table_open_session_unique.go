package tenantmigrations

import (
	"fmt"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
)

// V064TableOpenSessionUnique: una sola sesión open por mesa (dedupe + restricción física portable).
type V064TableOpenSessionUnique struct{}

func (V064TableOpenSessionUnique) Version() int { return 64 }
func (V064TableOpenSessionUnique) Name() string { return "table_open_session_unique" }

const (
	idxOpenSessionPerTable = "ux_open_session_per_table"
	colOpenTableKey        = "open_table_key"
)

func (V064TableOpenSessionUnique) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_table_sessions") {
		return nil
	}

	dupTables, err := countDuplicateGroups(db, `
		SELECT COUNT(*) FROM (
			SELECT table_id
			FROM tenant_table_sessions
			WHERE status = 'open' AND table_id IS NOT NULL
			GROUP BY table_id
			HAVING COUNT(*) > 1
		) dup`)
	if err != nil {
		return fmt.Errorf("auditoría sesiones open duplicadas: %w", err)
	}
	if dupTables > 0 {
		log.Printf("[v064] tenant: cerrando sesiones open duplicadas en %d mesa(s)", dupTables)
		if err := dedupeOpenSessionsKeepNewest(db); err != nil {
			return err
		}
	}

	if err := syncAllTableOccupancy(db); err != nil {
		return err
	}

	if migrationHasIndex(db, "tenant_table_sessions", idxOpenSessionPerTable) {
		log.Printf("[v064] tenant: índice %s ya existe — omitiendo creación", idxOpenSessionPerTable)
		return nil
	}

	cap := detectDBCapabilities(db)
	log.Printf("[v064] tenant: motor detectado: %s (major=%d minor=%d patch=%d)",
		cap.String(), cap.Major, cap.Minor, cap.Patch)

	if cap.SupportsFunctionalPartialUnique() {
		if err := createFunctionalPartialUniqueIndex(db, cap); err == nil {
			if migrationHasIndex(db, "tenant_table_sessions", idxOpenSessionPerTable) {
				log.Printf("[v064] tenant: índice funcional %s creado (%s)", idxOpenSessionPerTable, cap.Engine)
				return nil
			}
		} else {
			log.Printf("[v064] tenant: ADVERTENCIA — índice funcional no disponible en %s: %v", cap.Engine, err)
		}
	} else {
		log.Printf("[v064] tenant: motor %s no soporta índice funcional parcial — probando columna generada", cap.Engine)
	}

	if cap.SupportsGeneratedColumnUnique() {
		if err := createGeneratedColumnPartialUniqueIndex(db); err == nil {
			if migrationHasIndex(db, "tenant_table_sessions", idxOpenSessionPerTable) {
				log.Printf("[v064] tenant: índice sobre columna %s creado (%s)", colOpenTableKey, cap.Engine)
				return nil
			}
		} else {
			log.Printf("[v064] tenant: ADVERTENCIA — columna generada + UNIQUE falló en %s: %v", cap.Engine, err)
		}
	} else {
		log.Printf("[v064] tenant: motor %s no soporta columna generada indexada", cap.Engine)
	}

	log.Printf("[v064] tenant: ADVERTENCIA — sin restricción física en BD; unicidad delegada a aplicación (FOR UPDATE + v064 dedupe). Motor: %s", cap.Engine)
	return nil
}

func createFunctionalPartialUniqueIndex(db *gorm.DB, cap dbCapabilities) error {
	var sql string
	switch cap.Engine {
	case engineMariaDB:
		sql = fmt.Sprintf(`
			CREATE UNIQUE INDEX %s ON tenant_table_sessions (
				(CASE WHEN status = 'open' AND table_id IS NOT NULL THEN table_id ELSE NULL END)
			)`, idxOpenSessionPerTable)
	default:
		sql = fmt.Sprintf(`
			CREATE UNIQUE INDEX %s ON tenant_table_sessions (
				(IF(status = 'open', table_id, NULL))
			)`, idxOpenSessionPerTable)
	}
	return db.Exec(sql).Error
}

func createGeneratedColumnPartialUniqueIndex(db *gorm.DB) error {
	if !db.Migrator().HasColumn("tenant_table_sessions", colOpenTableKey) {
		expr := `CASE WHEN status = 'open' AND table_id IS NOT NULL THEN table_id ELSE NULL END`
		addSQL := fmt.Sprintf(`
			ALTER TABLE tenant_table_sessions ADD COLUMN %s BIGINT UNSIGNED
			GENERATED ALWAYS AS (%s) VIRTUAL`, colOpenTableKey, expr)
		if err := db.Exec(addSQL).Error; err != nil {
			// MariaDB / MySQL antiguos: sintaxis sin GENERATED ALWAYS
			addSQL = fmt.Sprintf(`
				ALTER TABLE tenant_table_sessions ADD COLUMN %s BIGINT UNSIGNED
				AS (%s) VIRTUAL`, colOpenTableKey, expr)
			if err2 := db.Exec(addSQL).Error; err2 != nil {
				return fmt.Errorf("añadir columna %s: %w (fallback: %v)", colOpenTableKey, err, err2)
			}
		}
	}
	return db.Exec(fmt.Sprintf(
		`CREATE UNIQUE INDEX %s ON tenant_table_sessions (%s)`,
		idxOpenSessionPerTable, colOpenTableKey,
	)).Error
}

func dedupeOpenSessionsKeepNewest(db *gorm.DB) error {
	type dupRow struct {
		TableID uint
		KeepID  uint
	}
	var rows []dupRow
	if err := db.Raw(`
		SELECT table_id, MAX(id) AS keep_id
		FROM tenant_table_sessions
		WHERE status = 'open' AND table_id IS NOT NULL
		GROUP BY table_id
		HAVING COUNT(*) > 1
	`).Scan(&rows).Error; err != nil {
		return err
	}
	now := time.Now()
	for _, r := range rows {
		res := db.Exec(`
			UPDATE tenant_table_sessions
			SET status = 'cancelled', closed_at = ?, updated_at = ?
			WHERE table_id = ? AND status = 'open' AND id <> ?
		`, now, now, r.TableID, r.KeepID)
		if res.Error != nil {
			return res.Error
		}
		log.Printf("[v064] mesa table_id=%d: conservada sesión id=%d, canceladas=%d",
			r.TableID, r.KeepID, res.RowsAffected)
	}
	return nil
}

func syncAllTableOccupancy(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_restaurant_tables") {
		return nil
	}
	if err := db.Exec(`
		UPDATE tenant_restaurant_tables t
		SET t.status = 'ocupada', t.updated_at = NOW()
		WHERE t.deleted_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM tenant_table_sessions s
		    WHERE s.table_id = t.id AND s.status = 'open'
		  )
		  AND t.status <> 'ocupada'
	`).Error; err != nil {
		return fmt.Errorf("sync mesas → ocupada: %w", err)
	}
	if err := db.Exec(`
		UPDATE tenant_restaurant_tables t
		SET t.status = 'libre', t.updated_at = NOW()
		WHERE t.deleted_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM tenant_table_sessions s
		    WHERE s.table_id = t.id AND s.status = 'open'
		  )
		  AND t.status = 'ocupada'
	`).Error; err != nil {
		return fmt.Errorf("sync mesas → libre: %w", err)
	}
	return nil
}

// migrationHasColumn indica si la tabla tiene la columna (information_schema).
func migrationHasColumn(db *gorm.DB, table, column string) bool {
	var n int64
	_ = db.Raw(`
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?
	`, table, column).Scan(&n).Error
	return n > 0
}

// DescribeOpenSessionConstraint devuelve estrategia y SQL de referencia (diagnóstico / documentación).
func DescribeOpenSessionConstraint(cap dbCapabilities) (strategy string, sql string) {
	if cap.SupportsFunctionalPartialUnique() {
		if cap.Engine == engineMariaDB {
			return "functional_index", strings.TrimSpace(`
CREATE UNIQUE INDEX ux_open_session_per_table ON tenant_table_sessions (
  (CASE WHEN status = 'open' AND table_id IS NOT NULL THEN table_id ELSE NULL END)
);`)
		}
		return "functional_index", strings.TrimSpace(`
CREATE UNIQUE INDEX ux_open_session_per_table ON tenant_table_sessions (
  (IF(status = 'open', table_id, NULL))
);`)
	}
	if cap.SupportsGeneratedColumnUnique() {
		return "generated_column", strings.TrimSpace(`
ALTER TABLE tenant_table_sessions ADD COLUMN open_table_key BIGINT UNSIGNED
GENERATED ALWAYS AS (
  CASE WHEN status = 'open' AND table_id IS NOT NULL THEN table_id ELSE NULL END
) VIRTUAL;
CREATE UNIQUE INDEX ux_open_session_per_table ON tenant_table_sessions (open_table_key);`)
	}
	return "application_only", "-- sin restricción física; aplicación + FOR UPDATE"
}
