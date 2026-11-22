package service

import (
	"encoding/json"
	"os"
	"testing"
)

// Prueba contra el libro REAL (datos de clientes: no se versiona). Se activa con
// EQUIPOS_PAYLOAD_JSON=ruta del JSON que arma el frontend. Sin la variable, se omite.
func TestImport_realWorkbook(t *testing.T) {
	path := os.Getenv("EQUIPOS_PAYLOAD_JSON")
	if path == "" {
		t.Skip("EQUIPOS_PAYLOAD_JSON no definido")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload ImportPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	db := setupEquiposDB(t)
	svc := New(db)
	pv, err := svc.ImportPreview(payload)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("período=%s pedidos=%d ítems=%d cobros=%d retornos=%d movimientos=%d clientes nuevos=%d ventas=%.2f cobrado=%.2f",
		pv.Period, pv.Counts.Orders, pv.Counts.Items, pv.Counts.Payments, pv.Counts.Returns, pv.Counts.Movements, pv.Counts.NewCustomers, pv.SalesTotal, pv.CollectedTotal)
	t.Logf("severidades=%v", pv.SeverityCounts)
	t.Logf("códigos=%v", pv.CodeCounts)
	for _, i := range pv.Issues {
		t.Logf("%s %s fila %d: %s", i.Severity, i.Code, i.Row, i.Message)
	}
	for _, r := range pv.Reconciliation {
		if !r.Match {
			t.Errorf("no coincide %s: excel=%+v sistema=%+v", r.Code, r.Excel, r.System)
		}
	}
	if !pv.CanCommit {
		t.Fatalf("no se puede importar: %v", pv.BlockedBy)
	}
	res, err := svc.ImportCommit(payload, 1)
	if err != nil {
		t.Fatalf("ImportCommit: %v", err)
	}
	t.Logf("lote %+v", res.Batch)
	// Reimportar el mismo mes debe rechazarse.
	if _, err := svc.ImportCommit(payload, 1); err == nil {
		t.Fatal("reimportar el mismo período debe fallar")
	}
}
