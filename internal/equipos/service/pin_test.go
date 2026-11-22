package service

import (
	"strings"
	"testing"
)

func TestSecurityPin(t *testing.T) {
	f := newFixture(t)
	if err := f.s.VerifyPin("1234", 91); err == nil || !strings.Contains(err.Error(), "configura") {
		t.Fatalf("sin PIN configurado debe pedir configurarlo: %v", err)
	}
	for _, bad := range []string{"12", "1234567", "12ab"} {
		if err := f.s.SetSecurityPin(bad, "", 91); err == nil {
			t.Errorf("PIN inválido aceptado: %s", bad)
		}
	}
	if err := f.s.SetSecurityPin("4321", "", 91); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.s.HasSecurityPin(); !ok {
		t.Fatal("debe quedar configurado")
	}
	if err := f.s.SetSecurityPin("1111", "0000", 91); err == nil {
		t.Error("cambiar el PIN exige el actual")
	}
	if err := f.s.VerifyPin("4321", 91); err != nil {
		t.Fatalf("PIN correcto: %v", err)
	}
	var last error
	for i := 0; i < 5; i++ {
		last = f.s.VerifyPin("9999", 92)
	}
	if last == nil || !strings.Contains(last.Error(), "bloque") {
		t.Fatalf("a los 5 fallos se bloquea: %v", last)
	}
	if err := f.s.VerifyPin("4321", 92); err == nil || !strings.Contains(err.Error(), "espera") {
		t.Fatalf("bloqueado aun con PIN correcto: %v", err)
	}
	if err := f.s.SetSecurityPin("1111", "4321", 91); err != nil {
		t.Fatalf("cambio con PIN actual: %v", err)
	}
}

func TestCustomersPaged(t *testing.T) {
	s := New(setupEquiposDB(t))
	for i := 0; i < 7; i++ {
		doc := "1000000" + string(rune('0'+i)) + "0"
		if _, err := s.CreateCustomer(CustomerInput{Name: "Cliente " + string(rune('A'+i)), DocType: "DNI", DocNumber: doc[:8]}); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := s.ListCustomersPaged("", 2, 3)
	if err != nil || total != 7 || len(rows) != 3 || rows[0].Name != "Cliente D" {
		t.Fatalf("página 2 de 3: %v total=%d %+v", err, total, rows)
	}
	rows, total, _ = s.ListCustomersPaged("Cliente G", 1, 3)
	if total != 1 || len(rows) != 1 {
		t.Fatalf("búsqueda: %d", total)
	}
}
