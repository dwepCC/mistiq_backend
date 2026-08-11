// Package taxregime centraliza el régimen tributario del contribuyente y las
// capacidades que de él derivan (qué comprobantes puede emitir, si discrimina
// IGV en la representación impresa, tipo de operación por defecto, etc.).
//
// Es la ÚNICA fuente de verdad de estas reglas: el gate de emisión las aplica y
// el backend expone las capacidades resueltas a los frontends (que así no
// contienen lógica basada en el régimen). Agregar un régimen futuro (RER, RMT,
// General diferenciado) = añadir una fila al `registry`, sin tocar consumidores.
package taxregime

import "strings"

type Regime string

const (
	General Regime = "general" // Régimen General / MYPE / RER (hoy mismo comportamiento)
	NRUS    Regime = "nrus"    // Nuevo RUS
)

// Normalize resuelve un string arbitrario a un régimen conocido; por defecto
// General (compatibilidad con tenants sin régimen o valores desconocidos).
func Normalize(s string) Regime {
	switch Regime(strings.ToLower(strings.TrimSpace(s))) {
	case NRUS:
		return NRUS
	case General:
		return General
	default:
		return General
	}
}

// Policy describe las capacidades de un régimen de forma declarativa (datos, no
// ramas if). Los únicos `if` viven encapsulados en los métodos de consulta.
type Policy struct {
	Regime               Regime
	allowedDocTypes      map[string]bool // Catálogo 01 SUNAT: "00","01","03","07","08",...
	ShowIgvBreakdown     bool            // discriminar IGV en la representación impresa
	DefaultOperationType string          // Catálogo 51 SUNAT (p. ej. "0101" venta interna)
}

var registry = map[Regime]Policy{
	General: {
		Regime:               General,
		allowedDocTypes:      map[string]bool{"00": true, "01": true, "03": true, "07": true, "08": true, "09": true, "20": true, "40": true},
		ShowIgvBreakdown:     true,
		DefaultOperationType: "0101",
	},
	NRUS: {
		Regime: NRUS,
		// Nuevo RUS: solo boleta (03) y nota de venta interna (00); NC/ND (07/08)
		// sobre boletas. NO factura (01). Guías/retención/percepción no aplican.
		allowedDocTypes:      map[string]bool{"00": true, "03": true, "07": true, "08": true},
		ShowIgvBreakdown:     false,
		DefaultOperationType: "0101",
	},
}

// For devuelve la política del régimen (General por defecto/valor desconocido).
func For(regime string) Policy {
	if p, ok := registry[Normalize(regime)]; ok {
		return p
	}
	return registry[General]
}

// CanEmit indica si el régimen puede emitir el tipo de comprobante (Catálogo 01).
func (p Policy) CanEmit(sunatCode string) bool {
	return p.allowedDocTypes[strings.TrimSpace(sunatCode)]
}

func (p Policy) CanEmitFactura() bool     { return p.CanEmit("01") }
func (p Policy) CanEmitBoleta() bool      { return p.CanEmit("03") }
func (p Policy) CanEmitNotaCredito() bool { return p.CanEmit("07") }
func (p Policy) CanEmitNotaDebito() bool  { return p.CanEmit("08") }

// CanEmitNoteAffecting: una NC/ND solo puede recaer sobre un comprobante que el
// régimen sí puede emitir (p. ej. NRUS: NC sobre boleta 03, nunca sobre factura 01).
func (p Policy) CanEmitNoteAffecting(affectedSunatCode string) bool {
	return p.CanEmit(affectedSunatCode)
}

// Capabilities es el shape que se expone a los frontends (nivel régimen). Los
// frontends lo consumen sin reimplementar reglas del régimen.
type Capabilities struct {
	AllowedSaleDocCodes  []string `json:"allowed_sale_doc_codes"` // para el selector de venta: subconjunto de {"00","03","01"}
	CanEmitFactura       bool     `json:"can_emit_factura"`
	CanEmitBoleta        bool     `json:"can_emit_boleta"`
	CanEmitNotaCredito   bool     `json:"can_emit_nota_credito"`
	CanEmitNotaDebito    bool     `json:"can_emit_nota_debito"`
	ShowIgvBreakdown     bool     `json:"show_igv_breakdown"`
	DefaultOperationType string   `json:"default_operation_type"`
}

// Capabilities resuelve las capacidades del régimen para exponerlas al frontend.
func (p Policy) Capabilities() Capabilities {
	sale := make([]string, 0, 3)
	for _, c := range []string{"00", "03", "01"} { // orden estable para la UI
		if p.CanEmit(c) {
			sale = append(sale, c)
		}
	}
	return Capabilities{
		AllowedSaleDocCodes:  sale,
		CanEmitFactura:       p.CanEmitFactura(),
		CanEmitBoleta:        p.CanEmitBoleta(),
		CanEmitNotaCredito:   p.CanEmitNotaCredito(),
		CanEmitNotaDebito:    p.CanEmitNotaDebito(),
		ShowIgvBreakdown:     p.ShowIgvBreakdown,
		DefaultOperationType: p.DefaultOperationType,
	}
}

// CapabilitiesFor atajo: capacidades resueltas a partir del string de régimen.
func CapabilitiesFor(regime string) Capabilities {
	return For(regime).Capabilities()
}
