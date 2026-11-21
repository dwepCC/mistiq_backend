package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Cobro por «Transferencia» dirigido a Yape (no a la cuenta del método) + egreso manual elegido de Yape: las filas de ingresos/egresos dicen la cuenta,
// el reporte de sesión suma por cuenta real, la lista combinada va por fecha y el efectivo no se toca.
func TestMovimientosConCuentaElegida(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	if err := db.AutoMigrate(&database.TenantPurchasePayment{}, &database.TenantPurchasePayable{}); err != nil {
		t.Fatal(err)
	}
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)
	var yape database.TenantBankAccount
	db.Where("name = ?", "Yape").First(&yape)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now().Add(-time.Hour)}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	// Venta pagada por «transferencia» con destino Yape.
	sale := &database.TenantSale{ID: 1, BranchID: 1, UserID: 1, CashSessionID: &session.ID, SeriesID: 1, DocType: "boleta", Series: "B001", Correlative: 1,
		Number: "B001-1", IssueDate: time.Now(), Total: 50, Status: "paid", CreatedAt: time.Now().Add(-10 * time.Minute)}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePayment{SaleID: 1, Method: "transferencia", Amount: 50, CashSessionID: &session.ID, CreatedAt: sale.CreatedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordPaymentToDestination(db, "transferencia", 50, &session.ID, "B001-1", "Venta B001-1", &sale.ID, 1, &yape.ID); err != nil {
		t.Fatal(err)
	}
	// Una venta en efectivo más antigua que la anterior y un egreso manual de Yape más nuevo.
	createReportSale(t, db, svc, 2, session.ID, map[string]float64{"cash": 30})
	db.Model(&database.TenantSale{}).Where("id = 2").Update("created_at", time.Now().Add(-20*time.Minute))
	db.Model(&database.TenantCashMovement{}).Where("sale_id = 2").Update("created_at", time.Now().Add(-20*time.Minute))

	if err := svc.AddMovementToAccount(session.ID, 1, "expense", "Gasto", "REF-1", "transferencia", 20, "", nil, &yape.ID); err != nil {
		t.Fatalf("egreso eligiendo la cuenta: %v", err)
	}
	// El efectivo no admite cuenta; una cuenta inexistente se rechaza.
	if err := svc.AddMovementToAccount(session.ID, 1, "expense", "Gasto", "X", "cash", 5, "", nil, &yape.ID); err == nil {
		t.Error("efectivo con cuenta debió rechazarse")
	}
	if err := svc.AddMovementToAccount(session.ID, 1, "expense", "Gasto", "X", "transferencia", 5, "", nil, ptrUint(9999)); err == nil {
		t.Error("cuenta inexistente debió rechazarse")
	}

	split, err := svc.ListMovementsReport(MovementReportFilters{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, r := range split.Electronic.Data {
		names[r.DocNumber] = r.AccountName
	}
	if names["B001-1"] != "Yape" || names["REF-1"] != "Yape" {
		t.Errorf("filas electrónicas deben decir la cuenta Yape: %v", names)
	}
	for _, r := range split.Cash.Data {
		if r.AccountName != "" {
			t.Errorf("el efectivo no lleva cuenta: %+v", r)
		}
	}

	rep, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Accounts) != 1 || rep.Accounts[0].Name != "Yape" || rep.Accounts[0].Income != 50 || rep.Accounts[0].Expense != 20 || rep.Accounts[0].Net != 30 {
		t.Errorf("reporte por cuenta: %+v", rep.Accounts)
	}
	var bcp database.TenantBankAccount
	db.Where("name = ?", "BCP").First(&bcp)
	if bcp.Balance != 0 {
		t.Errorf("la cuenta del método no debe recibir nada: %v", bcp.Balance)
	}

	// Lista combinada: por fecha (efectivo de hace 20 min entre medio de los electrónicos), no efectivo primero y electrónico después.
	all := append(append([]MovementReportRow{}, split.Cash.Data...), split.Electronic.Data...)
	SortMovementRowsDesc(all)
	for i := 1; i < len(all); i++ {
		if all[i].Date.After(all[i-1].Date) {
			t.Fatalf("la lista combinada no está ordenada por fecha: %s", fmt.Sprint(all))
		}
	}
}

func ptrUint(v uint) *uint { return &v }
