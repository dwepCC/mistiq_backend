package worker

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPendingCandidatesSoloElectronicasAbiertasYAntiguas(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:resend_candidates?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE tenant_document_series (id INTEGER PRIMARY KEY, sunat_code TEXT)`,
		`CREATE TABLE tenant_sales (id INTEGER PRIMARY KEY AUTOINCREMENT, series_id INTEGER, billing_status TEXT, created_at DATETIME, deleted_at DATETIME)`,
		`INSERT INTO tenant_document_series VALUES (1,'01'),(2,'03'),(3,'00')`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	recent := now.Add(-2 * time.Minute)
	add := func(series int, st string, at time.Time, deleted bool) {
		var del any
		if deleted {
			del = at
		}
		if err := db.Exec(`INSERT INTO tenant_sales (series_id, billing_status, created_at, deleted_at) VALUES (?,?,?,?)`, series, st, at, del).Error; err != nil {
			t.Fatal(err)
		}
	}
	add(1, "pending", old, false)    // 1: sí
	add(2, "error", old, false)      // 2: sí
	add(1, "", old, false)           // 3: billing_status vacío = pending, sí
	add(1, "accepted", old, false)   // 4: ya aceptada
	add(1, "rejected", old, false)   // 5: rechazada por SUNAT: no se reenvía
	add(1, "sent", old, false)       // 6: en tránsito
	add(3, "pending", old, false)    // 7: nota de venta (00): nunca
	add(1, "pending", recent, false) // 8: muy reciente
	add(1, "pending", old, true)     // 9: eliminada

	ids, total, err := pendingCandidates(db, now.Add(-ResendMinAge), 100)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("candidatos incorrectos: ids=%v total=%d", ids, total)
	}

	// Con tope menor, total sigue siendo 3 (lo que quedaría para otra pasada).
	ids, total, _ = pendingCandidates(db, now.Add(-ResendMinAge), 2)
	if len(ids) != 2 || total != 3 {
		t.Fatalf("con límite: ids=%v total=%d", ids, total)
	}
}
