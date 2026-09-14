package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupCancelDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{}, &database.TenantDocumentSeries{},
		&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantSalePayment{},
		&database.TenantCashSession{}, &database.TenantCashMovement{}, &database.TenantPaymentMethod{},
		&database.TenantProduct{}, &database.TenantBranch{}, &database.TenantProductStock{},
		&database.TenantStockMovement{}, &database.TenantInventoryOperationType{},
		&database.TenantBankMovement{}, &database.TenantBankAccount{}, &database.TenantProductSerial{},
	}
	for _, m := range models {
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

// seedSaleWithStockOut deja una venta cobrada en `sessionStatus`, con las salidas de kardex
// indicadas (productID → cantidad). Devuelve la venta.
func seedSaleWithStockOut(
	t *testing.T,
	db *gorm.DB,
	sessionStatus string,
	outs map[uint]float64,
) *database.TenantSale {
	t.Helper()
	session := database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: sessionStatus, OpenedAt: time.Now()}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	sale := database.TenantSale{
		Number: "NV01-1", DocType: "NOTA_VENTA", BranchID: 1, UserID: 1,
		Total: 100, Status: "completed", CashSessionID: &session.ID,
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
	for productID, qty := range outs {
		if err := db.Create(&database.TenantStockMovement{
			ProductID: productID, BranchID: 1, Type: "out", Quantity: qty,
			Reference: "VENTA/" + sale.Number, UserID: 1, CreatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return &sale
}

func countStockIn(db *gorm.DB, productID uint, ref string) float64 {
	var total float64
	db.Model(&database.TenantStockMovement{}).
		Where("product_id = ? AND type = ? AND reference = ?", productID, "in", ref).
		Select("COALESCE(SUM(quantity),0)").Scan(&total)
	return total
}

// Al vender un combo el stock sale de sus COMPONENTES, que no son líneas de la venta. Recorrer
// líneas al anular dejaba ese stock sin devolver; leyendo el kardex vuelve todo.
func TestAnulacionDevuelveStockDeComponentesDeCombo(t *testing.T) {
	db := setupCancelDB(t)
	// 7 y 9 son componentes: salieron del almacén aunque la venta facturó solo el combo.
	sale := seedSaleWithStockOut(t, db, "open", map[uint]float64{7: 2, 9: 1})

	svc := NewSaleService(db)
	if err := svc.Cancel(sale.ID, 1, "prueba"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	ref := "ANULACION VENTA/" + sale.Number
	if got := countStockIn(db, 7, ref); got != 2 {
		t.Errorf("componente 7: devueltos %.0f, se esperaban 2", got)
	}
	if got := countStockIn(db, 9, ref); got != 1 {
		t.Errorf("componente 9: devueltos %.0f, se esperaba 1", got)
	}
}

// Verificación puntual pedida por el usuario: confirma con un test real (no solo lectura de
// código) que Cancel() SIEMPRE revierte el movimiento bancario de una venta anulada — el bug
// encontrado en 15 tenants de producción (ventas ancladas de ANTES del 25/08, cuando este
// mecanismo de reversión bancaria todavía no existía) no puede volver a ocurrir con el código
// actual, porque reverseSaleCashTx() se llama sin condición alguna desde Cancel() (ver
// sale_service.go:1640) y busca el movimiento bancario por sale_id U por reference — cubre tanto
// ventas nuevas (sale_id poblado) como el patrón legado encontrado en producción (reference
// como único vínculo, sale_id NULL).
//
// Replica el caso EXACTO de producción (doriconta, venta B001-00000155): pago mixto
// efectivo+banco, con el movimiento bancario vinculado SOLO por reference (sale_id NULL, el
// patrón legado — así se prueba el caso más frágil, no el más fácil).
func TestAnulacionRevierteMovimientoBancario_VinculadoPorReference(t *testing.T) {
	db := setupCancelDB(t)
	sale := seedSaleWithStockOut(t, db, "open", map[uint]float64{7: 1})
	// Pago mixto: la caja ya se sembró en seedSaleWithStockOut (100 en efectivo) — se agrega
	// además el cobro bancario, vinculado SOLO por reference (sale_id NULL), igual que en
	// producción.
	bankAcc := database.TenantBankAccount{Name: "BBVA", PaymentMethod: "transferencia", Balance: 500, Active: true}
	if err := db.Create(&bankAcc).Error; err != nil {
		t.Fatal(err)
	}
	bankCredit := database.TenantBankMovement{
		BankAccountID: bankAcc.ID, Type: "credit", Amount: 40,
		Description: "Venta " + sale.Number, Reference: sale.Number, Date: time.Now(), UserID: 1,
	}
	if err := db.Create(&bankCredit).Error; err != nil {
		t.Fatal(err)
	}

	if err := NewSaleService(db).Cancel(sale.ID, 1, "NC aceptada"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	var reversal database.TenantBankMovement
	if err := db.Where("reversal_of_id = ?", bankCredit.ID).First(&reversal).Error; err != nil {
		t.Fatalf("no se registró la reversión del movimiento bancario (vinculado por reference): %v", err)
	}
	if reversal.Type != "debit" || reversal.Amount != 40 {
		t.Errorf("reversión = {type:%s amount:%.2f}, se esperaba {debit, 40.00}", reversal.Type, reversal.Amount)
	}
}

// Mismo escenario pero con el movimiento bancario vinculado por sale_id (el patrón actual desde
// el 25/08) — confirma que ambos caminos de búsqueda de reverseSaleCashTx funcionan.
func TestAnulacionRevierteMovimientoBancario_VinculadoPorSaleID(t *testing.T) {
	db := setupCancelDB(t)
	sale := seedSaleWithStockOut(t, db, "open", map[uint]float64{7: 1})
	bankAcc := database.TenantBankAccount{Name: "BCP", PaymentMethod: "transferencia", Balance: 500, Active: true}
	if err := db.Create(&bankAcc).Error; err != nil {
		t.Fatal(err)
	}
	bankCredit := database.TenantBankMovement{
		BankAccountID: bankAcc.ID, Type: "credit", Amount: 65,
		Description: "Venta " + sale.Number, Reference: sale.Number, SaleID: &sale.ID, Date: time.Now(), UserID: 1,
	}
	if err := db.Create(&bankCredit).Error; err != nil {
		t.Fatal(err)
	}

	if err := NewSaleService(db).Cancel(sale.ID, 1, "NC aceptada"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	var reversal database.TenantBankMovement
	if err := db.Where("reversal_of_id = ?", bankCredit.ID).First(&reversal).Error; err != nil {
		t.Fatalf("no se registró la reversión del movimiento bancario (vinculado por sale_id): %v", err)
	}
	if reversal.Type != "debit" || reversal.Amount != 65 {
		t.Errorf("reversión = {type:%s amount:%.2f}, se esperaba {debit, 65.00}", reversal.Type, reversal.Amount)
	}
}

// La anulación por nota de crédito no revertía caja: el cobro quedaba vivo y el arqueo cuadraba
// de más. Con la sesión abierta, la reversión va contra la misma caja.
func TestAnulacionRevierteCajaEnSesionAbierta(t *testing.T) {
	db := setupCancelDB(t)
	sale := seedSaleWithStockOut(t, db, "open", map[uint]float64{7: 1})

	if err := NewSaleService(db).Cancel(sale.ID, 1, "NC aceptada"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	var reversal database.TenantCashMovement
	if err := db.Where("sale_id = ? AND type = ?", sale.ID, "expense").First(&reversal).Error; err != nil {
		t.Fatalf("no se registró la salida de caja: %v", err)
	}
	if reversal.Amount != 100 {
		t.Errorf("monto revertido = %.2f, se esperaba 100", reversal.Amount)
	}
	if reversal.ReversalOfID == nil {
		t.Error("la reversión debe apuntar al cobro original vía reversal_of_id")
	}
	if reversal.CashSessionID != *sale.CashSessionID {
		t.Errorf("la reversión fue a la caja %d; con la sesión abierta debía ir a la %d",
			reversal.CashSessionID, *sale.CashSessionID)
	}
}

// Con la caja del cobro ya cerrada, el dinero sale de la caja abierta actual: reescribir un
// arqueo cerrado dejaría mintiendo el conteo de billetes de esa noche.
func TestAnulacionConCajaCerradaUsaLaCajaAbierta(t *testing.T) {
	db := setupCancelDB(t)
	sale := seedSaleWithStockOut(t, db, "closed", map[uint]float64{7: 1})
	abierta := database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	db.Create(&abierta)

	if err := NewSaleService(db).Cancel(sale.ID, 1, "NC aceptada"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	var reversal database.TenantCashMovement
	if err := db.Where("sale_id = ? AND type = ?", sale.ID, "expense").First(&reversal).Error; err != nil {
		t.Fatalf("no se registró la devolución: %v", err)
	}
	if reversal.CashSessionID != abierta.ID {
		t.Errorf("devolución en caja %d, se esperaba la abierta %d", reversal.CashSessionID, abierta.ID)
	}
	if reversal.ReversalOfID == nil {
		t.Error("sin reversal_of_id se pierde el vínculo con el cobro original")
	}
	// El arqueo cerrado no se toca.
	var enCerrada int64
	db.Model(&database.TenantCashMovement{}).
		Where("cash_session_id = ? AND type = ?", *sale.CashSessionID, "expense").Count(&enCerrada)
	if enCerrada != 0 {
		t.Errorf("se escribieron %d movimientos en la caja cerrada", enCerrada)
	}
}

// Sin ninguna caja abierta no se inventa un movimiento: la devolución queda pendiente y se
// deriva de los cobros sin reversión.
func TestAnulacionSinCajaAbiertaDejaDevolucionPendiente(t *testing.T) {
	db := setupCancelDB(t)
	sale := seedSaleWithStockOut(t, db, "closed", map[uint]float64{7: 1})

	svc := NewSaleService(db)
	if err := svc.Cancel(sale.ID, 1, "NC aceptada"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	var movimientos int64
	db.Model(&database.TenantCashMovement{}).Where("type = ?", "expense").Count(&movimientos)
	if movimientos != 0 {
		t.Errorf("se crearon %d salidas de caja sin ninguna sesión abierta", movimientos)
	}

	pendientes, err := svc.PendingSaleRefunds(1)
	if err != nil {
		t.Fatalf("PendingSaleRefunds: %v", err)
	}
	if len(pendientes) != 1 {
		t.Fatalf("devoluciones pendientes = %d, se esperaba 1", len(pendientes))
	}
	if pendientes[0].Amount != 100 || pendientes[0].SaleNumber != sale.Number {
		t.Errorf("pendiente = %+v", pendientes[0])
	}

	// Al aplicarla contra una caja abierta deja de estar pendiente, sin marcar nada.
	abierta := database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	db.Create(&abierta)
	if err := svc.ApplyPendingRefund(pendientes[0].CashMovementID, abierta.ID, 1, "devuelto"); err != nil {
		t.Fatalf("ApplyPendingRefund: %v", err)
	}
	restantes, _ := svc.PendingSaleRefunds(1)
	if len(restantes) != 0 {
		t.Errorf("sigue pendiente tras aplicarla: %d", len(restantes))
	}
}
