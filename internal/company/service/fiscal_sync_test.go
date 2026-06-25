package service

import (
	"encoding/json"
	"strings"
	"testing"

	"tukifac/pkg/facturador"
)

// buildFiscalSyncPayload replica la construcción de credenciales del sync (sin llamar al facturador).
func buildFiscalSyncPayload(solUserIn, solPassIn string) facturador.FiscalCompanySyncPayload {
	solUser := strings.TrimSpace(solUserIn)
	solPass := strings.TrimSpace(solPassIn)
	payload := facturador.FiscalCompanySyncPayload{
		RUC:      "10401387302",
		SendMode: "sunat_direct",
	}
	if solUser != "" {
		payload.SOLUser = solUser
	}
	if solPass != "" {
		payload.SOLPass = solPass
	}
	return payload
}

func TestFiscalSyncPayload_omitsSOLWhenNotProvided(t *testing.T) {
	raw, err := json.Marshal(buildFiscalSyncPayload("", ""))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "SOL_USER") {
		t.Fatalf("no debe enviar SOL_USER sin credenciales explícitas: %s", body)
	}
	if strings.Contains(body, "MODDATOS") {
		t.Fatalf("no debe inventar MODDATOS: %s", body)
	}
}

func TestFiscalSyncPayload_includesSOLWhenProvided(t *testing.T) {
	raw, err := json.Marshal(buildFiscalSyncPayload("10401387302USUARIO", "clave-secreta"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "10401387302USUARIO") {
		t.Fatalf("debe incluir SOL_USER explícito: %s", body)
	}
	if !strings.Contains(body, "clave-secreta") {
		t.Fatalf("debe incluir SOL_PASS explícito: %s", body)
	}
}
