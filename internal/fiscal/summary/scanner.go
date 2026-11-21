// Package summary mantiene en la BD central un resumen de la facturación electrónica de cada
// tenant (emitidos / aceptados / faltan enviar), calculado leyendo —solo lectura— la BD de cada
// tenant. El panel central consulta solo ese resumen, sin recorrer las bases de los tenants.
// Ver docs/PLAN-RESUMEN-FISCAL-POR-TENANT.md.
package summary

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"tukifac/pkg/branchscope"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"tukifac/pkg/database"
	"tukifac/pkg/logger"
)

const (
	// WindowDays: días hacia atrás que recalcula cada ciclo corto. Lo más antiguo solo cambia
	// por la pasada profunda nocturna.
	WindowDays = 60

	shortScanTimeout = 15 * time.Second
	deepScanTimeout  = 90 * time.Second
	dateLayout       = "2006-01-02"
)

// Códigos SUNAT de venta electrónica (igual que la campanita del tenant, billing_service.go).
var saleDocCodes = []string{"01", "03", "07", "08"}

// Guías de remisión: 09 remitente, 31 transportista (tabla tenant_despatches).
var despatchDocCodes = []string{"09", "31"}

type dailyKey struct{ Day, DocCode, Status string }
type dailyVal struct {
	Cnt    int
	Amount float64
}

type openAgg struct {
	Pending, Sent, Error, Rejected int
	// Oldest: fecha de emisión más antigua entre pending y error ("faltan enviar").
	Oldest *time.Time
}

// TenantOpener abre la BD de un tenant (inyectable para tests).
type TenantOpener func(t database.Tenant) (db *gorm.DB, release func(), err error)

// Scanner calcula y guarda el resumen.
type Scanner struct {
	Central *gorm.DB
	Open    TenantOpener
	Now     func() time.Time
}

// NewScanner usa la BD central y el gestor de BD de tenants reales.
func NewScanner() *Scanner {
	return &Scanner{
		Central: database.CentralDB,
		Open: func(t database.Tenant) (*gorm.DB, func(), error) {
			// Barrido de todos los tenants cada 15 min: no debe pasar por el pool LRU compartido
			// (max 200 vs ~620 tenants), que expulsaba los pools de los tenants en operación.
			db, release, err := database.GetTenantDBBackground(t.DBName)
			if err != nil {
				return nil, func() {}, err
			}
			return branchscope.System(db, "fiscal: barrido de resúmenes diarios"), release, nil
		},
		Now: time.Now,
	}
}

// Stats resumen de una pasada completa.
type Stats struct {
	Tenants  int
	Failed   int
	Duration time.Duration
	Deep     bool
}

// ScanAll recorre todos los tenants (incluye suspendidos/inactivos; solo excluye eliminados) con
// `workers` en paralelo. Un tenant que falla no detiene el resto.
func (s *Scanner) ScanAll(deep bool, workers int) (Stats, error) {
	started := time.Now()
	var tenants []database.Tenant
	if err := s.Central.Where("db_name <> ''").Order("id ASC").Find(&tenants).Error; err != nil {
		return Stats{}, err
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan database.Tenant)
	var failed int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				if err := s.ScanTenant(t, deep); err != nil {
					atomic.AddInt64(&failed, 1)
				}
			}
		}()
	}
	for _, t := range tenants {
		jobs <- t
	}
	close(jobs)
	wg.Wait()

	st := Stats{Tenants: len(tenants), Failed: int(failed), Duration: time.Since(started), Deep: deep}
	lg().Info("fiscal_snapshot_done",
		slog.Int("tenants", st.Tenants),
		slog.Int("failed", st.Failed),
		slog.Bool("deep", st.Deep),
		slog.Int64("ms", st.Duration.Milliseconds()),
	)
	return st, nil
}

