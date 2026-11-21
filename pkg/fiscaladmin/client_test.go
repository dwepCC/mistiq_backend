package fiscaladmin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Fase 4 del Panel Central Fiscal (sección 5 de la auditoría): backend_go/pkg/fiscaladmin es el
// cliente HTTP que usa internal/superadmin/handler/fiscal_handler.go para hablar con
// facturador_lycet — debe ser un proxy TRANSPARENTE. Estas pruebas confirman con un servidor
// falso que el cliente no clasifica fiscalmente, no modifica error_type/retryable/retry_count/
// next_retry_at, y no transforma el status code ni el body de un 409 del guard fiscal
// (FiscalBulkActionService::isBlockedForNormalAction, Fase 1). No se toca fiscal_handler.go
// (ya es un passthrough confirmado por lectura de código) — esto solo prueba el cliente HTTP
// subyacente con evidencia ejecutable, sin agregar ninguna lógica fiscal nueva en backend_go.

func TestGetJSON_PassesFiscalFieldsThroughUnmodified(t *testing.T) {
	body := `{"items":[{"document_uuid":"uuid-1","status":"error","error_type":"transient","retryable":true,"retry_count":3,"next_retry_at":"2026-09-20T00:16:01+00:00"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	Init(server.URL, "test-token")
	raw, status, err := GetJSON("/api/v1/fiscal/documents", url.Values{"group": {"processing"}})
	if err != nil {
		t.Fatalf("GetJSON error inesperado: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status esperado 200, obtuvo %d", status)
	}
	if string(raw) != body {
		t.Fatalf("el body debe pasar BYTE A BYTE sin transformación.\nesperado: %s\nobtuvo:   %s", body, string(raw))
	}

	// Verificación adicional a nivel de campos deserializados: ninguno se perdió/renombró.
	var decoded struct {
		Items []struct {
			DocumentUUID string `json:"document_uuid"`
			Status       string `json:"status"`
			ErrorType    string `json:"error_type"`
			Retryable    bool   `json:"retryable"`
			RetryCount   int    `json:"retry_count"`
			NextRetryAt  string `json:"next_retry_at"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("no se pudo decodificar el raw devuelto: %v", err)
	}
	if len(decoded.Items) != 1 {
		t.Fatalf("esperaba 1 item, obtuvo %d", len(decoded.Items))
	}
	item := decoded.Items[0]
	if item.ErrorType != "transient" || !item.Retryable || item.RetryCount != 3 || item.NextRetryAt != "2026-09-20T00:16:01+00:00" {
		t.Fatalf("campos fiscales alterados por el proxy: %+v", item)
	}
}

// El caso crítico de Fase 1: un 409 del guard fiscal (send/retry sobre business/manual_only/
// permanent/accepted) debe llegar al frontend con el MISMO status code y el MISMO body — el
// cliente HTTP no debe "tragarse" el 409 ni reescribir su contrato (error/status/error_type/
// retryable/hint).
func TestPostJSON_Passes409GuardResponseThroughUnmodified(t *testing.T) {
	body := `{"error":"Este documento no admite reenvío/reintento normal en su estado actual","status":"error","error_type":"manual_only","retryable":false,"hint":"Usar la acción \"force\" para forzar el reenvío de todas formas (override administrativo)."}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	Init(server.URL, "test-token")
	raw, status, err := PostJSON("/api/v1/fiscal/documents/uuid-1/retry", nil)

	// PostJSON devuelve error no-nil para status>=400 (por diseño, ver client.go) — lo
	// relevante para el proxy es que el status y el body crudo lleguen intactos igual, así el
	// handler de Fiber los reenvía tal cual al frontend en vez de inventar su propio 500/502.
	if err == nil {
		t.Fatalf("se esperaba un error no-nil para status>=400 (comportamiento documentado del cliente)")
	}
	if status != http.StatusConflict {
		t.Fatalf("status esperado 409, obtuvo %d", status)
	}
	if string(raw) != body {
		t.Fatalf("el body del 409 debe pasar sin transformación.\nesperado: %s\nobtuvo:   %s", body, string(raw))
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("no se pudo decodificar el 409: %v", err)
	}
	for _, key := range []string{"error", "status", "error_type", "retryable", "hint"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("el proxy perdió la clave %q del contrato 409", key)
		}
	}
	if decoded["error_type"] != "manual_only" {
		t.Fatalf("error_type alterado por el proxy: %v", decoded["error_type"])
	}
}

// Confirma que no existe ninguna reclasificación: el cliente no inspecciona ni reescribe el
// body según su contenido — un body con un error_type que backend_go no "reconocería" (si
// tuviera una clasificación propia) debe pasar exactamente igual.
func TestGetJSON_DoesNotReclassifyUnknownErrorType(t *testing.T) {
	body := `{"error_type":"un_bucket_nuevo_que_backend_go_no_conoce","status":"error"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	Init(server.URL, "test-token")
	raw, _, err := GetJSON("/api/v1/fiscal/documents/uuid-1", nil)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if string(raw) != body {
		t.Fatalf("un error_type desconocido para backend_go debe pasar igual, sin reclasificar.\nesperado: %s\nobtuvo:   %s", body, string(raw))
	}
}
