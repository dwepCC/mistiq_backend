package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// seedNotaCredito crea una NV PEN a crédito (total 118) con las cuotas y abonos indicados.
func seedNotaCredito(t *testing.T, db *gorm.DB, insts []database.TenantSaleCreditInstallment, pays []database.TenantSalePayment) (notaID, boletaSeriesID uint) {
	t.Helper()
	if err := db.AutoMigrate(&database.TenantSaleCreditInstallment{}); err != nil {
		t.Fatal(err)
	}
	nvSeries := database.TenantDocumentSeries{BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta", Series: "NV001", Correlative: 1, Active: true}
	boleta := database.TenantDocumentSeries{BranchID: 1, DocType: "BOLETA", SunatCode: "03", Category: "venta", Series: "B001", Correlative: 1, Active: true}
	if err := db.Create(&nvSeries).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&boleta).Error; err != nil {
		t.Fatal(err)
	}
	nota := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: nvSeries.ID, DocType: "NOTA DE VENTA",
		Series: "NV001", Correlative: 1, Number: "NV001-00000001",
		IssueDate: time.Now().AddDate(0, 0, -10), Subtotal: 100, TaxAmount: 18, Total: 118,
		Currency: "PEN", Status: "credit", PaymentConditionCode: "credit", PaymentMethod: "credit",
	}
	if err := db.Create(&nota).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSaleItem{
		SaleID: nota.ID, Code: "P1", Description: "Producto", Unit: "NIU",
		Quantity: 1, UnitPrice: 100, Subtotal: 100, TaxAmount: 18, Total: 118, IgvAffectationType: "10",
	}).Error; err != nil {
		t.Fatal(err)
	}
	for i := range insts {
		insts[i].SaleID = nota.ID
		if err := db.Create(&insts[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := range pays {
		pays[i].SaleID = nota.ID
		if err := db.Create(&pays[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	return nota.ID, boleta.ID
}

func TestIssueElectronicFromNota_creditCopiesInstallments(t *testing.T) {
	db := setupIssueElectronicFromNotaTestDB(t)
	future := time.Now().AddDate(0, 0, 20)
	past := time.Now().AddDate(0, 0, -3) // ya vencida: debe moverse al día siguiente a la emisión
	notaID, seriesID := seedNotaCredito(t, db, []database.TenantSaleCreditInstallment{
		{InstallmentNo: 1, DueDate: past, Amount: 58, Currency: "PEN", Status: "pending"},
		{InstallmentNo: 2, DueDate: future, Amount: 60, Currency: "PEN", Status: "pending"},
	}, nil)

	svc := NewSaleService(db)
	child, err := svc.IssueElectronicFromNota(notaID, seriesID, 1, "", 0, nil)
	if err != nil {
		t.Fatalf("la NV a crédito debe convertirse: %v", err)
	}
	if child.Status != "credit" || child.PaymentConditionCode != "credit" {
		t.Fatalf("comprobante debe nacer a crédito: status=%q cond=%q", child.Status, child.PaymentConditionCode)
	}
	insts, err := svc.GetCreditInstallments(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 2 || insts[0].Amount != 58 || insts[1].Amount != 60 {
		t.Fatalf("cuotas copiadas: %+v", insts)
	}
	if !insts[0].DueDate.After(child.IssueDate) {
		t.Fatalf("la cuota vencida debe quedar posterior a la emisión: %v vs %v", insts[0].DueDate, child.IssueDate)
	}
	if insts[1].DueDate.Format("2006-01-02") != future.Format("2006-01-02") {
		t.Fatalf("la cuota futura conserva su fecha: %v", insts[1].DueDate)
	}
	pays, _ := svc.GetPayments(child.ID)
	for _, p := range pays {
		if p.Method == "credit" {
			t.Fatalf("no debe inyectarse pago sintético credit: %+v", pays)
		}
	}
	// La NV no se toca: sigue como historial, con sus cuotas.
	var nota database.TenantSale
	db.First(&nota, notaID)
	if nota.Status != "credit" {
		t.Fatalf("la NV no se anula ni cambia de estado: %q", nota.Status)
	}
	orig, _ := svc.GetCreditInstallments(notaID)
	if len(orig) != 2 {
		t.Fatalf("las cuotas de la NV deben conservarse: %d", len(orig))
	}
}

func TestIssueElectronicFromNota_creditWithPartialPayment(t *testing.T) {
	db := setupIssueElectronicFromNotaTestDB(t)
	f1 := time.Now().AddDate(0, 0, 10)
	f2 := time.Now().AddDate(0, 0, 30)
	// abono de 40 aplicado a la cuota 1 (amount 59, pagado 40 → pendiente 19); cuota 2 pendiente 59
	notaID, seriesID := seedNotaCredito(t, db, []database.TenantSaleCreditInstallment{
		{InstallmentNo: 1, DueDate: f1, Amount: 59, PaidAmount: 40, Currency: "PEN", Status: "partial"},
		{InstallmentNo: 2, DueDate: f2, Amount: 59, Currency: "PEN", Status: "pending"},
	}, []database.TenantSalePayment{{Method: "cash", Amount: 40}})

	svc := NewSaleService(db)
	child, err := svc.IssueElectronicFromNota(notaID, seriesID, 1, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	insts, _ := svc.GetCreditInstallments(child.ID)
	if len(insts) != 2 || insts[0].Amount != 19 || insts[1].Amount != 59 {
		t.Fatalf("solo debe trasladarse lo pendiente: %+v", insts)
	}
	pays, _ := svc.GetPayments(child.ID)
	if len(pays) != 1 || pays[0].Method != "cash" || pays[0].Amount != 40 {
		t.Fatalf("el abono real se traslada como pago del comprobante: %+v", pays)
	}
	var movs int64
	if db.Migrator().HasTable("tenant_cash_movements") {
		db.Table("tenant_cash_movements").Count(&movs)
		if movs != 0 {
			t.Fatalf("no debe tocar caja: %d movimientos", movs)
		}
	}
}

func TestIssueElectronicFromNota_creditFullyPaidIssuesAsCash(t *testing.T) {
	db := setupIssueElectronicFromNotaTestDB(t)
	f1 := time.Now().AddDate(0, 0, 10)
	notaID, seriesID := seedNotaCredito(t, db, []database.TenantSaleCreditInstallment{
		{InstallmentNo: 1, DueDate: f1, Amount: 118, PaidAmount: 118, Currency: "PEN", Status: "paid"},
	}, []database.TenantSalePayment{{Method: "cash", Amount: 118}})

	svc := NewSaleService(db)
	child, err := svc.IssueElectronicFromNota(notaID, seriesID, 1, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != "paid" {
		t.Fatalf("NV ya saldada → comprobante pagado: %q", child.Status)
	}
}