// ScanTenant actualiza el resumen de un tenant. Si falla, deja registrado el error en su fila de
// salud (para que se vea en el panel) y lo devuelve.
func (s *Scanner) ScanTenant(t database.Tenant, deep bool) error {
	started := s.Now()
	timeout := shortScanTimeout
	if deep {
		timeout = deepScanTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := s.scanTenant(ctx, t, deep, started)
	if err != nil {
		s.recordFailure(t.ID, started, err)
		lg().Warn("fiscal_snapshot_tenant_failed",
			slog.String("tenant", t.Slug), slog.Uint64("tenant_id", uint64(t.ID)), slog.Any("error", err))
	}
	return err
}

func (s *Scanner) scanTenant(ctx context.Context, t database.Tenant, deep bool, started time.Time) error {
	tdb, release, err := s.Open(t)
	if err != nil {
		return fmt.Errorf("abrir BD del tenant: %w", err)
	}
	defer release()
	tdb = tdb.WithContext(ctx)

	if !tdb.Migrator().HasTable("tenant_sales") || !tdb.Migrator().HasTable("tenant_document_series") {
		return fmt.Errorf("el tenant no tiene tenant_sales/tenant_document_series")
	}

	windowStart := ""
	if !deep {
		windowStart = started.AddDate(0, 0, -WindowDays).Format(dateLayout)
	}

	rows, err := scanSales(tdb, windowStart)
	if err != nil {
		return fmt.Errorf("ventas: %w", err)
	}
	if tdb.Migrator().HasTable("tenant_despatches") {
		g, err := scanDespatches(tdb, windowStart)
		if err != nil {
			return fmt.Errorf("guías: %w", err)
		}
		for k, v := range g {
			rows[k] = v
		}
	}

	var existingHealth database.TenantFiscalHealth
	hasHealth := s.Central.Where("tenant_id = ?", t.ID).Limit(1).Find(&existingHealth).RowsAffected > 0

	// Abiertos de más de WindowDays: solo la pasada profunda los recalcula; el ciclo corto
	// arrastra lo último guardado.
	older := openAgg{}
	var olderAt *time.Time
	if deep {
		before := started.AddDate(0, 0, -WindowDays).Format(dateLayout)
		o, err := scanOpen(tdb, before)
		if err != nil {
			return fmt.Errorf("abiertos antiguos: %w", err)
		}
		older, olderAt = o, o.Oldest
	} else if hasHealth {
		older = openAgg{
			Pending: existingHealth.OlderOpenPending, Sent: existingHealth.OlderOpenSent,
			Error: existingHealth.OlderOpenError, Rejected: existingHealth.OlderOpenRejected,
		}
		olderAt = existingHealth.OlderOldestAt
	}

	// Abiertos dentro de la ventana (o de todo el historial en la pasada profunda): se derivan de
	// las filas ya calculadas, sin otra consulta.
	win := openAgg{}
	var lastIssue *time.Time
	for k, v := range rows {
		d, perr := time.Parse(dateLayout, k.Day)
		if perr == nil && (lastIssue == nil || d.After(*lastIssue)) {
			dd := d
			lastIssue = &dd
		}
		switch k.Status {
		case "pending":
			win.Pending += v.Cnt
		case "sent":
			win.Sent += v.Cnt
		case "error":
			win.Error += v.Cnt
		case "rejected":
			win.Rejected += v.Cnt
		}
		if (k.Status == "pending" || k.Status == "error") && perr == nil && (win.Oldest == nil || d.Before(*win.Oldest)) {
			dd := d
			win.Oldest = &dd
		}
	}
	// En la pasada profunda `rows` ya incluye todo el historial: los abiertos antiguos ya están
	// contados dentro de `win`, así que no se suman otra vez.
	totalOpen := win
	oldest := win.Oldest
	if !deep {
		totalOpen.Pending += older.Pending
		totalOpen.Sent += older.Sent
		totalOpen.Error += older.Error
		totalOpen.Rejected += older.Rejected
		if olderAt != nil && (oldest == nil || olderAt.Before(*oldest)) {
			oldest = olderAt
		}
	}
	if lastIssue == nil && hasHealth {
		lastIssue = existingHealth.LastIssueAt
	}

	now := s.Now()
	if err := s.applyDaily(t.ID, windowStart, rows, now); err != nil {
		return fmt.Errorf("guardar resumen diario: %w", err)
	}

	h := database.TenantFiscalHealth{
		TenantID:  t.ID,
		ScannedAt: now,
		ScanMs:    int(now.Sub(started).Milliseconds()),
		ScanError: "",

		OpenPending: totalOpen.Pending, OpenSent: totalOpen.Sent,
		OpenError: totalOpen.Error, OpenRejected: totalOpen.Rejected,
		OldestOpenAt: oldest, LastIssueAt: lastIssue,

		OlderOpenPending: older.Pending, OlderOpenSent: older.Sent,
		OlderOpenError: older.Error, OlderOpenRejected: older.Rejected,
		OlderOldestAt: olderAt,
	}
	if deep {
		h.LastDeepAt = &now
	} else if hasHealth {
		h.LastDeepAt = existingHealth.LastDeepAt
	}
	return s.Central.Clauses(clause.OnConflict{UpdateAll: true}).Create(&h).Error
}

func (s *Scanner) recordFailure(tenantID uint, started time.Time, cause error) {
	now := s.Now()
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	h := database.TenantFiscalHealth{
		TenantID: tenantID, ScannedAt: now, ScanMs: int(now.Sub(started).Milliseconds()), ScanError: msg,
	}
	// Solo se tocan el error y la fecha: los conteos anteriores se conservan.
	s.Central.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"scanned_at", "scan_ms", "scan_error"}),
	}).Create(&h)
}

