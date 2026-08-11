package database

import (
	"testing"

	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/taxpayment"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openFinancialTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TenantPaymentMethod{}, &TenantPaymentCondition{}, &TenantTaxPaymentType{}, &TenantBankAccount{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSplitFinancialDomainsFromLegacy(t *testing.T) {
	db := openFinancialTestDB(t)
	db.Create(&TenantPaymentMethod{Code: "cash", Name: "Efectivo", DestinationType: "cash", IsSystem: true, Active: true})
	db.Create(&TenantPaymentMethod{Code: taxpayment.CodeDetraccionBN, Name: taxpayment.NameDetraccionBN, DestinationType: "detraction", IsSystem: true, Active: true})
	db.Create(&TenantPaymentMethod{Code: "credito", Name: paymentcondition.NameCredit, DestinationType: "receivable", IsSystem: true, Active: true})

	if err := SplitFinancialDomainsFromLegacy(db); err != nil {
		t.Fatal(err)
	}

	var orphan int64
	db.Model(&TenantPaymentMethod{}).Where("code IN ?", []string{"credito", taxpayment.CodeDetraccionBN}).Count(&orphan)
	if orphan != 0 {
		t.Fatalf("orphan rows in payment_methods: %d", orphan)
	}
	var cond, tax int64
	db.Model(&TenantPaymentCondition{}).Where("code = ?", paymentcondition.CodeCredit).Count(&cond)
	db.Model(&TenantTaxPaymentType{}).Where("code = ?", taxpayment.CodeDetraccionBN).Count(&tax)
	if cond != 1 || tax != 1 {
		t.Fatalf("cond=%d tax=%d", cond, tax)
	}

	audit, err := AuditFinancialCatalog(db)
	if err != nil {
		t.Fatal(err)
	}
	if !audit.OK {
		t.Fatalf("audit not ok: %+v", audit)
	}
}

func TestSeedFinancialCatalog_idempotent(t *testing.T) {
	db := openFinancialTestDB(t)
	if err := SeedFinancialCatalog(db); err != nil {
		t.Fatal(err)
	}
	var c1 int64
	db.Model(&TenantPaymentMethod{}).Count(&c1)
	if err := SeedFinancialCatalog(db); err != nil {
		t.Fatal(err)
	}
	var c2 int64
	db.Model(&TenantPaymentMethod{}).Count(&c2)
	if c1 != c2 || c1 != 5 {
		t.Fatalf("methods count=%d want 5 stable", c2)
	}
}
