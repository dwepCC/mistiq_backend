package prepayment

import (
	"testing"
	"time"

	"tukifac/pkg/database"
	sunatpre "tukifac/pkg/sunat/prepayment"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestReconcileVouchersForContact_opensWhenBillingAccepted(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:prepay_reconcile?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSalePrepaymentVoucher{}); err != nil {
		t.Fatal(err)
	}
	contactID := uint(10)
	sale := database.TenantSale{
		ID: 50, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "BOLETA",
		ContactID: &contactID, Number: "B001-99", Total: 118, BillingStatus: "accepted",
		IssueDate: time.Now(),
	}
	if err := db.Create(&sale).Error; err != nil {
		t.Fatal(err)
	}
	voucher := database.TenantSalePrepaymentVoucher{
		SaleID:            sale.ID,
		ContactID:         &contactID,
		SunatDocCode:      "03",
		DocumentNumber:    "B001-99",
		OperationTypeCode: "0101",
		AffectationGroup:  sunatpre.AffectationGravado,
		RelatedDocType:    "03",
		OriginalAmount:    118,
		BalanceAmount:     118,
		Currency:          "PEN",
		Status:            sunatpre.StatusPendingAcceptance,
	}
	if err := db.Create(&voucher).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewService(db)
	if err := svc.ReconcileAllPendingVouchers(); err != nil {
		t.Fatal(err)
	}
	var row database.TenantSalePrepaymentVoucher
	if err := db.First(&row, "sale_id = ?", sale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != sunatpre.StatusOpen {
		t.Fatalf("expected open, got %s", row.Status)
	}
}