// applyDaily guarda solo las diferencias del tenant dentro del rango escaneado: inserta/actualiza
// lo nuevo o cambiado y borra lo que ya no existe (p. ej. el último "pending" pasó a "accepted").
func (s *Scanner) applyDaily(tenantID uint, windowStart string, fresh map[dailyKey]dailyVal, now time.Time) error {
	q := s.Central.Where("tenant_id = ?", tenantID)
	if windowStart != "" {
		q = q.Where("day >= ?", windowStart)
	}
	var current []database.TenantFiscalDaily
	if err := q.Find(&current).Error; err != nil {
		return err
	}
	existing := make(map[dailyKey]database.TenantFiscalDaily, len(current))
	for _, r := range current {
		existing[dailyKey{trimDay(r.Day), r.DocCode, r.BillingStatus}] = r
	}

	var upserts []database.TenantFiscalDaily
	for k, v := range fresh {
		if cur, ok := existing[k]; ok && cur.Cnt == v.Cnt && roundCents(cur.Amount) == roundCents(v.Amount) {
			continue
		}
		upserts = append(upserts, database.TenantFiscalDaily{
			TenantID: tenantID, Day: k.Day, DocCode: k.DocCode, BillingStatus: k.Status,
			Cnt: v.Cnt, Amount: roundCents(v.Amount), RefreshedAt: now,
		})
	}
	var stale []uint
	for k, r := range existing {
		if _, ok := fresh[k]; !ok {
			stale = append(stale, r.ID)
		}
	}

	return s.Central.Transaction(func(tx *gorm.DB) error {
		if len(upserts) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "tenant_id"}, {Name: "day"}, {Name: "doc_code"}, {Name: "billing_status"},
				},
				DoUpdates: clause.AssignmentColumns([]string{"cnt", "amount", "refreshed_at"}),
			}).CreateInBatches(&upserts, 200).Error; err != nil {
				return err
			}
		}
		if len(stale) > 0 {
			if err := tx.Where("id IN ?", stale).Delete(&database.TenantFiscalDaily{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// lg devuelve el logger de la app o el de slog por defecto (tests).
func lg() *slog.Logger {
	if logger.L != nil {
		return logger.L
	}
	return slog.Default()
}

func roundCents(v float64) float64 { return float64(int64(v*100+0.5*sign(v))) / 100 }

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

func trimDay(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// scanSales agrupa las ventas electrónicas por día/tipo/estado. Mantiene el JOIN con la serie:
// las notas de venta (00) también traen billing_status='pending' y solo el filtro por sunat_code
// las excluye (igual que GetNotificationCounts). Las anuladas cuentan, como en la campanita.
func scanSales(db *gorm.DB, windowStart string) (map[dailyKey]dailyVal, error) {
	sqlText := `SELECT DATE(s.issue_date) AS day, ds.sunat_code AS doc_code,
	       COALESCE(NULLIF(s.billing_status,''),'pending') AS st,
	       COUNT(*) AS cnt, COALESCE(SUM(s.total),0) AS amount
	FROM tenant_sales s
	JOIN tenant_document_series ds ON ds.id = s.series_id
	WHERE s.deleted_at IS NULL AND ds.sunat_code IN ?`
	args := []any{saleDocCodes}
	if windowStart != "" {
		sqlText += " AND s.issue_date >= ?"
		args = append(args, windowStart)
	}
	sqlText += " GROUP BY 1, 2, 3"
	return scanDaily(db, sqlText, args)
}

// scanDespatches: guías de remisión (tabla propia, sin soft delete).
func scanDespatches(db *gorm.DB, windowStart string) (map[dailyKey]dailyVal, error) {
	sqlText := `SELECT DATE(d.issue_date) AS day, ds.sunat_code AS doc_code,
	       COALESCE(NULLIF(d.status,''),'pending') AS st,
	       COUNT(*) AS cnt, 0 AS amount
	FROM tenant_despatches d
	JOIN tenant_document_series ds ON ds.id = d.series_id
	WHERE ds.sunat_code IN ?`
	args := []any{despatchDocCodes}
	if windowStart != "" {
		sqlText += " AND d.issue_date >= ?"
		args = append(args, windowStart)
	}
	sqlText += " GROUP BY 1, 2, 3"
	return scanDaily(db, sqlText, args)
}

func scanDaily(db *gorm.DB, sqlText string, args []any) (map[dailyKey]dailyVal, error) {
	rows, err := db.Raw(sqlText, args...).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[dailyKey]dailyVal{}
	for rows.Next() {
		var day, code, st sql.NullString
		var cnt int
		var amount sql.NullFloat64
		if err := rows.Scan(&day, &code, &st, &cnt, &amount); err != nil {
			return nil, err
		}
		if !day.Valid || len(day.String) < 10 {
			continue
		}
		k := dailyKey{Day: day.String[:10], DocCode: code.String, Status: st.String}
		cur := out[k]
		cur.Cnt += cnt
		cur.Amount += amount.Float64
		out[k] = cur
	}
	return out, rows.Err()
}

// scanOpen cuenta lo aún no terminado (pending/sent/error/rejected) anterior a `before`
// (YYYY-MM-DD), en ventas y guías. Solo lo usa la pasada profunda: billing_status no tiene índice.
func scanOpen(db *gorm.DB, before string) (openAgg, error) {
	agg := openAgg{}
	queries := []string{
		`SELECT COALESCE(NULLIF(s.billing_status,''),'pending') AS st, COUNT(*) AS cnt, MIN(s.issue_date) AS oldest
		 FROM tenant_sales s JOIN tenant_document_series ds ON ds.id = s.series_id
		 WHERE s.deleted_at IS NULL AND ds.sunat_code IN ? AND s.issue_date < ?
		   AND COALESCE(NULLIF(s.billing_status,''),'pending') IN ('pending','sent','error','rejected')
		 GROUP BY 1`,
	}
	codes := [][]string{saleDocCodes}
	if db.Migrator().HasTable("tenant_despatches") {
		queries = append(queries,
			`SELECT COALESCE(NULLIF(d.status,''),'pending') AS st, COUNT(*) AS cnt, MIN(d.issue_date) AS oldest
			 FROM tenant_despatches d JOIN tenant_document_series ds ON ds.id = d.series_id
			 WHERE ds.sunat_code IN ? AND d.issue_date < ?
			   AND COALESCE(NULLIF(d.status,''),'pending') IN ('pending','sent','error','rejected')
			 GROUP BY 1`)
		codes = append(codes, despatchDocCodes)
	}
	for i, q := range queries {
		rows, err := db.Raw(q, codes[i], before).Rows()
		if err != nil {
			return agg, err
		}
		for rows.Next() {
			var st string
			var cnt int
			var oldest sql.NullString
			if err := rows.Scan(&st, &cnt, &oldest); err != nil {
				rows.Close()
				return agg, err
			}
			switch st {
			case "pending":
				agg.Pending += cnt
			case "sent":
				agg.Sent += cnt
			case "error":
				agg.Error += cnt
			case "rejected":
				agg.Rejected += cnt
			}
			if (st == "pending" || st == "error") && oldest.Valid && len(oldest.String) >= 10 {
				if d, perr := time.Parse(dateLayout, oldest.String[:10]); perr == nil &&
					(agg.Oldest == nil || d.Before(*agg.Oldest)) {
					dd := d
					agg.Oldest = &dd
				}
			}
		}
		rows.Close()
	}
	return agg, nil
}
