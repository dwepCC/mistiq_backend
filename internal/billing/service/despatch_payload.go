package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/facturador"
)

const (
	greModTrasladoPublico  = "01"
	greModTrasladoPrivado  = "02"
	greIndVehCondTransport = "SUNAT_Envio_IndicadorVehiculoConductoresTransp"
	// greCodEmisorMTC — catálogo SUNAT Entidad Autorizadora (MTC / TUC-CHV). Greenter usa "000001".
	greCodEmisorMTC = "000001"
)

func resolveDespatchModTraslado(sunatCode, inputMod string) string {
	if sunatCode == "31" {
		return greModTrasladoPrivado
	}
	mod := strings.TrimSpace(inputMod)
	if mod == "" {
		return greModTrasladoPrivado
	}
	return mod
}

func buildDespatchShipment(input CreateDespatchInput, sunatCode, fechaEmision, emisorRUC, emisorRazon string, partida, llegada facturador.DespatchDirection) facturador.DespatchShipment {
	fecTraslado := strings.TrimSpace(input.Envio.FecTraslado)
	if fecTraslado == "" {
		fecTraslado = fechaEmision
	}
	fecEntrega := strings.TrimSpace(input.Envio.FecEntregaTransportista)
	if fecEntrega == "" {
		fecEntrega = fecTraslado
	}
	modTraslado := resolveDespatchModTraslado(sunatCode, input.Envio.ModTraslado)

	shipment := facturador.DespatchShipment{
		CodTraslado:               input.Envio.CodTraslado,
		DesTraslado:               input.Envio.DesTraslado,
		ModTraslado:               modTraslado,
		FecTraslado:               fecTraslado,
		FecEntregaBienes:          fecEntrega,
		FecEntregaTransportista:   fecEntrega,
		Partida:                   partida,
		Llegada:                   llegada,
		PesoTotal:                 input.Envio.PesoTotal,
		UndPesoTotal:              input.Envio.UndPesoTotal,
		NumBultos:                 input.Envio.NumBultos,
	}
	if shipment.UndPesoTotal == "" {
		shipment.UndPesoTotal = "KGM"
	}

	placa := strings.TrimSpace(input.Envio.TransportistaPlaca)
	choferDoc := strings.TrimSpace(input.Envio.ChoferDoc)
	choferTipo := strings.TrimSpace(input.Envio.ChoferTipoDoc)
	if choferTipo == "" {
		choferTipo = "1"
	}

	needsVehicle := sunatCode == "31" || (sunatCode == "09" && modTraslado == greModTrasladoPrivado) || placa != ""
	if needsVehicle && placa != "" {
		shipment.Vehiculo = buildDespatchVehicle(
			placa,
			input.Envio.VehiculoHabCert,
			input.Envio.VehiculoCodEmisor,
		)
	}
	if driver := buildDespatchDriver(input, choferTipo, choferDoc); driver != nil {
		shipment.Choferes = []facturador.DespatchDriver{*driver}
	}

	if carrier := buildDespatchCarrier(input, sunatCode, modTraslado, emisorRUC, emisorRazon); carrier != nil {
		shipment.Transportista = carrier
	}

	// GRE remitente + transporte público: si hay flota del transportista, indicador exime GRE-T.
	if sunatCode == "09" && modTraslado == greModTrasladoPublico &&
		shipment.Vehiculo != nil && len(shipment.Choferes) > 0 {
		shipment.Indicadores = []string{greIndVehCondTransport}
	}

	return shipment
}

func buildDespatchVehicle(placa, habCert, codEmisor string) *facturador.DespatchVehicle {
	placa = normalizeGrePlaca(placa)
	if placa == "" {
		return nil
	}
	v := &facturador.DespatchVehicle{Placa: placa}
	cert := strings.TrimSpace(habCert)
	if cert != "" {
		v.NroAutorizacion = cert
		emisor := strings.TrimSpace(codEmisor)
		if emisor == "" {
			emisor = greCodEmisorMTC
		}
		v.CodEmisor = emisor
	}
	return v
}

func normalizeGrePlaca(placa string) string {
	placa = strings.ToUpper(strings.TrimSpace(placa))
	placa = strings.ReplaceAll(placa, "-", "")
	placa = strings.ReplaceAll(placa, " ", "")
	return placa
}

