package service

import (
	"testing"

	"tukifac/pkg/paymentmethod"
)

func TestPopulateSessionReportSections_excludesDetractionFromCashAndElectronic(t *testing.T) {
	report := &SessionReport{
		IncomeDetail: []IncomeDetailRow{
			{Type: "venta", PaymentMethod: "efectivo", Amount: 1132.80},
			{Type: "venta", PaymentMethod: paymentmethod.CodeDetraccionBN, Amount: 47.20},
			{Type: "venta", PaymentMethod: "yape", Amount: 500},
		},
		Session: SessionReportHeader{OpeningBalance: 100},
		Totals:  SessionTotals{TotalIncome: 0, TotalExpense: 0, FinalBalance: 100},
	}
	populateSessionReportSections(report)
	if len(report.CashPhysical.CashSales) != 1 || report.CashPhysical.SalesTotal != 1132.80 {
		t.Fatalf("cash sales: %+v total=%v", report.CashPhysical.CashSales, report.CashPhysical.SalesTotal)
	}
	if len(report.Electronic.Sales) != 1 || report.Electronic.TotalSales != 500 {
		t.Fatalf("electronic: %+v total=%v", report.Electronic.Sales, report.Electronic.TotalSales)
	}
}

func TestSessionTotals_directVsSpot(t *testing.T) {
	totals := SessionTotals{
		TotalSales:           1132.80,
		TotalSalesDirect:     1132.80,
		TotalDetractionSpot:  47.20,
		TotalSalesCommercial: 1180,
	}
	if totals.TotalSalesCommercial != totals.TotalSalesDirect+totals.TotalDetractionSpot {
		t.Fatal("commercial should equal direct + spot")
	}
}
