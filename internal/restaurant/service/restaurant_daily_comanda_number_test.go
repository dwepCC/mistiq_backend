package service

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupDailyComandaCounterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantBranchDailyComandaCounter{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func reserveInTx(t *testing.T, db *gorm.DB, branchID uint, at time.Time) int {
	t.Helper()
	var got int
	if err := db.Transaction(func(tx *gorm.DB) error {
		n, err := reserveDailyComandaNumber(tx, branchID, at)
		got = n
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReserveDailyComandaNumber_incrementsWithinSameDay(t *testing.T) {
	db := setupDailyComandaCounterTestDB(t)
	day := time.Date(2026, 8, 25, 10, 0, 0, 0, time.Local)

	for i, want := range []int{1, 2, 3} {
		if got := reserveInTx(t, db, 1, day); got != want {
			t.Fatalf("intento %d: got %d want %d", i, got, want)
		}
	}
}

// El bug reportado: la mesa se cierra y se reabre (nueva TenantTableSession), y el número volvía
// a 1 porque el cálculo anterior era MAX(order_number)+1 por session_id. El contador por
// sucursal+día no sabe nada de sesiones de mesa, así que no debe reiniciarse acá.
func TestReserveDailyComandaNumber_doesNotResetOnTableReopen_sameDaySameBranch(t *testing.T) {
	db := setupDailyComandaCounterTestDB(t)
	day := time.Date(2026, 8, 25, 10, 0, 0, 0, time.Local)

	if got := reserveInTx(t, db, 1, day); got != 1 {
		t.Fatalf("primer pedido del día: got %d want 1", got)
	}
	// Simula: se cierra la mesa y se abre otra vez (o se abre otra mesa) más tarde el mismo día.
	if got := reserveInTx(t, db, 1, day.Add(2*time.Hour)); got != 2 {
		t.Fatalf("pedido tras reabrir mesa: got %d want 2 (no debe reiniciar)", got)
	}
}

func TestReserveDailyComandaNumber_resetsOnNewDay(t *testing.T) {
	db := setupDailyComandaCounterTestDB(t)
	day1 := time.Date(2026, 8, 25, 23, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 8, 26, 0, 5, 0, 0, time.Local)

	if n := reserveInTx(t, db, 1, day1); n != 1 {
		t.Fatalf("día 1, primer pedido: got %d want 1", n)
	}
	if n := reserveInTx(t, db, 1, day1); n != 2 {
		t.Fatalf("día 1, segundo pedido: got %d want 2", n)
	}
	if n := reserveInTx(t, db, 1, day2); n != 1 {
		t.Fatalf("día 2 debe reiniciar en 1, got %d", n)
	}
}

func TestReserveDailyComandaNumber_independentPerBranch(t *testing.T) {
	db := setupDailyComandaCounterTestDB(t)
	day := time.Date(2026, 8, 25, 10, 0, 0, 0, time.Local)

	if n := reserveInTx(t, db, 1, day); n != 1 {
		t.Fatalf("sucursal 1, primer pedido: got %d want 1", n)
	}
	if n := reserveInTx(t, db, 2, day); n != 1 {
		t.Fatalf("sucursal 2 debe tener su propio contador, got %d want 1", n)
	}
	if n := reserveInTx(t, db, 1, day); n != 2 {
		t.Fatalf("sucursal 1, segundo pedido: got %d want 2", n)
	}
}

// Dos mesas de la misma sucursal pidiendo "al mismo tiempo" (transacciones concurrentes) nunca
// deben terminar con el mismo número — justo el escenario que un MAX() recalculado sin lock
// dedicado sí podría duplicar.
func TestReserveDailyComandaNumber_concurrentNoDuplicates(t *testing.T) {
	db := setupDailyComandaCounterTestDB(t)
	day := time.Date(2026, 8, 25, 10, 0, 0, 0, time.Local)

	const workers = 12
	var wg sync.WaitGroup
	results := make(chan int, workers)
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 20; attempt++ {
				var n int
				err := db.Transaction(func(tx *gorm.DB) error {
					var innerErr error
					n, innerErr = reserveDailyComandaNumber(tx, 1, day)
					return innerErr
				})
				if err == nil {
					results <- n
					return
				}
				msg := strings.ToLower(err.Error())
				if strings.Contains(msg, "locked") || strings.Contains(msg, "deadlock") || strings.Contains(msg, "busy") {
					time.Sleep(time.Millisecond * time.Duration(attempt+1))
					continue
				}
				errs <- err
				return
			}
			errs <- errors.New("se agotaron los reintentos")
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := make(map[int]bool, workers)
	for n := range results {
		if seen[n] {
			t.Fatalf("número duplicado bajo concurrencia: %d", n)
		}
		seen[n] = true
	}
	if len(seen) != workers {
		t.Fatalf("esperaba %d números únicos, got %d", workers, len(seen))
	}
}