func normalizeGreLicencia(lic string) string {
	lic = strings.ToUpper(strings.TrimSpace(lic))
	lic = strings.ReplaceAll(lic, "-", "")
	lic = strings.ReplaceAll(lic, " ", "")
	return lic
}

func isValidGreLicencia(lic string) bool {
	lic = normalizeGreLicencia(lic)
	if len(lic) < 9 || len(lic) > 10 {
		return false
	}
	for _, r := range lic {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func buildDespatchDriver(input CreateDespatchInput, choferTipo, choferDoc string) *facturador.DespatchDriver {
	choferDoc = strings.TrimSpace(choferDoc)
	lic := normalizeGreLicencia(input.Envio.ChoferLicencia)
	if choferDoc == "" || lic == "" {
		return nil
	}
	nombres := strings.TrimSpace(input.Envio.ChoferNombres)
	apellidos := strings.TrimSpace(input.Envio.ChoferApellidos)
	if nombres == "" && apellidos == "" {
		nombres = "CONDUCTOR"
	}
	return &facturador.DespatchDriver{
		Tipo:      "Principal",
		TipoDoc:   choferTipo,
		NroDoc:    choferDoc,
		Nombres:   nombres,
		Apellidos: apellidos,
		Licencia:  lic,
	}
}

func buildDespatchCarrier(
	input CreateDespatchInput,
	sunatCode, modTraslado, emisorRUC, emisorRazon string,
) *facturador.DespatchTransportist {
	if sunatCode == "31" {
		ruc := strings.TrimSpace(emisorRUC)
		razon := strings.TrimSpace(emisorRazon)
		if ruc == "" || razon == "" {
			return nil
		}
		return &facturador.DespatchTransportist{
			TipoDoc:   "6",
			NumDoc:    ruc,
			RznSocial: razon,
			NroMtc:    strings.TrimSpace(input.Envio.TransportistaMTC),
		}
	}
	if sunatCode == "09" && modTraslado == greModTrasladoPublico {
		ruc := strings.TrimSpace(input.Envio.TransportistaRUC)
		if ruc == "" {
			return nil
		}
		return &facturador.DespatchTransportist{
			TipoDoc:   "6",
			NumDoc:    ruc,
			RznSocial: strings.TrimSpace(input.Envio.TransportistaRazon),
			NroMtc:    strings.TrimSpace(input.Envio.TransportistaMTC),
		}
	}
	return nil
}

func normalizeGrePartyDoc(tipoDoc, numDoc string) (string, string) {
	numDoc = strings.ReplaceAll(strings.TrimSpace(numDoc), "-", "")
	tipoDoc = strings.TrimSpace(tipoDoc)
	if tipoDoc == "" {
		switch len(numDoc) {
		case 11:
			tipoDoc = "6"
		case 8:
			tipoDoc = "1"
		default:
			tipoDoc = "6"
		}
	}
	return tipoDoc, numDoc
}

func validateDespatchDriverFields(env DespatchEnvioInput, required bool) error {
	doc := strings.TrimSpace(env.ChoferDoc)
	lic := normalizeGreLicencia(env.ChoferLicencia)
	hasAny := doc != "" || lic != ""
	if !required && !hasAny {
		return nil
	}
	if doc == "" {
		return fmt.Errorf("documento del conductor es obligatorio")
	}
	if lic == "" {
		return fmt.Errorf("licencia de conducir del conductor es obligatoria (SUNAT GRE)")
	}
	if !isValidGreLicencia(lic) {
		return fmt.Errorf("licencia de conducir inválida: SUNAT exige 9 a 10 caracteres alfanuméricos (no use el DNI como licencia)")
	}
	if doc == lic {
		return fmt.Errorf("la licencia no puede ser igual al documento del conductor")
	}
	return nil
}

func validateDespatchBusinessRules(input CreateDespatchInput, sunatCode, emisorRUC string) error {
	emisorRUC = strings.TrimSpace(emisorRUC)
	destDoc := strings.TrimSpace(input.Destinatario.NumDoc)
	if emisorRUC != "" && destDoc == emisorRUC {
		return fmt.Errorf("el destinatario (%s) no puede ser el mismo RUC del emisor; seleccione un cliente distinto", destDoc)
	}

	modTraslado := resolveDespatchModTraslado(sunatCode, input.Envio.ModTraslado)

	if sunatCode == "31" {
		r := input.Remitente
		if strings.TrimSpace(r.NumDoc) == "" || strings.TrimSpace(r.RznSocial) == "" {
			return fmt.Errorf("en guía transportista (31) el remitente (quien entrega la mercadería) es obligatorio")
		}
		if strings.TrimSpace(r.Address) == "" || strings.TrimSpace(r.Ubigeo) == "" {
			return fmt.Errorf("dirección y ubigeo del remitente son obligatorios en guía transportista (31)")
		}
		if emisorRUC != "" && strings.TrimSpace(r.NumDoc) == emisorRUC {
			return fmt.Errorf("el remitente no puede ser el mismo RUC del transportista emisor")
		}
		if strings.TrimSpace(input.Envio.TransportistaPlaca) == "" {
			return fmt.Errorf("la placa del vehículo es obligatoria en guía transportista (31)")
		}
		if err := validateDespatchDriverFields(input.Envio, true); err != nil {
			return err
		}
	}

	if sunatCode == "09" && modTraslado == greModTrasladoPublico {
		if strings.TrimSpace(input.Envio.TransportistaRUC) == "" {
			return fmt.Errorf("RUC del transportista es obligatorio en guía remitente con transporte público (SUNAT 2558)")
		}
		if strings.TrimSpace(input.Envio.TransportistaRazon) == "" {
			return fmt.Errorf("razón social del transportista es obligatoria en transporte público")
		}
		transpRUC := strings.TrimSpace(input.Envio.TransportistaRUC)
		if emisorRUC != "" && transpRUC == emisorRUC {
			return fmt.Errorf("el transportista no puede ser el mismo RUC del remitente/emisor (%s): SUNAT 2560. Si transporta con flota propia, use modalidad privada (02)", transpRUC)
		}
		if destDoc != "" && transpRUC == destDoc {
			return fmt.Errorf("el transportista no puede ser el mismo RUC del destinatario (%s): SUNAT 2560", transpRUC)
		}
	}

	if sunatCode == "09" && modTraslado == greModTrasladoPrivado {
		if strings.TrimSpace(input.Envio.TransportistaPlaca) == "" {
			return fmt.Errorf("la placa del vehículo es obligatoria en transporte privado (flota propia)")
		}
		if err := validateDespatchDriverFields(input.Envio, true); err != nil {
			return err
		}
	}

	// Si se informa placa + conductor en transporte público (indicador GRE-T), validar licencia real.
	if sunatCode == "09" && modTraslado == greModTrasladoPublico {
		hasDriverData := strings.TrimSpace(input.Envio.ChoferDoc) != "" || strings.TrimSpace(input.Envio.ChoferLicencia) != ""
		hasPlaca := strings.TrimSpace(input.Envio.TransportistaPlaca) != ""
		if hasPlaca && hasDriverData {
			if err := validateDespatchDriverFields(input.Envio, true); err != nil {
				return err
			}
		}
	}

	if strings.TrimSpace(input.Envio.FecEntregaTransportista) == "" && strings.TrimSpace(input.Envio.FecTraslado) == "" {
		return fmt.Errorf("fecha de entrega al transportista es obligatoria (SUNAT GRE 2022)")
	}
	return nil
}

func enrichDespatchPayloadMap(m map[string]interface{}) {
	if m == nil {
		return
	}
	m["version"] = "2022"
	envio, ok := m["envio"].(map[string]interface{})
	if !ok {
		return
	}
	tipo := strings.TrimSpace(fmt.Sprint(m["tipoDoc"]))

	fecTraslado := strings.TrimSpace(fmt.Sprint(envio["fecTraslado"]))
	if fecTraslado == "" || fecTraslado == "<nil>" {
		if fe, ok := m["fechaEmision"].(string); ok && fe != "" {
			fecTraslado = fe
			envio["fecTraslado"] = fe
		}
	}
	fecEntrega := strings.TrimSpace(fmt.Sprint(envio["fecEntregaBienes"]))
	if fecEntrega == "" || fecEntrega == "<nil>" {
		fecEntrega = strings.TrimSpace(fmt.Sprint(envio["fecEntregaTransportista"]))
	}
	if fecEntrega == "" || fecEntrega == "<nil>" {
		if fecTraslado != "" && fecTraslado != "<nil>" {
			envio["fecEntregaBienes"] = fecTraslado
			envio["fecEntregaTransportista"] = fecTraslado
		}
	} else {
		envio["fecEntregaBienes"] = fecEntrega
		if _, ok := envio["fecEntregaTransportista"]; !ok {
			envio["fecEntregaTransportista"] = fecEntrega
		}
	}

	mod := strings.TrimSpace(fmt.Sprint(envio["modTraslado"]))
	if tipo == "31" {
		envio["modTraslado"] = greModTrasladoPrivado
	} else if mod == "" || mod == "<nil>" {
		envio["modTraslado"] = greModTrasladoPrivado
	}

	// GRE 31: transportista = emisor si falta.
	if tipo == "31" && envio["transportista"] == nil {
		if company, ok := m["company"].(map[string]interface{}); ok {
			ruc := strings.TrimSpace(fmt.Sprint(company["ruc"]))
			razon := strings.TrimSpace(fmt.Sprint(company["razonSocial"]))
			if ruc != "" && razon != "" {
				envio["transportista"] = map[string]interface{}{
					"tipoDoc":   "6",
					"numDoc":    ruc,
					"rznSocial": razon,
				}
			}
		}
	}

	// Indicador exención GRE-T en remitente público con vehículo+conductor.
	if tipo == "09" {
		modFinal := strings.TrimSpace(fmt.Sprint(envio["modTraslado"]))
		hasVeh := envio["vehiculo"] != nil
		hasChofer := false
		if ch, ok := envio["choferes"].([]interface{}); ok && len(ch) > 0 {
			hasChofer = true
		}
		if modFinal == greModTrasladoPublico && hasVeh && hasChofer {
			envio["indicadores"] = []interface{}{greIndVehCondTransport}
		}
	}

	if veh, ok := envio["vehiculo"].(map[string]interface{}); ok {
		if placa, ok := veh["placa"].(string); ok && placa != "" {
			veh["placa"] = normalizeGrePlaca(placa)
		}
		cert := strings.TrimSpace(fmt.Sprint(veh["nroAutorizacion"]))
		if cert != "" && cert != "<nil>" {
			emisor := strings.TrimSpace(fmt.Sprint(veh["codEmisor"]))
			if emisor == "" || emisor == "<nil>" {
				veh["codEmisor"] = greCodEmisorMTC
			}
		}
		envio["vehiculo"] = veh
	}

	m["envio"] = envio
}

func enrichDespatchFiscalPayloadJSON(payloadJSON, tipoDoc, documentKind string) string {
	enriched := enrichFiscalPayloadJSON(payloadJSON, tipoDoc, documentKind)
	if enriched == "" {
		return enriched
	}
	var m map[string]interface{}
	if json.Unmarshal([]byte(enriched), &m) != nil {
		return enriched
	}
	enrichDespatchPayloadMap(m)
	b, err := json.Marshal(m)
	if err != nil {
		return enriched
	}
	return string(b)
}

// normalizeDespatchDateTime asegura formato ISO fiscal; si solo hay fecha, usa la hora de referencia.
func normalizeDespatchDateTime(value, fallback string) string {
	value = strings.TrimSpace(value)
	fallback = strings.TrimSpace(fallback)
	if value == "" {
		return fallback
	}
	if strings.Contains(value, "T") {
		return value
	}
	if t, err := time.Parse("2006-01-02", value); err == nil && fallback != "" {
		if ref, err2 := time.Parse(facturador.FiscalDateTimeLayout, fallback); err2 == nil {
			loc := ref.Location()
			merged := time.Date(t.Year(), t.Month(), t.Day(), ref.Hour(), ref.Minute(), ref.Second(), 0, loc)
			return merged.Format(facturador.FiscalDateTimeLayout)
		}
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Format(facturador.FiscalDateTimeLayout)
	}
	return value
}
