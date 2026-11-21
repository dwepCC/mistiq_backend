package database

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"tukifac/config"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Regresión OOM 14-oct-2026: abrir y cerrar pools de tenant (LRU, idle TTL, conexiones efímeras de los
// barridos) no debe dejar goroutines vivas. Con PrepareStmt=true gorm v1.31 fugaba una por apertura.
func TestTenantGormConfigNoGoroutineLeak(t *testing.T) {
	const opens = 100
	runtime.GC()
	before := runtime.NumGoroutine()
	for i := 0; i < opens; i++ {
		cfg := tenantGormConfig()
		cfg.DisableAutomaticPing = true // sin MySQL real: solo importa lo que gorm.Open deja corriendo
		db, err := gorm.Open(mysql.New(mysql.Config{DSN: "u:p@tcp(127.0.0.1:1)/saas_tenant_x", SkipInitializeWithVersion: true}), cfg)
		if err != nil {
			t.Fatal(err)
		}
		closeDB(db)
	}
	deadline := time.Now().Add(2 * time.Second)
	after := runtime.NumGoroutine()
	for after > before+5 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before+5 {
		t.Fatalf("goroutines %d -> %d tras %d aperturas/cierres: tenantGormConfig fuga goroutines (¿PrepareStmt activado?)", before, after, opens)
	}
}

func TestTenantDSNInterpolatesParams(t *testing.T) {
	prev := config.AppConfig
	config.AppConfig = &config.Config{DBUser: "u", DBPassword: "p", DBHost: "h", DBPort: "3306"}
	defer func() { config.AppConfig = prev }()

	dsn := tenantDSN("saas_tenant_x")
	for _, want := range []string{"/saas_tenant_x?", "interpolateParams=true", "charset=utf8mb4", "parseTime=True"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q no contiene %q", dsn, want)
		}
	}
}
