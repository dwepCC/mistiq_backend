package engine

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantbackfills"
	"tukifac/pkg/logger"
)

// BackfillOptions migrate-backfill-fleet.
type BackfillOptions struct {
	// Limit máximo de EJECUCIONES REALES por backfill en esta corrida (<= 0: sin límite). Los
	// tenants que ya no necesitan un backfill no consumen presupuesto (ver runBackfillsOverTenants).
	Limit      int
	Workers    int
	ActiveOnly bool
	Version    int
	TenantSlug string
}

// RunBackfillFleet ejecuta backfills run-once registrados.
// Version <= 0: recorre todos los backfills del registry (recomendado en cron).
// Version > 0: un backfill concreto; si no existe en registry, skip operacional (no falla el proceso).
func RunBackfillFleet(opts BackfillOptions) database.MigrateSummary {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Version <= 0 {
		return runBackfills(opts, tenantbackfills.TenantBackfills)
	}

	byVer := tenantbackfills.ByVersion()
	bf, ok := byVer[opts.Version]
	if !ok {
		logger.L.Warn("backfill_registry_skip",
			slog.Int("version", opts.Version),
			slog.String("reason", "not_registered"),
		)
		return database.MigrateSummary{}
	}
	return runBackfills(opts, []tenantbackfills.TenantBackfill{bf})
}

func runBackfills(opts BackfillOptions, bfs []tenantbackfills.TenantBackfill) database.MigrateSummary {
	var tenants []database.Tenant
	if opts.TenantSlug != "" {
		var t database.Tenant
		if err := database.CentralDB.Where("slug = ?", opts.TenantSlug).First(&t).Error; err != nil {
			return database.MigrateSummary{Failed: []database.TenantMigrateFailure{{Slug: opts.TenantSlug, Err: err}}}
		}
		tenants = []database.Tenant{t}
	} else {
		var err error
		tenants, err = database.ListTenantsForMigration(opts.ActiveOnly)
		if err != nil {
			return database.MigrateSummary{Failed: []database.TenantMigrateFailure{{Slug: "(list)", Err: err}}}
		}
	}
	return runBackfillsOverTenants(tenants, bfs, opts.Limit, opts.Workers, openTenantForBackfill)
}

// tenantDBOpener abre la BD de un tenant y devuelve con qué cerrarla (inyectable en tests).
type tenantDBOpener func(dbName string) (*gorm.DB, func(), error)

func openTenantForBackfill(dbName string) (*gorm.DB, func(), error) {
	db, err := database.OpenTenantDBForMigration(dbName)
	if err != nil {
		return nil, nil, err
	}
	return db, func() { database.CloseTenantDB(db) }, nil
}

// backfillBudget cuenta ejecuciones reales por versión de backfill, cada una con su propio tope
// independiente (un backfill que gasta su presupuesto no deja sin turno a los demás).
type backfillBudget struct {
	mu    sync.Mutex
	limit int
	used  map[int]int
}

func newBackfillBudget(limit int) *backfillBudget {
	return &backfillBudget{limit: limit, used: map[int]int{}}
}

// take reserva una ejecución para version; false si su presupuesto ya se agotó.
func (b *backfillBudget) take(version int) bool {
	if b.limit <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used[version] >= b.limit {
		return false
	}
	b.used[version]++
	return true
}

// exhausted true si ningún backfill de bfs puede ejecutarse más en esta corrida.
func (b *backfillBudget) exhausted(bfs []tenantbackfills.TenantBackfill) bool {
	if b.limit <= 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, bf := range bfs {
		if b.used[bf.Version()] < b.limit {
			return false
		}
	}
	return true
}

// runBackfillsOverTenants recorre los tenants en orden y, en cada uno, ejecuta los backfills de
// bfs que todavía le hacen falta. El presupuesto `limit` cuenta SOLO ejecuciones reales: un
// tenant donde un backfill ya está al día se salta sin gastar turno.
//
// Antes, la lista de tenants (orden alfabético fijo) se truncaba a `limit` ANTES de saber cuáles
// necesitaban el backfill, así que el cron (limit=100) recorría siempre los mismos ~100 primeros
// tenants y el resto nunca recibía un backfill por esa vía; y como V031/V032/V036 son Repeatable,
// se re-ejecutaban sobre esos mismos 100 cada 5 min sin fin (en producción, 14-oct-2026: ~23.700
// filas de historial por tenant en esos 100, ~2,4 M en total; los 52 tenants creados desde el
// 06-oct no tenían ningún backfill registrado). Ahora la rotación es natural: lo que se ejecuta
// deja de estar pendiente y la siguiente corrida avanza a los siguientes.
//
// Cada tenant se abre UNA sola vez para todos los backfills (antes: una conexión por backfill).
func runBackfillsOverTenants(
	tenants []database.Tenant,
	bfs []tenantbackfills.TenantBackfill,
	limit, workers int,
	open tenantDBOpener,
) database.MigrateSummary {
	summary := database.MigrateSummary{}
	if workers <= 0 {
		workers = 1
	}
	budget := newBackfillBudget(limit)

	var pause time.Duration
	batchSize := 50
	if cfg := config.AppConfig; cfg != nil {
		pause = cfg.MigrationBatchPause
		if cfg.MigrationBatchSize > 0 {
			batchSize = cfg.MigrationBatchSize
		}
	}
	// La pausa entre lotes protege a la BD de las ejecuciones reales; los tenants al día solo
	// cuestan una consulta al historial, así que no cuentan para el lote.
	var executed atomic.Int64

	jobs := make(chan database.Tenant)
	var wg sync.WaitGroup
	var mu sync.Mutex

	worker := func() {
		defer wg.Done()
		for t := range jobs {
			ok, failed, ran := runTenantBackfills(t, bfs, budget, open)
			executed.Add(int64(ran))
			mu.Lock()
			summary.Success = append(summary.Success, ok...)
			summary.Failed = append(summary.Failed, failed...)
			mu.Unlock()
		}
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go worker()
	}
	var lastPauseAt int64
	for _, t := range tenants {
		if budget.exhausted(bfs) {
			break
		}
		jobs <- t
		if pause > 0 {
			if n := executed.Load(); n-lastPauseAt >= int64(batchSize) {
				time.Sleep(pause)
				lastPauseAt = n
			}
		}
	}
	close(jobs)
	wg.Wait()
	return summary
}

