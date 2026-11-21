package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V193SessionVersionBackfill lleva tenant_users.branch_session_version de 0 a 1.
//
// La columna se creó en V031 sin backfill (default 0) y el middleware trataba el claim 0 como "legado": la
// primera revocación (0 -> 1) no invalidaba ningún token. Con todos los usuarios en >= 1, los tokens nuevos
// llevan claim >= 1 y son revocables desde la primera vez (ver pkg/middleware/branch_context.go).
//
// Solo toca filas en 0, incluidos usuarios eliminados lógicamente (por si se restauran). Idempotente; si la
// tabla o la columna aún no existen no hace nada.
type V193SessionVersionBackfill struct{}

func (V193SessionVersionBackfill) Version() int { return 193 }
func (V193SessionVersionBackfill) Name() string  { return "session_version_backfill" }

func (V193SessionVersionBackfill) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&database.TenantUser{}) || !mig.HasColumn(&database.TenantUser{}, "BranchSessionVersion") {
		return nil
	}
	return db.Unscoped().Model(&database.TenantUser{}).
		Where("branch_session_version = 0").
		UpdateColumn("branch_session_version", 1).Error
}
