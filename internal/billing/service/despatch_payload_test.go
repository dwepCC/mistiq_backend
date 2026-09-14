package service

import (
	"testing"

	"tukifac/pkg/facturador"
)

// TestBuildDespatchShipment_PublicoSinChofer_OmiteVehiculo cubre el bug real en producción
// (tenant RUC 20611288710, guías T002-4/5/6): al declarar la placa del vehículo en transporte
// público (mod_traslado 01) SIN datos de conductor, SUNAT rechaza con el código 3354 "No debe
// ingresar información de vehículo principal" porque el indicador de exención GRE-T solo aplica
// si también hay conductor. buildDespatchShipment debe omitir el vehículo por completo en ese
// caso, en vez de construir un payload que SUNAT rechaza seguro.
func TestBuildDespatchShipment_PublicoSinChofer_OmiteVehiculo(t *testing.T) {
	input := CreateDespatchInput{
		Envio: DespatchEnvioInput{
			ModTraslado:        greModTrasladoPublico,
			TransportistaRUC:   "20123456789",
			TransportistaRazon: "Transportes SAC",
			TransportistaPlaca: "BEN946",
			// Sin ChoferDoc/ChoferLicencia.
		},
	}
	shipment := buildDespatchShipment(input, "09", "2026-08-31T10:00:00", "20999999999", "Emisor SAC", facturador.DespatchDirection{}, facturador.DespatchDirection{})

	if shipment.Vehiculo != nil {
		t.Fatalf("esperaba vehículo omitido (sin conductor en modalidad pública), pero se construyó: %+v", shipment.Vehiculo)
	}
	if len(shipment.Indicadores) != 0 {
		t.Fatalf("no debía agregarse el indicador de exención GRE-T sin vehículo+conductor, got %v", shipment.Indicadores)
	}
}

// TestBuildDespatchShipment_PublicoConChofer_IncluyeVehiculoEIndicador es el caso legítimo
// original: placa + conductor completos en modalidad pública sí deben viajar juntos, con el
// indicador de exención.
func TestBuildDespatchShipment_PublicoConChofer_IncluyeVehiculoEIndicador(t *testing.T) {
	input := CreateDespatchInput{
		Envio: DespatchEnvioInput{
			ModTraslado:        greModTrasladoPublico,
			TransportistaRUC:   "20123456789",
			TransportistaRazon: "Transportes SAC",
			TransportistaPlaca: "BEN946",
			ChoferDoc:          "12345678",
			ChoferLicencia:     "Q12345678",
			ChoferNombres:      "Juan",
			ChoferApellidos:    "Perez",
		},
	}
	shipment := buildDespatchShipment(input, "09", "2026-08-31T10:00:00", "20999999999", "Emisor SAC", facturador.DespatchDirection{}, facturador.DespatchDirection{})

	if shipment.Vehiculo == nil {
		t.Fatal("esperaba vehículo incluido: placa + conductor completos en modalidad pública")
	}
	if len(shipment.Choferes) != 1 {
		t.Fatalf("esperaba 1 conductor, got %d", len(shipment.Choferes))
	}
	if len(shipment.Indicadores) != 1 || shipment.Indicadores[0] != greIndVehCondTransport {
		t.Fatalf("esperaba el indicador de exención GRE-T, got %v", shipment.Indicadores)
	}
}

// TestBuildDespatchShipment_Privado_VehiculoSinChofer_NoSeToca confirma que el fix es exclusivo
// de modalidad pública: en privado (flota propia) la placa sola sigue incluyéndose como antes
// (la validación de negocio, aparte, exige conductor en ese caso — ver
// TestValidateDespatchBusinessRules_Privado_RequiereChofer).
func TestBuildDespatchShipment_Privado_VehiculoSinChofer_NoSeToca(t *testing.T) {
	input := CreateDespatchInput{
		Envio: DespatchEnvioInput{
			ModTraslado:        greModTrasladoPrivado,
			TransportistaPlaca: "BEN946",
		},
	}
	shipment := buildDespatchShipment(input, "09", "2026-08-31T10:00:00", "20999999999", "Emisor SAC", facturador.DespatchDirection{}, facturador.DespatchDirection{})

	if shipment.Vehiculo == nil {
		t.Fatal("en modalidad privada la placa sola debía seguir incluyéndose (comportamiento previo intacto)")
	}
}

