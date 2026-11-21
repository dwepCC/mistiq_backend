package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
)

func destSetup(t *testing.T) (*CashBankService, *database.TenantCashSession, map[string]uint) {
	t.Helper()
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db) // cuentas: Yape (id 1), BCP (id 2)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	ids := map[string]uint{}
	var accs []database.TenantBankAccount
	db.Find(&accs)
	for _, a := range accs {
		ids[a.Name] = a.ID
	}
	return svc, session, ids
}

func balanceOf(t *testing.T, svc *CashBankService, id uint) float64 {
	t.Helper()
	var a database.TenantBankAccount
	if err := svc.db.First(&a, id).Error; err != nil {
		t.Fatal(err)
	}
	return a.Balance
}

// Fase B: el cajero elige a qué cuenta va este pago; sin elegir, va a la del método (como siempre).
func TestRecordPaymentToDestination_ChosenAccountReceivesPayment(t *testing.T) {
	svc, ses, ids := destSetup(t)
	sid := ses.ID
	// Sin destino: Yape → cuenta Yape
	if err := svc.RecordPaymentToDestination(nil, "yape", 50, &sid, "NV-1", "Venta", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	if balanceOf(t, svc, ids["Yape"]) != 50 || balanceOf(t, svc, ids["BCP"]) != 0 {
		t.Fatalf("sin destino debe ir a la cuenta del método: yape=%v bcp=%v", balanceOf(t, svc, ids["Yape"]), balanceOf(t, svc, ids["BCP"]))
	}
	// Con destino: el mismo método Yape, pero a la cuenta BCP
	bcp := ids["BCP"]
	if err := svc.RecordPaymentToDestination(nil, "yape", 30, &sid, "NV-2", "Venta", nil, 1, &bcp); err != nil {
		t.Fatal(err)
	}
	if balanceOf(t, svc, ids["Yape"]) != 50 || balanceOf(t, svc, ids["BCP"]) != 30 {
		t.Fatalf("con destino debe ir a la cuenta elegida: yape=%v bcp=%v", balanceOf(t, svc, ids["Yape"]), balanceOf(t, svc, ids["BCP"]))
	}
	var mv database.TenantBankMovement
	svc.db.Where("reference = ?", "NV-2").First(&mv)
	if mv.BankAccountID != bcp || mv.CashSessionID == nil || *mv.CashSessionID != sid {
		t.Errorf("el movimiento debe quedar en la cuenta elegida y en la sesión: %+v", mv)
	}
}

func TestRecordPaymentToDestination_InvalidDestinations(t *testing.T) {
	svc, ses, ids := destSetup(t)
	sid := ses.ID
	// El efectivo siempre va a la caja
	bcp := ids["BCP"]
	if err := svc.RecordPaymentToDestination(nil, "cash", 10, &sid, "NV-3", "Venta", nil, 1, &bcp); err == nil || !strings.Contains(err.Error(), "efectivo") {
		t.Errorf("efectivo con cuenta de destino debe rechazarse, err=%v", err)
	}
	// Cuenta inexistente
	bad := uint(9999)
	if err := svc.RecordPaymentToDestination(nil, "yape", 10, &sid, "NV-4", "Venta", nil, 1, &bad); err == nil || !strings.Contains(err.Error(), "no existe") {
		t.Errorf("cuenta inexistente debe rechazarse, err=%v", err)
	}
	// Cuenta inactiva
	svc.db.Model(&database.TenantBankAccount{}).Where("id = ?", bcp).Update("active", false)
	if err := svc.RecordPaymentToDestination(nil, "yape", 10, &sid, "NV-5", "Venta", nil, 1, &bcp); err == nil || !strings.Contains(err.Error(), "inactiva") {
		t.Errorf("cuenta inactiva debe rechazarse, err=%v", err)
	}
	svc.db.Model(&database.TenantBankAccount{}).Where("id = ?", bcp).Update("active", true)
	if balanceOf(t, svc, ids["BCP"]) != 0 || balanceOf(t, svc, ids["Yape"]) != 0 {
		t.Errorf("ningún rechazo debe mover saldos")
	}
}

// Fase A: un método a banco sin cuenta resoluble ya no pierde el pago en silencio.
func TestRecordPayment_BankMethodWithoutAccountFails(t *testing.T) {
	svc, ses, _ := destSetup(t)
	sid := ses.ID
	svc.db.Create(&database.TenantPaymentMethod{Code: "huerfano", Name: "Huérfano", Active: true, DestinationType: "bank_account"})
	err := svc.RecordPayment(nil, "huerfano", 10, &sid, "NV-7", "Venta", nil, 1)
	if err == nil || !strings.Contains(err.Error(), "no tiene una cuenta bancaria") {
		t.Fatalf("método sin cuenta debe dar error claro, err=%v", err)
	}
}

// Fase A: solo Efectivo puede enviarse a caja; los demás métodos deben ir a una cuenta.
func TestCreatePaymentMethod_OnlyCashCodeCanTargetCash(t *testing.T) {
	svc, _, _ := destSetup(t)
	if _, err := svc.CreatePaymentMethod("Caja chica", "caja_chica", "cash", nil); err == nil {
		t.Error("un método personalizado a caja debe rechazarse")
	}
}

// Fase A: movimientos manuales de banco validan cuenta y tipo.
func TestAddBankMovement_Validations(t *testing.T) {
	svc, _, ids := destSetup(t)
	if err := svc.AddBankMovement(9999, 1, "credit", "x", "r", 10, time.Now()); err == nil {
		t.Error("cuenta inexistente debe rechazarse")
	}
	if err := svc.AddBankMovement(ids["Yape"], 1, "otro", "x", "r", 10, time.Now()); err == nil {
		t.Error("tipo inválido debe rechazarse")
	}
	svc.db.Model(&database.TenantBankAccount{}).Where("id = ?", ids["BCP"]).Update("active", false)
	if err := svc.AddBankMovement(ids["BCP"], 1, "credit", "x", "r", 10, time.Now()); err == nil {
		t.Error("cuenta inactiva debe rechazarse")
	}
	if err := svc.AddBankMovement(ids["Yape"], 1, "credit", "x", "r", 10, time.Now()); err != nil {
		t.Errorf("movimiento válido: %v", err)
	}
	if balanceOf(t, svc, ids["Yape"]) != 10 {
		t.Errorf("saldo = %v, quiere 10", balanceOf(t, svc, ids["Yape"]))
	}
}

func TestReconcileBankAccounts_ImpliedOpening(t *testing.T) {
	svc, ses, ids := destSetup(t)
	sid := ses.ID
	svc.db.Model(&database.TenantBankAccount{}).Where("id = ?", ids["Yape"]).Update("balance", 100) // saldo inicial 100 sin movimiento
	if err := svc.RecordPayment(nil, "yape", 40, &sid, "NV-9", "Venta", nil, 1); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.ReconcileBankAccounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.AccountID == ids["Yape"] && (r.Balance != 140 || r.Credits != 40 || r.ImpliedOpening != 100) {
			t.Errorf("cuadre Yape: %+v (quiere saldo 140, créditos 40, inicial implícito 100)", r)
		}
	}
}
