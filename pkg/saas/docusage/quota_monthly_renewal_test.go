package docusage

import (
	"os"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupQuotaDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Un fichero por test y cerrando la conexión al terminar: compartir nombre hacía que
	// en Windows el borrado fallara con la conexión abierta y el test siguiente
	// heredara los datos del anterior.
	file := "docusage_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + ".db"
	_ = os.Remove(file)
	db, err := gorm.Open(sqlite.Open("file:"+file+"?_journal_mode=WAL&_busy_timeout=15000"),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
		_ = os.Remove(file)
		_ = os.Remove(file + "-wal")
		_ = os.Remove(file + "-shm")
	})

	if err := db.AutoMigrate(
		&database.SaasPlan{},
		&database.SaasSubscription{},
		&database.SaasBillingCycle{},
		&database.SaasElectronicDocumentUsage{},
		&database.SaasDocumentPackage{},
		&database.SaasTenantDocumentPackage{},
		&database.SaasDocumentQuotaPeriod{},
		&database.SaasPlatformSettings{},
		&database.Tenant{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.CentralDB = db
	return db
}

// El escenario reportado: plan de 200 comprobantes al mes contratado por 6 meses. Antes
// los 200 se repartían entre los 6 meses (≈33/mes); ahora cada mes debe traer 200
// nuevos.
func TestCuotaMensualSeRenuevaEnPlanSemestral(t *testing.T) {
	db := setupQuotaDB(t)

	const limiteMensual = 200
	plan := database.SaasPlan{
		Name: "Semestral 200", Price: 100, BillingCycle: "semiannual", Active: true,
		MonthlyDocumentsLimit: limiteMensual,
	}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	tenant := database.Tenant{Slug: "semestral", DBName: "t_semestral", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}

	inicio := day(2026, time.August, 3)
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: "semiannual",
		StartDate: inicio, EndDate: endOfDay(inicio.AddDate(0, 6, 0)),
		Status: database.SaasSubActive,
	}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	cycle, err := EnsureBillingCycleForSubscription(&sub)
	if err != nil || cycle == nil {
		t.Fatalf("cycle: %v", err)
	}

	// Mes 1: cupo completo.
	p1 := ensurePeriodAt(t, db, &sub, cycle, inicio)
	if p1.DocumentsLimit != limiteMensual {
		t.Fatalf("mes 1: límite %d, se esperaban %d", p1.DocumentsLimit, limiteMensual)
	}
	if p1.PeriodIndex != 1 {
		t.Fatalf("mes 1: índice %d", p1.PeriodIndex)
	}

	// Se agota el mes 1.
	if err := db.Model(p1).Update("documents_used", limiteMensual).Error; err != nil {
		t.Fatal(err)
	}

	// Mes 2: período nuevo, cupo completo otra vez y contador en cero.
	p2 := ensurePeriodAt(t, db, &sub, cycle, inicio.AddDate(0, 1, 0))
	if p2.ID == p1.ID {
		t.Fatal("el mes 2 reutilizó el período del mes 1: el cupo no se renovó")
	}
	if p2.DocumentsLimit != limiteMensual {
		t.Errorf("mes 2: límite %d, se esperaban %d", p2.DocumentsLimit, limiteMensual)
	}
	if p2.DocumentsUsed != 0 {
		t.Errorf("mes 2: arranca con %d usados, se esperaba 0", p2.DocumentsUsed)
	}
	if p2.PeriodIndex != 2 {
		t.Errorf("mes 2: índice %d, se esperaba 2", p2.PeriodIndex)
	}

	// El semestre completo debe dar 6 × 200, no 200.
	total := TotalQuotaPeriods(&sub)
	if total != 6 {
		t.Fatalf("períodos del semestre = %d, se esperaban 6", total)
	}
	if got := total * limiteMensual; got != 1200 {
		t.Fatalf("cupo total del semestre = %d, se esperaban 1200", got)
	}

	// Y el saldo no consumido no se arrastra: el mes 2 no hereda nada del mes 1.
	if p2.DocumentsLimit != limiteMensual {
		t.Errorf("el mes 2 acumuló saldo del mes anterior: límite %d", p2.DocumentsLimit)
	}
}

