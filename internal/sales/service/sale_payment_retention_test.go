package service

import (
	"testing"

	salecontext "tukifac/internal/fiscal/salecontext"
)

func boolPtr(b bool) *bool { return &b }

func TestIgvRetentionAmountForPayments(t *testing.T) {
	agent := &salecontext.ContactSnapshot{DocType: "6", DocNumber: "20100000001", EsAgenteDeRetencion: true}
	plain := &salecontext.ContactSnapshot{DocType: "6", DocNumber: "20100000001"}
	on := &salecontext.FiscalContextInput{HasIgvRetention: boolPtr(true), IgvRetentionManualOverride: true}
	autoOnly := &salecontext.FiscalContextInput{HasIgvRetention: boolPtr(true)}

	cases := []struct {
		name    string
		fc      *salecontext.FiscalContextInput
		code    string
		contact *salecontext.ContactSnapshot
		total   float64
		want    float64
	}{
		{"agente sobre umbral", autoOnly, "01", agent, 1000, 30},
		{"manual a cliente no agente", on, "01", plain, 1000, 30},
		{"no agente sin override", autoOnly, "01", plain, 1000, 0},
		{"bajo umbral S/ 700", on, "01", agent, 700, 0},
		{"boleta no aplica", on, "03", agent, 1000, 0},
		{"sin contexto fiscal", nil, "01", agent, 1000, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := igvRetentionAmountForPayments(c.fc, c.code, c.contact, c.total, "PEN", nil)
			if got != c.want {
				t.Fatalf("got %.2f want %.2f", got, c.want)
			}
		})
	}
}
