package service

import (
	"testing"
	"time"
)

func limaLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Lima")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// Bug reportado: F001-75 se emitió con la única cuota en la misma fecha que la emisión
// (2026-08-31 ambas) y SUNAT la rechazó (código 3267 — "Fecha del pago único o de las
// cuotas no puede ser anterior o igual a la fecha de emisión"). Nada validaba esto antes.
func TestValidateCreditInstallments_RejectsDueDateEqualToIssueDate(t *testing.T) {
	loc := limaLoc(t)
	issueDate := time.Date(2026, 8, 31, 12, 0, 0, 0, loc)
	_, _, err := validateCreditInstallments(
		[]CreditInstallmentInput{{DueDate: "2026-08-31", Amount: 100}},
		100, "PEN", loc, issueDate,
	)
	if err == nil {
		t.Fatal("esperaba error: cuota en la misma fecha que la emisión")
	}
}

func TestValidateCreditInstallments_RejectsDueDateBeforeIssueDate(t *testing.T) {
	loc := limaLoc(t)
	issueDate := time.Date(2026, 8, 31, 12, 0, 0, 0, loc)
	_, _, err := validateCreditInstallments(
		[]CreditInstallmentInput{{DueDate: "2026-08-30", Amount: 100}},
		100, "PEN", loc, issueDate,
	)
	if err == nil {
		t.Fatal("esperaba error: cuota antes de la fecha de emisión")
	}
}

func TestValidateCreditInstallments_RejectsAnyInstallmentBeforeOrEqualIssueDate_MultiCuota(t *testing.T) {
	loc := limaLoc(t)
	issueDate := time.Date(2026, 8, 31, 12, 0, 0, 0, loc)
	// La primera cuota es válida (posterior), pero la segunda coincide con la emisión —
	// debe rechazarse igual, no solo validar la última.
	_, _, err := validateCreditInstallments(
		[]CreditInstallmentInput{
			{DueDate: "2026-09-15", Amount: 50},
			{DueDate: "2026-08-31", Amount: 50},
		},
		100, "PEN", loc, issueDate,
	)
	if err == nil {
		t.Fatal("esperaba error: la cuota 2 coincide con la fecha de emisión")
	}
}

func TestValidateCreditInstallments_AcceptsDueDateAfterIssueDate(t *testing.T) {
	loc := limaLoc(t)
	issueDate := time.Date(2026, 8, 31, 12, 0, 0, 0, loc)
	rows, lastDue, err := validateCreditInstallments(
		[]CreditInstallmentInput{{DueDate: "2026-09-20", Amount: 100}},
		100, "PEN", loc, issueDate,
	)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if len(rows) != 1 || lastDue == nil {
		t.Fatalf("filas inesperadas: %+v lastDue=%v", rows, lastDue)
	}
}

// issueDate cero (zero value) = sin fecha de referencia disponible en el caller — no debe
// bloquear la creación (comportamiento previo intacto para ese caso límite).
func TestValidateCreditInstallments_ZeroIssueDateSkipsCheck(t *testing.T) {
	loc := limaLoc(t)
	_, _, err := validateCreditInstallments(
		[]CreditInstallmentInput{{DueDate: "2026-08-31", Amount: 100}},
		100, "PEN", loc, time.Time{},
	)
	if err != nil {
		t.Fatalf("no esperaba error con issueDate cero: %v", err)
	}
}
