package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestApplyPaymentToInstallments_FIFO(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:inst?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleCreditInstallment{}); err != nil {
		t.Fatal(err)
	}
	sale := database.TenantSale{Number: "B001-00000001", Total: 300, Status: "credit"}
	if err := db.Create(&sale).Error; err != nil {
		t.Fatal(err)
	}
	for i, days := range []int{10, 40, 70} {
		if err := db.Create(&database.TenantSaleCreditInstallment{
			SaleID: sale.ID, InstallmentNo: i + 1,
			DueDate: time.Now().AddDate(0, 0, days), Amount: 100, Status: InstallmentPending,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyPaymentToInstallmentsTx(tx, sale.ID, 150, 0)
	}); err != nil {
		t.Fatal(err)
	}
	var rows []database.TenantSaleCreditInstallment
	if err := db.Where("sale_id = ?", sale.ID).Order("installment_no").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows[0].Status != InstallmentPaid || rows[0].PaidAmount != 100 {
		t.Fatalf("cuota 1: %+v", rows[0])
	}
	if rows[1].Status != InstallmentPartial || rows[1].PaidAmount != 50 {
		t.Fatalf("cuota 2: %+v", rows[1])
	}
	if rows[2].PaidAmount != 0 {
		t.Fatalf("cuota 3: %+v", rows[2])
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyPaymentToInstallmentsTx(tx, sale.ID, 150, 0)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("sale_id = ?", sale.ID).Order("installment_no").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Status != InstallmentPaid {
			t.Fatalf("expected all paid, got %+v", r)
		}
	}
}

func TestApplyPaymentToInstallments_PreferID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:inst2?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleCreditInstallment{}); err != nil {
		t.Fatal(err)
	}
	sale := database.TenantSale{Number: "B001-00000002", Total: 200, Status: "credit"}
	if err := db.Create(&sale).Error; err != nil {
		t.Fatal(err)
	}
	i1 := database.TenantSaleCreditInstallment{
		SaleID: sale.ID, InstallmentNo: 1, DueDate: time.Now(), Amount: 100, Status: InstallmentPending,
	}
	i2 := database.TenantSaleCreditInstallment{
		SaleID: sale.ID, InstallmentNo: 2, DueDate: time.Now().AddDate(0, 1, 0), Amount: 100, Status: InstallmentPending,
	}
	if err := db.Create(&i1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&i2).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyPaymentToInstallmentsTx(tx, sale.ID, 100, i2.ID)
	}); err != nil {
		t.Fatal(err)
	}
	var rows []database.TenantSaleCreditInstallment
	if err := db.Where("sale_id = ?", sale.ID).Order("installment_no").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows[0].PaidAmount != 0 {
		t.Fatalf("cuota 1 should remain pending: %+v", rows[0])
	}
	if rows[1].Status != InstallmentPaid {
		t.Fatalf("cuota 2 should be paid: %+v", rows[1])
	}
}
