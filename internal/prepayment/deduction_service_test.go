package prepayment

import (
	"testing"
	"time"

	"tukifac/pkg/database"
	sunatpre "tukifac/pkg/sunat/prepayment"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestListOpenVouchers_listsByAffectationWithoutContactFilter(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:prepay_list?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantSale{},
		&database.TenantContact{},
		&database.TenantSalePrepaymentVoucher{},
	); err != nil {
		t.Fatal(err)
	}

	c1, c2 := uint(1), uint(2)
	now := time.Now()
	for _, tc := range []struct {
		saleID  uint
		contact uint
		doc     string
		balance float64
	}{
		{10, c1, "F001-1", 100},
		{20, c2, "F001-2", 200},
	} {
		contact := tc.contact
		sale := database.TenantSale{
			ID: tc.saleID, BranchID: 1, UserID: 1, SeriesID: 1, DocType: "FACTURA",
			ContactID: &contact, Number: tc.doc, Total: tc.balance, BillingStatus: "accepted",
			IssueDate: now,
		}
		if err := db.Create(&sale).Error; err != nil {
			t.Fatal(err)
		}
		voucher := database.TenantSalePrepaymentVoucher{
			SaleID:            sale.ID,
			ContactID:         &contact,
			SunatDocCode:      "01",
			DocumentNumber:    tc.doc,
			OperationTypeCode: "0101",
			AffectationGroup:  sunatpre.AffectationGravado,
			RelatedDocType:    "02",
			OriginalAmount:    tc.balance,
			BalanceAmount:     tc.balance,
			Currency:          "PEN",
			Status:            sunatpre.StatusOpen,
			AvailableAt:       &now,
		}
		if err := db.Create(&voucher).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(db)
	rows, err := svc.ListOpenVouchers(0, sunatpre.AffectationGravado, 18)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 open vouchers (PHP no filtra cliente), got %d", len(rows))
	}
}
