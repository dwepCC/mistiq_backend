package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
)

// LastDespatchTransport últimos datos de transporte usados en una guía de la sucursal. El
// formulario los cruza con el catálogo de flota (por placa y documento) para preseleccionar
// conductor, vehículo y transportista sin pedirlos de nuevo.
type LastDespatchTransport struct {
	Found       bool   `json:"found"`
	ModTraslado string `json:"mod_traslado,omitempty"`
	CarrierDoc  string `json:"carrier_doc,omitempty"`
	Placa       string `json:"placa,omitempty"`
	DriverDoc   string `json:"driver_doc,omitempty"`
}

// GetLastDespatchTransport busca, entre las últimas guías de la sucursal del tipo pedido (09 o 31),
// la más reciente que declaró vehículo, conductor o transportista. No necesita tabla propia: el
// payload enviado ya guarda esos datos.
func (s *BillingService) GetLastDespatchTransport(branchID uint, tipoDoc string) LastDespatchTransport {
	out := LastDespatchTransport{}
	var list []database.TenantDespatch
	q := s.db.Select("id", "branch_id", "payload_json").Order("id DESC").Limit(40)
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	if err := q.Find(&list).Error; err != nil {
		return out
	}
	for _, d := range list {
		var p facturador.DespatchPayload
		if json.Unmarshal([]byte(d.PayloadJSON), &p) != nil {
			continue
		}
		if tipoDoc != "" && p.TipoDoc != tipoDoc {
			continue
		}
		hasVeh := p.Envio.Vehiculo != nil && p.Envio.Vehiculo.Placa != ""
		hasDriver := len(p.Envio.Choferes) > 0
		hasCarrier := p.TipoDoc == "09" && p.Envio.ModTraslado == greModTrasladoPublico && p.Envio.Transportista != nil
		if !hasVeh && !hasDriver && !hasCarrier {
			continue
		}
		// Cada dato se toma de la guía más reciente que lo declaró: una guía con transporte público
		// (sin conductor ni vehículo propios) no debe borrar el último conductor y vehículo usados.
		if !out.Found {
			out.Found = true
			out.ModTraslado = p.Envio.ModTraslado
		}
		if hasVeh && out.Placa == "" {
			out.Placa = p.Envio.Vehiculo.Placa
		}
		if hasDriver && out.DriverDoc == "" {
			out.DriverDoc = p.Envio.Choferes[0].NroDoc
		}
		if hasCarrier && out.CarrierDoc == "" {
			out.CarrierDoc = p.Envio.Transportista.NumDoc
		}
		if out.Placa != "" && out.DriverDoc != "" && out.CarrierDoc != "" {
			break
		}
	}
	return out
}

// DespatchLookupResult datos de una guía remitente propia, para autocompletar una guía
// transportista que la referencia.
type DespatchLookupResult struct {
	Number       string                       `json:"number"`
	Remitente    DespatchDestinatarioInput    `json:"remitente"`
	Destinatario DespatchDestinatarioInput    `json:"destinatario"`
	Partida      facturador.DespatchDirection `json:"partida"`
	Llegada      facturador.DespatchDirection `json:"llegada"`
	PesoTotal    float64                      `json:"peso_total"`
	NumBultos    int                          `json:"num_bultos"`
	Details      []DespatchDetailInput        `json:"details"`
}

// LookupOwnDespatch busca una guía remitente emitida por este mismo tenant ("T001-23").
func (s *BillingService) LookupOwnDespatch(number string) (*DespatchLookupResult, error) {
	parts := strings.SplitN(strings.TrimSpace(number), "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("escriba el número de la guía como SERIE-NÚMERO, por ejemplo T001-23")
	}
	serie := strings.ToUpper(strings.TrimSpace(parts[0]))
	corr, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil || serie == "" {
		return nil, fmt.Errorf("número de guía inválido")
	}
	var d database.TenantDespatch
	if err := s.db.Where("series = ? AND correlative = ?", serie, uint(corr)).First(&d).Error; err != nil {
		return nil, fmt.Errorf("no hay una guía %s-%d emitida desde este sistema", serie, corr)
	}
	var p facturador.DespatchPayload
	if err := json.Unmarshal([]byte(d.PayloadJSON), &p); err != nil || p.TipoDoc != "09" {
		return nil, fmt.Errorf("la guía %s-%d no es una guía de remisión remitente", serie, corr)
	}
	res := &DespatchLookupResult{
		Number:    fmt.Sprintf("%s-%d", serie, corr),
		Remitente: DespatchDestinatarioInput{TipoDoc: "6", NumDoc: p.Company.RUC, RznSocial: p.Company.RazonSocial},
		Partida:   p.Envio.Partida,
		Llegada:   p.Envio.Llegada,
		PesoTotal: p.Envio.PesoTotal,
		NumBultos: p.Envio.NumBultos,
	}
	dest := DespatchDestinatarioInput{TipoDoc: p.Destinatario.TipoDoc, NumDoc: p.Destinatario.NumDoc, RznSocial: p.Destinatario.RznSocial}
	if p.Destinatario.Address != nil {
		dest.Address, dest.Ubigeo = p.Destinatario.Address.Direccion, p.Destinatario.Address.Ubigueo
	}
	res.Destinatario = dest
	for _, it := range p.Details {
		res.Details = append(res.Details, DespatchDetailInput{Codigo: it.Codigo, Descripcion: it.Descripcion, Unidad: it.Unidad, Cantidad: it.Cantidad})
	}
	return res, nil
}
