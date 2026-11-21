package database

import (
	"sync"
	"sync/atomic"

	"gorm.io/gorm"
)

// Los sondeos de esquema (Migrator().HasColumn/HasTable) consultan information_schema y, en gorm,
// además hacen un SELECT DATABASE()/SCHEMATA previo. Se ejecutaban en CADA login y en cada
// resolución de sucursal; con la BD cargada esas consultas llegaron a tardar 14 a 1.400 segundos y
// todo el request quedaba esperando (incidente 2026-10-09).
//
// El esquema de un tenant solo crece (las migraciones agregan columnas/tablas, nunca las quitan),
// así que un resultado POSITIVO es definitivo mientras viva el pool: se cachea por pool de conexión.
// Los negativos no se cachean (el tenant puede migrar después).

type schemaProbeKey struct {
	pool interface{}
	name string
}

const schemaProbeCacheMax = 8192

var (
	schemaProbeTrue sync.Map
	schemaProbeSize atomic.Int64
)

// CachedSchemaProbe devuelve true de inmediato si el sondeo `name` ya dio true para el pool de db;
// si no, ejecuta probe y cachea solo un resultado true.
func CachedSchemaProbe(db *gorm.DB, name string, probe func() bool) bool {
	if db == nil || db.Config == nil || db.Config.ConnPool == nil {
		return probe()
	}
	key := schemaProbeKey{pool: db.Config.ConnPool, name: name}
	if _, ok := schemaProbeTrue.Load(key); ok {
		return true
	}
	if !probe() {
		return false
	}
	if schemaProbeSize.Add(1) > schemaProbeCacheMax {
		// Los pools evictados dejan claves huérfanas: se vacía todo y se vuelve a llenar.
		schemaProbeTrue.Range(func(k, _ interface{}) bool { schemaProbeTrue.Delete(k); return true })
		schemaProbeSize.Store(1)
	}
	schemaProbeTrue.Store(key, struct{}{})
	return true
}