// Reservar dos veces el mismo documento no debe consumir dos cupos.
func TestReservaEsIdempotente(t *testing.T) {
	db := setupQuotaDB(t)

	plan := database.SaasPlan{
		Name: "Mensual", Price: 50, BillingCycle: "monthly", Active: true,
		MonthlyDocumentsLimit: 10,
	}
	db.Create(&plan)
	tenant := database.Tenant{Slug: "idem", DBName: "t_idem", Status: "active"}
	db.Create(&tenant)
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: "monthly",
		StartDate: nowLima(), EndDate: endOfDay(nowLima().AddDate(0, 1, 0)),
		Status: database.SaasSubActive,
	}
	db.Create(&sub)
	if _, err := EnsureBillingCycleForSubscription(&sub); err != nil {
		t.Fatal(err)
	}

	in := ReserveInput{TenantID: tenant.ID, DocumentType: "invoice", DocumentID: 77, Source: "sync"}
	for i := 0; i < 3; i++ {
		if err := ReserveElectronicDocument(in); err != nil {
			t.Fatalf("intento %d: %v", i+1, err)
		}
	}

	var periods []database.SaasDocumentQuotaPeriod
	db.Where("subscription_id = ?", sub.ID).Find(&periods)
	if len(periods) != 1 {
		t.Fatalf("se esperaba 1 período, hay %d", len(periods))
	}
	if periods[0].DocumentsUsed != 1 {
		t.Fatalf("documents_used = %d, se esperaba 1", periods[0].DocumentsUsed)
	}
}

// El consumo debe quedar enlazado a su período para poder auditar cada mes.
func TestConsumoRegistraElPeriodo(t *testing.T) {
	db := setupQuotaDB(t)

	plan := database.SaasPlan{
		Name: "Mensual", Price: 50, BillingCycle: "monthly", Active: true,
		MonthlyDocumentsLimit: 10,
	}
	db.Create(&plan)
	tenant := database.Tenant{Slug: "link", DBName: "t_link", Status: "active"}
	db.Create(&tenant)
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: "monthly",
		StartDate: nowLima(), EndDate: endOfDay(nowLima().AddDate(0, 1, 0)),
		Status: database.SaasSubActive,
	}
	db.Create(&sub)
	if _, err := EnsureBillingCycleForSubscription(&sub); err != nil {
		t.Fatal(err)
	}
	if err := ReserveElectronicDocument(ReserveInput{
		TenantID: tenant.ID, DocumentType: "receipt", DocumentID: 5, Source: "sync",
	}); err != nil {
		t.Fatal(err)
	}

	var usage database.SaasElectronicDocumentUsage
	if err := db.Where("tenant_id = ?", tenant.ID).First(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if usage.QuotaPeriodID == 0 {
		t.Fatal("el consumo no quedó enlazado a ningún período de cuota")
	}
	var period database.SaasDocumentQuotaPeriod
	if err := db.First(&period, usage.QuotaPeriodID).Error; err != nil {
		t.Fatalf("el período referenciado no existe: %v", err)
	}
	if period.SubscriptionID != sub.ID {
		t.Errorf("el período pertenece a otra suscripción")
	}
}

func ensurePeriodAt(
	t *testing.T,
	db *gorm.DB,
	sub *database.SaasSubscription,
	cycle *database.SaasBillingCycle,
	at time.Time,
) *database.SaasDocumentQuotaPeriod {
	t.Helper()
	var out *database.SaasDocumentQuotaPeriod
	err := db.Transaction(func(tx *gorm.DB) error {
		p, e := ensureQuotaPeriodTx(tx, sub, cycle, at)
		out = p
		return e
	})
	if err != nil {
		t.Fatalf("ensureQuotaPeriodTx(%s): %v", at.Format("2006-01-02"), err)
	}
	return out
}
