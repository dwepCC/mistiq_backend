package service

import (
	"encoding/json"
	"strings"
)

// enrichFiscalPayloadJSON añade tipoDoc y _meta para el deserializador Greenter en facturador SSOT.
func enrichFiscalPayloadJSON(payloadJSON, tipoDoc, documentKind string) string {
	if payloadJSON == "" {
		return payloadJSON
	}
	var m map[string]interface{}
	if json.Unmarshal([]byte(payloadJSON), &m) != nil {
		return payloadJSON
	}
	if tipoDoc != "" {
		m["tipoDoc"] = tipoDoc
	}
	if documentKind != "" {
		m["_meta"] = map[string]string{"document_kind": documentKind}
	}
	if documentKind == "note" {
		normalizeNoteRelDocs(m)
		stripNoteFormaPago(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return payloadJSON
	}
	return string(b)
}

// normalizeNoteRelDocs copia relDocs[0] a tipDocAfectado/numDocfectado (Greenter los usa en BillingReference XML).
func normalizeNoteRelDocs(m map[string]interface{}) {
	tip, _ := m["tipDocAfectado"].(string)
	num, _ := m["numDocfectado"].(string)
	if strings.TrimSpace(tip) != "" && strings.TrimSpace(num) != "" {
		return
	}
	relDocs, ok := m["relDocs"].([]interface{})
	if !ok || len(relDocs) == 0 {
		return
	}
	first, ok := relDocs[0].(map[string]interface{})
	if !ok {
		return
	}
	if strings.TrimSpace(tip) == "" {
		if v, ok := first["tipoDoc"].(string); ok && strings.TrimSpace(v) != "" {
			m["tipDocAfectado"] = strings.TrimSpace(v)
		}
	}
	if strings.TrimSpace(num) == "" {
		if v, ok := first["nroDoc"].(string); ok && strings.TrimSpace(v) != "" {
			m["numDocfectado"] = strings.TrimSpace(v)
		}
	}
	tip, _ = m["tipDocAfectado"].(string)
	num, _ = m["numDocfectado"].(string)
	if strings.TrimSpace(tip) != "" && strings.TrimSpace(num) != "" {
		delete(m, "relDocs")
	}
}

// stripNoteFormaPago elimina formaPago/cuotas: la guía SUNAT de NC/ND UBL 2.1 no admite PaymentMeansID "Contado".
func stripNoteFormaPago(m map[string]interface{}) {
	delete(m, "formaPago")
	delete(m, "cuotas")
}
