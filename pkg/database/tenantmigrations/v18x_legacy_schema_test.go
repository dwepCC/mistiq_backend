package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Sobre un esquema ANTERIOR (sin las columnas nuevas) las migraciones agregan solo lo suyo y el backfill funciona.
func TestV182toV184_OnLegacySchemaAddOnlyNewColumns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE tenant_cash_movements (id INTEGER PRIMARY KEY AUTOINCREMENT, cash_session_id INTEGER NOT NULL, type TEXT NOT NULL, amount REAL NOT NULL, sale_id INTEGER, purchase_id INTEGER, user_id INTEGER NOT NULL, created_at DATETIME)`,
		`CREATE TABLE tenant_bank_movements (id INTEGER PRIMARY KEY AUTOINCREMENT, bank_account_id INTEGER NOT NULL, type TEXT NOT NULL, amount REAL NOT NULL, date DATETIME, user_id INTEGER NOT NULL, sale_id INTEGER, purchase_id INTEGER, created_at DATETIME)`,
		`CREATE TABLE tenant_bank_accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, bank_name TEXT, currency TEXT DEFAULT 'PEN', balance REAL DEFAULT 0, active NUMERIC DEFAULT 1, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`,
		`CREATE TABLE tenant_payment_methods (id INTEGER PRIMARY KEY AUTOINCREMENT, code TEXT, name TEXT, active NUMERIC DEFAULT 1, deleted_at DATETIME)`,
		`INSERT INTO tenant_bank_accounts (name, bank_name) VALUES ('Cta', 'BCP')`,
		`INSERT INTO tenant_bank_movements (bank_account_id, type, amount, date, user_id) VALUES (1, 'credit', 50, '2026-10-01', 1)`,
		`INSERT INTO tenant_payment_methods (code, name) VALUES ('cash', 'Efectivo')`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	// tablas pequeñas que las migraciones leen
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantPurchase{}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface {
		Up(*gorm.DB) error
	}{V197MovementCurrencySnapshot{}, V198AccountTransfers{}, V199BanksCatalog{}} {
		if err := m.Up(db); err != nil {
			t.Fatalf("%T: %v", m, err)
		}
	}
	mig := db.Migrator()
	for table, cols := range map[string][]string{
		"tenant_cash_movements":  {"currency", "exchange_rate", "amount_pen", "transfer_id"},
		"tenant_bank_movements":  {"currency", "exchange_rate", "amount_pen", "account_amount", "transfer_id"},
		"tenant_bank_accounts":   {"bank_id"},
		"tenant_payment_methods": {"applies_to", "requires_reference"},
	} {
		for _, c := range cols {
			if !mig.HasColumn(table, c) {
				t.Errorf("falta %s.%s", table, c)
			}
		}
	}
	var aa *float64
	db.Raw("SELECT account_amount FROM tenant_bank_movements WHERE id = 1").Scan(&aa)
	if aa == nil || *aa != 50 {
		t.Errorf("backfill de account_amount sobre esquema antiguo: %v", aa)
	}
	var bid *uint
	db.Raw("SELECT bank_id FROM tenant_bank_accounts WHERE id = 1").Scan(&bid)
	if bid == nil {
		t.Error("la cuenta con banco BCP debe enlazarse al catálogo (alias)")
	}
	var applies string
	db.Raw("SELECT applies_to FROM tenant_payment_methods WHERE id = 1").Scan(&applies)
	if applies != "both" {
		t.Errorf("applies_to de métodos antiguos = %q", applies)
	}
}
