package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupVoidRejectedDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantCompanyConfig{}, &database.TenantDocumentSeries{},
		&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantSalePayment{},
		&database.TenantCashSession{}, &database.TenantCashMovement{}, &database.TenantPaymentMethod{},
		&database.TenantProduct{}, &database.TenantBranch{}, &database.TenantProductStock{},
		&database.TenantStockMovement{}, &database.TenantInventoryOperationType{},
		&database.TenantBankMovement{}, &database.TenantBankAccount{}, &database.TenantProductSerial{},
		&database.TenantInvoice{}, &database.TenantSalePrepaymentApplication{},
		&database.TenantSalePrepaymentVoucher{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"})
	return db
}

// seedRejectedBoleta deja una boleta cobrada en efectivo en una caja abierta, con una salida de
// kardex, y su registro fiscal con el estado indicado.
func seedRejectedBoleta(t *testing.T, db *gorm.DB, billingStatus string) *database.TenantSale {
	t.Helper()
	session := database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	sale := database.TenantSale{
		Number: "B002-00000999", Series: "B002", DocType: "BOLETA", BranchID: 1, UserID: 1,
		Total: 100, Status: "paid", BillingStatus: billingStatus, CashSessionID: &session.ID,
	}
	if err := db.Create(&sale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCashMovement{
		CashSessionID: session.ID, Type: "income", Amount: 100, PaymentMethod: "cash",
		Category: "Venta", Reference: "VENTA/" + sale.Number, SaleID: &sale.ID, UserID: 1,
		CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantStockMovement{
		ProductID: 7, BranchID: 1, Type: "out", Quantity: 3,
		Reference: "VENTA/" + sale.Number, UserID: 1, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantInvoice{
		SaleID: sale.ID, PipelineStatus: "SUNAT_REJECTED", SunatStatus: "rejected",
	}).Error; err != nil {
		t.Fatal(err)
	}
	return &sale
}

func stockInFor(db *gorm.DB, productID uint) float64 {
	var total float64
	db.Model(&database.TenantStockMovement{}).
		Where("product_id = ? AND type = ?", productID, "in").
		Select("COALESCE(SUM(quantity),0)").Scan(&total)
	return total
}

func TestVoidRejectedSale_revierteCajaStockYQuedaAnulada(t *testing.T) {
	db := setupVoidRejectedDB(t)
	sale := seedRejectedBoleta(t, db, "rejected")
	s := &BillingService{db: db}

	if err := s.VoidRejectedSale(VoidRejectedInput{SaleID: sale.ID, Reason: "numeración chocada", ActorID: 1}); err != nil {
		t.Fatalf("no debía fallar: %v", err)
	}

	var got database.TenantSale
	db.First(&got, sale.ID)
	if got.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
	// El estado fiscal NO se toca: sigue siendo la verdad sobre lo que dijo SUNAT.
	if got.BillingStatus != "rejected" {
		t.Errorf("billing_status = %q, debe seguir rejected", got.BillingStatus)
	}
	if !strings.Contains(got.Notes, "Comprobante rechazado por SUNAT") || !strings.Contains(got.Notes, "numeración chocada") {
		t.Errorf("la nota de anulación no recoge el motivo: %q", got.Notes)
	}
	if in := stockInFor(db, 7); in != 3 {
		t.Errorf("stock repuesto = %v, want 3", in)
	}
	var expenses int64
	db.Model(&database.TenantCashMovement{}).
		Where("sale_id = ? AND type = ?", sale.ID, "expense").Count(&expenses)
	if expenses != 1 {
		t.Errorf("reversiones de caja = %d, want 1", expenses)
	}
}

func TestVoidRejectedSale_devuelveAnticipoDeducido(t *testing.T) {
	db := setupVoidRejectedDB(t)
	sale := seedRejectedBoleta(t, db, "rejected")
	voucher := database.TenantSalePrepaymentVoucher{SaleID: 500, OriginalAmount: 100, BalanceAmount: 70}
	if err := db.Create(&voucher).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePrepaymentApplication{
		ConsumerSaleID: sale.ID, SourceSaleID: 500, Total: 30,
	}).Error; err != nil {
		t.Fatal(err)
	}
	s := &BillingService{db: db}
	if err := s.VoidRejectedSale(VoidRejectedInput{SaleID: sale.ID, Reason: "x", ActorID: 1}); err != nil {
		t.Fatalf("no debía fallar: %v", err)
	}
	var v database.TenantSalePrepaymentVoucher
	db.First(&v, "sale_id = ?", voucher.SaleID)
	if v.BalanceAmount != 100 {
		t.Errorf("saldo del anticipo = %v, want 100", v.BalanceAmount)
	}
}

func TestVoidRejectedSale_rechazaLoQueNoEstaRechazado(t *testing.T) {
	db := setupVoidRejectedDB(t)
	sale := seedRejectedBoleta(t, db, "accepted")
	s := &BillingService{db: db}

	err := s.VoidRejectedSale(VoidRejectedInput{SaleID: sale.ID, Reason: "x", ActorID: 1})
	if err == nil || !strings.Contains(err.Error(), "no está rechazado") {
		t.Fatalf("una boleta aceptada no debe anularse así, got %v", err)
	}
	var got database.TenantSale
	db.First(&got, sale.ID)
	if got.Status == "cancelled" {
		t.Fatal("la venta aceptada quedó anulada")
	}
}

func TestVoidRejectedSale_exigeMotivo(t *testing.T) {
	db := setupVoidRejectedDB(t)
	sale := seedRejectedBoleta(t, db, "rejected")
	s := &BillingService{db: db}
	if err := s.VoidRejectedSale(VoidRejectedInput{SaleID: sale.ID, Reason: "   ", ActorID: 1}); err == nil {
		t.Fatal("sin motivo debe rechazarse")
	}
}

// Una NC rechazada no movió stock ni caja: Cancel le repondría stock que nunca salió.
func TestVoidRejectedSale_noAplicaANotasDeCredito(t *testing.T) {
	db := setupVoidRejectedDB(t)
	sale := seedRejectedBoleta(t, db, "rejected")
	db.Model(&database.TenantSale{}).Where("id = ?", sale.ID).Update("doc_type", "NOTA_CREDITO")
	s := &BillingService{db: db}
	if err := s.VoidRejectedSale(VoidRejectedInput{SaleID: sale.ID, Reason: "x", ActorID: 1}); err == nil {
		t.Fatal("una nota de crédito no debe anularse con esta acción")
	}
}

func TestVoidRejectedSale_noAnulaDosVeces(t *testing.T) {
	db := setupVoidRejectedDB(t)
	sale := seedRejectedBoleta(t, db, "rejected")
	s := &BillingService{db: db}
	if err := s.VoidRejectedSale(VoidRejectedInput{SaleID: sale.ID, Reason: "x", ActorID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.VoidRejectedSale(VoidRejectedInput{SaleID: sale.ID, Reason: "x", ActorID: 1}); err == nil {
		t.Fatal("la segunda anulación debe rechazarse")
	}
	if in := stockInFor(db, 7); in != 3 {
		t.Errorf("el stock se repuso más de una vez: %v", in)
	}
}

func TestVoidRejectedSale_bloqueaCreditoConCobrosPosteriores(t *testing.T) {
	db := setupVoidRejectedDB(t)
	sale := seedRejectedBoleta(t, db, "rejected")
	db.Model(&database.TenantSale{}).Where("id = ?", sale.ID).
		Updates(map[string]interface{}{"status": "credit", "payment_condition_code": "credit"})
	// Cobro registrado mucho después de la venta (CxC), no el adelanto del momento de vender.
	late := time.Now().Add(48 * time.Hour)
	if err := db.Create(&database.TenantSalePayment{SaleID: sale.ID, Method: "cash", Amount: 40, CreatedAt: late}).Error; err != nil {
		t.Fatal(err)
	}
	s := &BillingService{db: db}
	err := s.VoidRejectedSale(VoidRejectedInput{SaleID: sale.ID, Reason: "x", ActorID: 1})
	if err == nil || !strings.Contains(err.Error(), "cobros registrados") {
		t.Fatalf("debe bloquear por cobros posteriores, got %v", err)
	}
}