// TestValidateDespatchBusinessRules_Publico_PlacaSinChofer_Rechaza cubre la validación de
// entrada: declarar la placa sin conductor en transporte público debe rechazarse en el
// formulario, con un mensaje claro, en vez de dejar pasar datos que terminan rechazados por
// SUNAT (código 3354).
func TestValidateDespatchBusinessRules_Publico_PlacaSinChofer_Rechaza(t *testing.T) {
	input := CreateDespatchInput{
		Destinatario: DespatchDestinatarioInput{NumDoc: "20111111111"},
		Envio: DespatchEnvioInput{
			ModTraslado:             greModTrasladoPublico,
			TransportistaRUC:        "20123456789",
			TransportistaRazon:      "Transportes SAC",
			TransportistaPlaca:      "BEN946",
			FecEntregaTransportista: "2026-08-31",
		},
	}
	err := validateDespatchBusinessRules(input, "09", "20999999999")
	if err == nil {
		t.Fatal("esperaba error: placa declarada sin conductor en transporte público")
	}
}

// TestValidateDespatchBusinessRules_Publico_SinVehiculo_Permite confirma que el caso normal
// (público sin declarar vehículo/conductor del remitente en absoluto) sigue siendo válido — es
// el flujo estándar donde el transportista viaja con sus propios medios.
func TestValidateDespatchBusinessRules_Publico_SinVehiculo_Permite(t *testing.T) {
	input := CreateDespatchInput{
		Destinatario: DespatchDestinatarioInput{NumDoc: "20111111111"},
		Envio: DespatchEnvioInput{
			ModTraslado:             greModTrasladoPublico,
			TransportistaRUC:        "20123456789",
			TransportistaRazon:      "Transportes SAC",
			FecEntregaTransportista: "2026-08-31",
		},
	}
	if err := validateDespatchBusinessRules(input, "09", "20999999999"); err != nil {
		t.Fatalf("no debía rechazarse: transporte público sin datos de vehículo del remitente es válido, got %v", err)
	}
}

// TestValidateDespatchBusinessRules_Privado_RequiereChofer confirma que el requisito de
// conductor en modalidad privada (ya existente, sin relación al fix) sigue intacto.
func TestValidateDespatchBusinessRules_Privado_RequiereChofer(t *testing.T) {
	input := CreateDespatchInput{
		Destinatario: DespatchDestinatarioInput{NumDoc: "20111111111"},
		Envio: DespatchEnvioInput{
			ModTraslado:             greModTrasladoPrivado,
			TransportistaPlaca:      "BEN946",
			FecEntregaTransportista: "2026-08-31",
		},
	}
	if err := validateDespatchBusinessRules(input, "09", "20999999999"); err == nil {
		t.Fatal("esperaba error: modalidad privada exige conductor")
	}
}

// TestEnrichDespatchPayloadMap_StripsOrphanVehicleInPublico cubre el paso defensivo aplicado a
// nivel de JSON (usado en reenrichecimiento/reenvío de payloads ya construidos): un payload
// existente que quedó con vehículo sin conductor en modalidad pública (p.ej. los 3 registros
// reales rechazados antes de este fix) debe quedar sin el nodo "vehiculo" al reprocesarse, en
// vez de reintentar con el mismo dato que SUNAT ya rechazó.
func TestEnrichDespatchPayloadMap_StripsOrphanVehicleInPublico(t *testing.T) {
	m := map[string]interface{}{
		"tipoDoc": "09",
		"envio": map[string]interface{}{
			"modTraslado": greModTrasladoPublico,
			"vehiculo":    map[string]interface{}{"placa": "BEN946"},
		},
	}
	enrichDespatchPayloadMap(m)

	envio := m["envio"].(map[string]interface{})
	if envio["vehiculo"] != nil {
		t.Fatalf("esperaba que se quitara el vehículo huérfano, quedó: %+v", envio["vehiculo"])
	}
}

// TestEnrichDespatchPayloadMap_MantieneVehiculoConChofer confirma que el paso defensivo no
// toca el caso legítimo: vehículo + conductor juntos en modalidad pública se mantienen, y se
// agrega el indicador de exención GRE-T.
func TestEnrichDespatchPayloadMap_MantieneVehiculoConChofer(t *testing.T) {
	m := map[string]interface{}{
		"tipoDoc": "09",
		"envio": map[string]interface{}{
			"modTraslado": greModTrasladoPublico,
			"vehiculo":    map[string]interface{}{"placa": "BEN946"},
			"choferes":    []interface{}{map[string]interface{}{"numDoc": "12345678"}},
		},
	}
	enrichDespatchPayloadMap(m)

	envio := m["envio"].(map[string]interface{})
	if envio["vehiculo"] == nil {
		t.Fatal("no debía quitarse el vehículo: hay conductor declarado junto a él")
	}
	indicadores, _ := envio["indicadores"].([]interface{})
	if len(indicadores) != 1 || indicadores[0] != greIndVehCondTransport {
		t.Fatalf("esperaba el indicador de exención GRE-T, got %v", envio["indicadores"])
	}
}