// runTenantBackfills ejecuta en un tenant los backfills pendientes que aún tengan presupuesto.
// ok: un slug por backfill al día o ejecutado con éxito (misma convención que antes); ran:
// cuántos se ejecutaron de verdad.
func runTenantBackfills(
	t database.Tenant,
	bfs []tenantbackfills.TenantBackfill,
	budget *backfillBudget,
	open tenantDBOpener,
) (ok []string, failed []database.TenantMigrateFailure, ran int) {
	db, closeDB, err := open(t.DBName)
	if err != nil {
		return nil, []database.TenantMigrateFailure{{Slug: t.Slug, DBName: t.DBName, Err: err}}, 0
	}
	defer closeDB()

	state, err := loadBackfillState(db)
	if err != nil {
		return nil, []database.TenantMigrateFailure{{Slug: t.Slug, DBName: t.DBName, Err: err}}, 0
	}
	for _, bf := range bfs {
		if !state.due(bf) {
			logger.L.Debug("tenant_backfill_skip",
				slog.String("tenant", t.Slug),
				slog.Int("version", bf.Version()),
			)
			ok = append(ok, t.Slug)
			continue
		}
		if !budget.take(bf.Version()) {
			continue // pendiente; le toca en una próxima corrida
		}
		ran++
		if err := runBackfillOn(db, t.Slug, bf); err != nil {
			failed = append(failed, database.TenantMigrateFailure{Slug: t.Slug, DBName: t.DBName, Err: err})
			continue
		}
		ok = append(ok, t.Slug)
	}
	return ok, failed, ran
}

// backfillState lo que el historial del tenant dice sobre sus backfills, leído en UNA consulta.
// Se compara por id (autoincremental, orden de inserción) y no por applied_at: no depende de
// relojes ni de la precisión de la columna.
type backfillState struct {
	lastBackfillOK map[int]uint // versión → id del último éxito
	lastSchemaOK   uint         // id de la última migración de schema exitosa (0: ninguna)
}

func loadBackfillState(db *gorm.DB) (backfillState, error) {
	st := backfillState{lastBackfillOK: map[int]uint{}}
	if !db.Migrator().HasTable(&database.TenantMigrationHistory{}) {
		return st, nil
	}
	var rows []struct {
		Type    string
		Version int
		MaxID   uint
	}
	if err := db.Model(&database.TenantMigrationHistory{}).
		Select("type, version, MAX(id) AS max_id").
		Where("success = ?", true).
		Group("type, version").
		Scan(&rows).Error; err != nil {
		return st, err
	}
	for _, r := range rows {
		switch r.Type {
		case database.MigrationHistoryTypeBackfill:
			st.lastBackfillOK[r.Version] = r.MaxID
		case database.MigrationHistoryTypeSchema:
			if r.MaxID > st.lastSchemaOK {
				st.lastSchemaOK = r.MaxID
			}
		}
	}
	return st, nil
}

// due decide si el backfill le hace falta al tenant.
//
//   - Run-once (normal): mientras no tenga un éxito registrado para esa versión.
//   - tenantbackfills.Repeatable: mientras no tenga un éxito registrado DESPUÉS de la última
//     migración de schema del tenant. Así cubre el caso que motivó Repeatable (un éxito "vacío"
//     mientras el schema del que depende seguía a medio desplegar: cada migración posterior lo
//     vuelve a habilitar una vez) sin re-ejecutarse cada 5 min para siempre con el schema quieto.
func (st backfillState) due(bf tenantbackfills.TenantBackfill) bool {
	last, ok := st.lastBackfillOK[bf.Version()]
	if !ok {
		return true
	}
	if tenantbackfills.IsRepeatable(bf) {
		return last < st.lastSchemaOK
	}
	return false
}

// shouldSkipBackfill true si el tenant de db ya no necesita bf (ver backfillState.due).
func shouldSkipBackfill(db *gorm.DB, bf tenantbackfills.TenantBackfill) (bool, error) {
	st, err := loadBackfillState(db)
	if err != nil {
		return false, err
	}
	return !st.due(bf), nil
}

func runBackfillOn(db *gorm.DB, slug string, bf tenantbackfills.TenantBackfill) error {
	version := bf.Version()
	logger.L.Info("tenant_backfill_start",
		slog.String("tenant", slug),
		slog.Int("version", version),
		slog.String("name", bf.Name()),
	)
	start := time.Now()
	err := bf.Run(db)
	dur := time.Since(start)
	if err != nil {
		_ = RecordBackfillHistory(db, version, bf.Name(), dur, false, err.Error())
		logger.L.Error("tenant_backfill_failed",
			slog.String("tenant", slug),
			slog.Int("version", version),
			slog.Duration("duration", dur),
			slog.Any("error", err),
		)
		return err
	}
	if err := RecordBackfillHistory(db, version, bf.Name(), dur, true, ""); err != nil {
		return err
	}
	logger.L.Info("tenant_backfill_success",
		slog.String("tenant", slug),
		slog.Int("version", version),
		slog.Duration("duration", dur),
	)
	return nil
}
