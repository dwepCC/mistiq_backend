package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V205DocumentSnapshots crea las tablas de la «foto» congelada de los documentos (tenant_document_snapshots y tenant_snapshot_logos).
// Son tablas NUEVAS: no toca ninguna tabla existente ni datos. Los documentos ya emitidos reciben su foto la primera vez que se
// imprimen (o con el comando backfill-document-snapshots). Idempotente.
type V205DocumentSnapshots struct{}

func (V205DocumentSnapshots) Version() int { return 205 }
func (V205DocumentSnapshots) Name() string { return "document_snapshots" }

func (V205DocumentSnapshots) Up(db *gorm.DB) error {
	return db.AutoMigrate(&database.TenantDocumentSnapshot{}, &database.TenantSnapshotLogo{})
}
