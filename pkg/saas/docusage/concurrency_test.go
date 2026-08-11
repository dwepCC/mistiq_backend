package docusage

import (
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestConcurrentReserve_100goroutines_5quota valida que no hay sobreconsumo bajo contención.
func TestConcurrentReserve_100goroutines_5quota(t *testing.T) {
	dsn := os.Getenv("DOCUSAGE_MYSQL_DSN")
	useMySQL := dsn != ""
	if !useMySQL {
		dsn = "file:docusage_conc_test.db?_journal_mode=WAL&_busy_timeout=15000"
		defer os.Remove("docusage_conc_test.db")
	}

	var db *gorm.DB
	var err error
	if useMySQL {
		db, err = gorm.Open(mysqldriver.Open(dsn), &gorm.Config{})
	} else {
		db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	}
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	database.CentralDB = db

	if err := db.AutoMigrate(
		&database.SaasPlan{},
		&database.SaasSubscription{},
		&database.SaasBillingCycle{},
		&database.SaasElectronicDocumentUsage{},
		&database.SaasDocumentPackage{},
		&database.SaasTenantDocumentPackage{},
		&database.SaasDocumentQuotaPeriod{},
		&database.Tenant{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if useMySQL {
		if err := MigrateBillingCycleConstraints(); err != nil {
			t.Fatalf("constraints: %v", err)
		}
	}

	db.Exec("DELETE FROM saas_electronic_document_usages")
	db.Exec("DELETE FROM saas_document_quota_periods")
	db.Exec("DELETE FROM saas_billing_cycles")
	db.Exec("DELETE FROM saas_subscriptions")
	db.Exec("DELETE FROM saas_plans")
	db.Exec("DELETE FROM tenants")

	plan := database.SaasPlan{
		Name: "ConcTest", Price: 10, BillingCycle: "monthly", Active: true,
		MonthlyDocumentsLimit: 5,
	}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	tenant := database.Tenant{Slug: "conc-test", DBName: "saas_tenant_conc", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(30 * 24 * time.Hour)
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: "monthly",
		StartDate: time.Now(), EndDate: end, Status: database.SaasSubActive,
	}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	cycle, err := EnsureBillingCycleForSubscription(&sub)
	if err != nil || cycle == nil {
		t.Fatalf("cycle: %v", err)
	}
	if cycle.DocumentsLimit != 5 {
		t.Fatalf("expected limit 5, got %d", cycle.DocumentsLimit)
	}

	const workers = 100
	var okCount int32
	var wg sync.WaitGroup
	parallel := 25
	if !useMySQL {
		parallel = 8
	}
	sem := make(chan struct{}, parallel)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var err error
			for attempt := 0; attempt < 12; attempt++ {
				err = ReserveElectronicDocument(ReserveInput{
					TenantID:     tenant.ID,
					DocumentType: "invoice",
					DocumentID:   uint(i + 1),
					Source:       "sync",
				})
				if err == nil || errors.Is(err, ErrQuotaExceeded) {
					break
				}
				if isSQLiteBusy(err) {
					time.Sleep(time.Duration(attempt+1) * 4 * time.Millisecond)
					continue
				}
				break
			}
			if err == nil {
				atomic.AddInt32(&okCount, 1)
			} else if !errors.Is(err, ErrQuotaExceeded) && !isSQLiteBusy(err) {
				t.Errorf("unexpected error doc %d: %v", i+1, err)
			}
		}()
	}
	wg.Wait()

	if int(okCount) != 5 {
		t.Fatalf("expected exactly 5 successes, got %d (use DOCUSAGE_MYSQL_DSN for MySQL stress)", okCount)
	}

	// El cupo se descuenta del período mensual, no del ciclo de cobro.
	var periods []database.SaasDocumentQuotaPeriod
	if err := db.Where("subscription_id = ?", sub.ID).Find(&periods).Error; err != nil {
		t.Fatal(err)
	}
	if len(periods) != 1 {
		t.Fatalf("se esperaba 1 período de cuota, hay %d", len(periods))
	}
	p := periods[0]
	if p.DocumentsUsed != 5 {
		t.Fatalf("documents_used want 5, got %d", p.DocumentsUsed)
	}
	if p.DocumentsUsed < 0 || p.DocumentsLimit < 0 {
		t.Fatal("negative counters")
	}

	var usageCount int64
	db.Model(&database.SaasElectronicDocumentUsage{}).Where("tenant_id = ?", tenant.ID).Count(&usageCount)
	if usageCount != 5 {
		t.Fatalf("usage rows want 5, got %d", usageCount)
	}
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "deadlocked")
}
