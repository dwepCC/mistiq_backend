package service

import (
	"fmt"
	"testing"

	cashbanksvc "tukifac/internal/cashbank/service"
	"tukifac/pkg/database"
	"tukifac/pkg/salecurrency"
	sunatdet "tukifac/pkg/sunat/detraccion"
	"tukifac/pkg/taxpayment"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func detractionEval1180(t *testing.T) sunatdet.CalcResult {
	t.Helper()
	cat, err := sunatdet.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	res, err := sunatdet.Evaluate(cat, sunatdet.CalcInput{
		OperationTypeCode: salecurrency.OpDetraccion,
		SunatDocCode:      "01",
		Currency:          salecurrency.CurrencyPEN,
		GravadoTotalPEN:   1180,
		SaleTotalPEN:      1180,
		GoodCode:          "014",
		BankAccount:       "0004-1234567890",
		PaymentMethodCode: "001",
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPrepareDetractionSalePayments_cashContado1180(t *testing.T) {
	eval := detractionEval1180(t)
	out, err := PrepareDetractionSalePayments(
		[]PaymentInput{{Method: "cash", Amount: 1132.80}},
		1180,
		eval,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(out))
	}
	if out[0].Method != "cash" || out[0].Amount != 1132.80 {
		t.Fatalf("direct: %+v", out[0])
	}
	if out[1].Method != taxpayment.CodeDetraccionBN || out[1].Amount != 47.2 {
		t.Fatalf("detraction: %+v", out[1])
	}
}

func TestPrepareDetractionSalePayments_rejectsTotalAsDirect(t *testing.T) {
	eval := detractionEval1180(t)
	_, err := PrepareDetractionSalePayments(
		[]PaymentInput{{Method: "cash", Amount: 1180}},
		1180,
		eval,
	)
	if err == nil {
		t.Fatal("expected error when paying full invoice as direct")
	}
}

func TestPrepareDetractionSalePayments_rejectsUnderpay(t *testing.T) {
	eval := detractionEval1180(t)
	_, err := PrepareDetractionSalePayments(
		[]PaymentInput{{Method: "cash", Amount: 1000}},
		1180,
		eval,
	)
	if err == nil {
		t.Fatal("expected underpay error")
	}
}

func TestPrepareDetractionSalePayments_mixedDirectMethods(t *testing.T) {
	eval := detractionEval1180(t)
	out, err := PrepareDetractionSalePayments(
		[]PaymentInput{
			{Method: "cash", Amount: 500},
			{Method: "yape", Amount: 632.80},
		},
		1180,
		eval,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 lines, got %d: %+v", len(out), out)
	}
	if out[2].Method != taxpayment.CodeDetraccionBN {
		t.Fatalf("last line should be detraction: %+v", out[2])
	}
}

func TestPrepareDetractionSalePayments_stripsClientDetractionLine(t *testing.T) {
	eval := detractionEval1180(t)
	out, err := PrepareDetractionSalePayments(
		[]PaymentInput{
			{Method: "cash", Amount: 1132.80},
			{Method: taxpayment.CodeDetraccionBN, Amount: 99},
		},
		1180,
		eval,
	)
	if err != nil {
		t.Fatal(err)
	}
	var detCount int
	for _, p := range out {
		if taxpayment.IsDetractionCode(p.Method) {
			detCount++
			if p.Amount != 47.2 {
				t.Fatalf("expected server calc 47.20, got %v", p.Amount)
			}
		}
	}
	if detCount != 1 {
		t.Fatalf("expected exactly one detraction line, got %d", detCount)
	}
}

func TestPrimaryDirectPaymentMethod_skipsDetraction(t *testing.T) {
	got := PrimaryDirectPaymentMethod([]PaymentInput{
		{Method: taxpayment.CodeDetraccionBN, Amount: 47.2},
		{Method: "yape", Amount: 1132.80},
	}, "cash")
	if got != "yape" {
		t.Fatalf("expected yape, got %q", got)
	}
}

func setupDetractionPaymentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantPaymentMethod{},
		&database.TenantTaxPaymentType{},
		&database.TenantCashSession{},
		&database.TenantCashMovement{},
		&database.TenantBankMovement{},
		&database.TenantBankAccount{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.EnsureDetractionTaxPaymentType(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{
		Name: "Efectivo", Code: "cash", DestinationType: "cash", IsSystem: true, SortOrder: 0, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPrepareDetractionSalePaymentsAllowCredit_fullCreditSpotOnly(t *testing.T) {
	eval := detractionEval1180(t)
	out, isCredit, err := PrepareDetractionSalePaymentsAllowCredit(nil, 1180, eval)
	if err != nil {
		t.Fatal(err)
	}
	if !isCredit {
		t.Fatal("expected credit")
	}
	if len(out) != 1 || out[0].Method != taxpayment.CodeDetraccionBN {
		t.Fatalf("expected spot only: %+v", out)
	}
}

func TestEnsureDetractionTaxPaymentType_idempotent(t *testing.T) {
	db := setupDetractionPaymentTestDB(t)
	if err := database.EnsureDetractionTaxPaymentType(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&database.TenantTaxPaymentType{}).Where("code = ?", taxpayment.CodeDetraccionBN).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
	var row database.TenantTaxPaymentType
	if err := db.Where("code = ?", taxpayment.CodeDetraccionBN).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Name != taxpayment.NameDetraccionBN {
		t.Fatalf("unexpected: %+v", row)
	}
}

func TestRecordPayment_skipsDetractionBN(t *testing.T) {
	db := setupDetractionPaymentTestDB(t)
	sess := database.TenantCashSession{BranchID: 1, UserID: 1, Status: "open", OpeningBalance: 0}
	if err := db.Create(&sess).Error; err != nil {
		t.Fatal(err)
	}
	sid := sess.ID
	svc := cashbanksvc.NewCashBankService(db)
	if err := svc.RecordPayment(nil, taxpayment.CodeDetraccionBN, 47.2, &sid, "F001-1", "Venta", nil, 1); err != nil {
		t.Fatal(err)
	}
	var cashCount int64
	db.Model(&database.TenantCashMovement{}).Count(&cashCount)
	if cashCount != 0 {
		t.Fatalf("expected no cash movement, got %d", cashCount)
	}
	var bankCount int64
	db.Model(&database.TenantBankMovement{}).Count(&bankCount)
	if bankCount != 0 {
		t.Fatalf("expected no bank movement, got %d", bankCount)
	}
}

func TestRecordPayment_cashStillWorks(t *testing.T) {
	db := setupDetractionPaymentTestDB(t)
	sess := database.TenantCashSession{BranchID: 1, UserID: 1, Status: "open", OpeningBalance: 0}
	if err := db.Create(&sess).Error; err != nil {
		t.Fatal(err)
	}
	sid := sess.ID
	svc := cashbanksvc.NewCashBankService(db)
	if err := svc.RecordPayment(nil, "cash", 100, &sid, "F001-1", "Venta", nil, 1); err != nil {
		t.Fatal(err)
	}
	var cashCount int64
	db.Model(&database.TenantCashMovement{}).Count(&cashCount)
	if cashCount != 1 {
		t.Fatalf("expected 1 cash movement, got %d", cashCount)
	}
}
