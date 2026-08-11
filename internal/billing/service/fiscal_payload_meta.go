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
	// NC/ND en cola antigua: relDocs sin tipDocAfectado → XML SUNAT con DocumentTypeCode vacío.
	if documentKind == "note" {
		ensureNoteAffectedDocFields(m)
		delete(m, "formaPago") // SUNAT 3246: PaymentMeansID "Contado" no aplica en notas.
		stripNoteBillingDuplicateRelDocs(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return payloadJSON
	}
	return string(b)
}

func ensureNoteAffectedDocFields(m map[string]interface{}) {
	if m == nil {
		return
	}
	if s, _ := m["tipDocAfectado"].(string); strings.TrimSpace(s) != "" {
		return
	}
	rel, ok := m["relDocs"].([]interface{})
	if !ok || len(rel) == 0 {
		return
	}
	first, ok := rel[0].(map[string]interface{})
	if !ok {
		return
	}
	if td, ok := first["tipoDoc"].(string); ok && strings.TrimSpace(td) != "" {
		m["tipDocAfectado"] = strings.TrimSpace(td)
	}
	if nd, ok := first["nroDoc"].(string); ok && strings.TrimSpace(nd) != "" {
		m["numDocfectado"] = strings.TrimSpace(nd)
	}
}

// stripNoteBillingDuplicateRelDocs quita relDocs que repiten el comprobante en BillingReference (obs. SUNAT 4009).
func stripNoteBillingDuplicateRelDocs(m map[string]interface{}) {
	if m == nil {
		return
	}
	tip, _ := m["tipDocAfectado"].(string)
	num, _ := m["numDocfectado"].(string)
	tip = strings.TrimSpace(tip)
	num = strings.TrimSpace(num)
	if tip == "" || num == "" {
		return
	}
	rel, ok := m["relDocs"].([]interface{})
	if !ok || len(rel) == 0 {
		return
	}
	var kept []interface{}
	for _, item := range rel {
		doc, ok := item.(map[string]interface{})
		if !ok {
			kept = append(kept, item)
			continue
		}
		td, _ := doc["tipoDoc"].(string)
		nd, _ := doc["nroDoc"].(string)
		if strings.TrimSpace(td) == tip && strings.TrimSpace(nd) == num {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		delete(m, "relDocs")
		return
	}
	m["relDocs"] = kept
}
