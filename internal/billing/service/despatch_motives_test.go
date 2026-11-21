package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
)

const testEmisorRUC = "20999999999"

func baseTraslado04() CreateDespatchInput {
	return CreateDespatchInput{
		BranchID: 1, SeriesID: 1,
		Destinatario: DespatchDestinatarioInput{TipoDoc: "6", NumDoc: testEmisorRUC, RznSocial: "Emisor SAC"},
		Envio: DespatchEnvioInput{
			CodTraslado: greMotivoTrasladoLocal, ModTraslado: greModTrasladoPublico,
			TransportistaRUC: "20123456789", TransportistaRazon: "Transportes SAC",
			FecTraslado: "2026-10-22", PesoTotal: 5, NumBultos: 1,
			PartidaUbigueo: "150101", PartidaDireccion: "Av. Principal 100",
			LlegadaUbigueo: "040101", LlegadaDireccion: "Calle Sucursal 200",
		},
		Details: []DespatchDetailInput{{Descripcion: "Caja", Cantidad: 2}},
	}
}

func TestMotivo04_DestinatarioEmpresa_Pasa(t *testing.T) {
	in := baseTraslado04()
	if err := validateDespatchInput(in, "09"); err != nil {
		t.Fatalf("validateDespatchInput: %v", err)
	}
	if err := validateDespatchBusinessRules(in, "09", testEmisorRUC); err != nil {
		t.Fatalf("el traslado entre locales debía aceptar destinatario = emisor: %v", err)
	}
}

func TestMotivo04_OrigenIgualDestino_Rechaza(t *testing.T) {
	in := baseTraslado04()
	in.Envio.LlegadaUbigueo = in.Envio.PartidaUbigueo
	in.Envio.LlegadaDireccion = "  av.  principal 100 "
	err := validateDespatchBusinessRules(in, "09", testEmisorRUC)
	if err == nil || !strings.Contains(err.Error(), "distinto del origen") {
		t.Fatalf("esperaba rechazo por origen = destino, got %v", err)
	}
}

func TestMotivo04_TransportistaPublicoEmpresa_Rechaza2560(t *testing.T) {
	in := baseTraslado04()
	in.Envio.TransportistaRUC = testEmisorRUC
	err := validateDespatchBusinessRules(in, "09", testEmisorRUC)
	if err == nil || !strings.Contains(err.Error(), "2560") {
		t.Fatalf("esperaba SUNAT 2560, got %v", err)
	}
}

func TestDestinatarioEmisor_EnVenta_SigueRechazado(t *testing.T) {
	in := baseTraslado04()
	in.Envio.CodTraslado = greMotivoVenta
	if err := validateDespatchBusinessRules(in, "09", testEmisorRUC); err == nil {
		t.Fatal("en una venta el destinatario no puede ser el RUC del emisor")
	}
	in.Envio.CodTraslado = "" // vacío = venta
	if err := validateDespatchBusinessRules(in, "09", testEmisorRUC); err == nil {
		t.Fatal("sin motivo (venta) el destinatario no puede ser el RUC del emisor")
	}
}

func TestDestinatarioEmisor_EnGuiaTransportista_SigueRechazado(t *testing.T) {
	in := baseTraslado04()
	in.Remitente = DespatchDestinatarioInput{NumDoc: "20555555555", RznSocial: "Remitente", Address: "x", Ubigeo: "150101"}
	in.Envio.TransportistaPlaca = "ABC123"
	if err := validateDespatchBusinessRules(in, "31", testEmisorRUC); err == nil {
		t.Fatal("la guía transportista no admite destinatario = emisor")
	}
}

func TestPublico_TransportistaIgualDestinatario_Rechaza(t *testing.T) {
	in := baseTraslado04()
	in.Envio.CodTraslado = greMotivoVenta
	in.Destinatario.NumDoc = "20111111111"
	in.Envio.TransportistaRUC = "20111111111"
	err := validateDespatchBusinessRules(in, "09", testEmisorRUC)
	if err == nil || !strings.Contains(err.Error(), "2560") {
		t.Fatalf("esperaba SUNAT 2560, got %v", err)
	}
}

func TestMotivo02_RequiereProveedor(t *testing.T) {
	in := baseTraslado04()
	in.Envio.CodTraslado = greMotivoCompra
	if err := validateDespatchBusinessRules(in, "09", testEmisorRUC); err == nil {
		t.Fatal("compra sin proveedor debía rechazarse")
	}
	in.Tercero = &DespatchDestinatarioInput{TipoDoc: "6", NumDoc: "20777777777", RznSocial: "Proveedor SA"}
	if err := validateDespatchBusinessRules(in, "09", testEmisorRUC); err != nil {
		t.Fatalf("compra con proveedor debía pasar: %v", err)
	}
	in.Tercero.NumDoc = testEmisorRUC
	if err := validateDespatchBusinessRules(in, "09", testEmisorRUC); err == nil {
		t.Fatal("el proveedor no puede ser la propia empresa")
	}
}

func TestMotivo13_RequiereDescripcion(t *testing.T) {
	in := baseTraslado04()
	in.Envio.CodTraslado = greMotivoOtros
	in.Envio.DesTraslado = ""
	if err := normalizeDespatchMotivo(&in, "09"); err == nil {
		t.Fatal("motivo otros sin descripción debía rechazarse")
	}
	in.Envio.DesTraslado = "Muestras para feria"
	if err := normalizeDespatchMotivo(&in, "09"); err != nil || in.Envio.DesTraslado != "Muestras para feria" {
		t.Fatalf("otros con descripción: err=%v des=%q", err, in.Envio.DesTraslado)
	}
}

func TestNormalizeMotivo_DescripcionPorDefectoYNoSoportados(t *testing.T) {
	in := CreateDespatchInput{Envio: DespatchEnvioInput{CodTraslado: greMotivoDevolucion}}
	if err := normalizeDespatchMotivo(&in, "09"); err != nil || in.Envio.DesTraslado != "Devolución" {
		t.Fatalf("devolución: err=%v des=%q", err, in.Envio.DesTraslado)
	}
	in = CreateDespatchInput{Envio: DespatchEnvioInput{CodTraslado: "08"}}
	if err := normalizeDespatchMotivo(&in, "09"); err == nil {
		t.Fatal("importación todavía no está soportada")
	}
	in = CreateDespatchInput{}
	if err := normalizeDespatchMotivo(&in, "31"); err != nil || in.Envio.CodTraslado != "01" {
		t.Fatalf("31 conserva venta por defecto: err=%v cod=%q", err, in.Envio.CodTraslado)
	}
}

func TestApplyMotivoParties_ForzaEmpresaEn04y02(t *testing.T) {
	in := baseTraslado04()
	in.Destinatario = DespatchDestinatarioInput{NumDoc: "20111111111", RznSocial: "Otro"}
	in.Tercero = &DespatchDestinatarioInput{NumDoc: "x"}
	applyDespatchMotivoParties(&in, "09", testEmisorRUC, "Emisor SAC", "Dir", "150101")
	if in.Destinatario.NumDoc != testEmisorRUC || in.Tercero != nil {
		t.Fatalf("04 debe fijar destinatario=empresa y limpiar tercero: %+v / %+v", in.Destinatario, in.Tercero)
	}
	in = baseTraslado04()
	in.Envio.CodTraslado = greMotivoCompra
	in.Destinatario = DespatchDestinatarioInput{NumDoc: "20111111111"}
	in.Tercero = &DespatchDestinatarioInput{NumDoc: "20777777777", RznSocial: "Prov"}
	applyDespatchMotivoParties(&in, "09", testEmisorRUC, "Emisor SAC", "Dir", "150101")
	if in.Destinatario.NumDoc != testEmisorRUC || in.Tercero == nil {
		t.Fatalf("02 debe fijar destinatario=empresa y conservar proveedor: %+v / %+v", in.Destinatario, in.Tercero)
	}
	in = baseTraslado04()
	in.Envio.CodTraslado = greMotivoVenta
	in.Destinatario = DespatchDestinatarioInput{NumDoc: "20111111111"}
	applyDespatchMotivoParties(&in, "09", testEmisorRUC, "Emisor SAC", "Dir", "150101")
	if in.Destinatario.NumDoc != "20111111111" {
		t.Fatal("venta no debe tocar el destinatario")
	}
}

func TestValidateInput_DestinatarioSinDireccionNiUbigeo_Pasa(t *testing.T) {
	in := baseTraslado04()
	in.Envio.CodTraslado = greMotivoVenta
	in.Destinatario = DespatchDestinatarioInput{TipoDoc: "1", NumDoc: "12345678", RznSocial: "Cliente"}
	if err := validateDespatchInput(in, "09"); err != nil {
		t.Fatalf("SUNAT no exige dirección/ubigeo del destinatario: %v", err)
	}
}

func TestShipment_M1L_OmiteVehiculoYConductorYAgregaIndicador(t *testing.T) {
	in := CreateDespatchInput{Envio: DespatchEnvioInput{
		ModTraslado: greModTrasladoPrivado, IndM1L: true, FecTraslado: "2026-10-22",
		TransportistaPlaca: "ABC123", ChoferDoc: "12345678", ChoferLicencia: "Q12345678",
	}}
	sh := buildDespatchShipment(in, "09", "2026-10-22T10:00:00", testEmisorRUC, "Emisor", facturador.DespatchDirection{}, facturador.DespatchDirection{})
	if sh.Vehiculo != nil || len(sh.Choferes) != 0 {
		t.Fatalf("M1/L no lleva vehículo ni conductor: %+v %+v", sh.Vehiculo, sh.Choferes)
	}
	if len(sh.Indicadores) != 1 || sh.Indicadores[0] != greIndM1L {
		t.Fatalf("indicadores: %v", sh.Indicadores)
	}
	in.Envio.TransportistaPlaca, in.Envio.ChoferDoc, in.Envio.ChoferLicencia = "", "", ""
	in.Destinatario.NumDoc = "20111111111"
	if err := validateDespatchBusinessRules(in, "09", testEmisorRUC); err != nil {
		t.Fatalf("privado con M1/L no exige placa ni conductor: %v", err)
	}
}

func TestShipment_IndicadoresExtraYSecundarios(t *testing.T) {
	in := CreateDespatchInput{Envio: DespatchEnvioInput{
		ModTraslado: greModTrasladoPrivado, TransportistaPlaca: "ABC123", ChoferDoc: "12345678", ChoferLicencia: "Q12345678",
		IndRetornoEnvases: true, IndRetornoVehiculoVacio: true, IndTransbordo: true,
		ChoferSecundario:   &DespatchChoferInput{Doc: "87654321", Licencia: "Q87654321", Nombres: "Ana"},
		VehiculoSecundario: &DespatchVehiculoInput{Placa: "xyz-987"},
	}}
	sh := buildDespatchShipment(in, "09", "2026-10-22T10:00:00", testEmisorRUC, "Emisor", facturador.DespatchDirection{}, facturador.DespatchDirection{})
	if len(sh.Indicadores) != 3 {
		t.Fatalf("indicadores: %v", sh.Indicadores)
	}
	if len(sh.Choferes) != 2 || sh.Choferes[1].Tipo != "Secundario" {
		t.Fatalf("choferes: %+v", sh.Choferes)
	}
	if sh.Vehiculo == nil || len(sh.Vehiculo.Secundarios) != 1 || sh.Vehiculo.Secundarios[0].Placa != "XYZ987" {
		t.Fatalf("vehículo secundario: %+v", sh.Vehiculo)
	}
}

func TestShipment_FletePagaRemitente_Solo31(t *testing.T) {
	in := CreateDespatchInput{Envio: DespatchEnvioInput{PagaFleteRemitente: true, TransportistaPlaca: "ABC123", ChoferDoc: "12345678", ChoferLicencia: "Q12345678"}}
	sh := buildDespatchShipment(in, "31", "2026-10-22T10:00:00", testEmisorRUC, "Emisor", facturador.DespatchDirection{}, facturador.DespatchDirection{})
	if len(sh.Indicadores) != 1 || sh.Indicadores[0] != greIndPagaFleteRemitente {
		t.Fatalf("indicadores 31: %v", sh.Indicadores)
	}
	sh = buildDespatchShipment(in, "09", "2026-10-22T10:00:00", testEmisorRUC, "Emisor", facturador.DespatchDirection{}, facturador.DespatchDirection{})
	if len(sh.Indicadores) != 0 {
		t.Fatalf("el flete del remitente es solo de la guía transportista: %v", sh.Indicadores)
	}
}

func TestSecondaryDriver_LicenciaInvalida_Rechaza(t *testing.T) {
	in := baseTraslado04()
	in.Envio.CodTraslado = greMotivoVenta
	in.Destinatario.NumDoc = "20111111111"
	in.Envio.ChoferSecundario = &DespatchChoferInput{Doc: "87654321", Licencia: "123"}
	if err := validateDespatchBusinessRules(in, "09", testEmisorRUC); err == nil {
		t.Fatal("licencia del segundo conductor inválida debía rechazarse")
	}
}

func TestEnrich_PublicoConFlota_ConservaIndicadoresExtra(t *testing.T) {
	m := map[string]interface{}{
		"tipoDoc": "09",
		"envio": map[string]interface{}{
			"modTraslado": greModTrasladoPublico,
			"vehiculo":    map[string]interface{}{"placa": "BEN946"},
			"choferes":    []interface{}{map[string]interface{}{"nroDoc": "12345678"}},
			"indicadores": []interface{}{greIndRetornoEnvases},
		},
	}
	enrichDespatchPayloadMap(m)
	ind := m["envio"].(map[string]interface{})["indicadores"].([]interface{})
	if len(ind) != 2 {
		t.Fatalf("debían convivir el indicador existente y el de exención GRE-T: %v", ind)
	}
}

func TestRelatedDocs_Manuales(t *testing.T) {
	docs, _ := buildDespatchRelatedDocs([]DespatchRelatedDocInput{{Tipo: "09", Nro: " t001-23 ", Emisor: "20555555555"}, {Nro: ""}})
	if len(docs) != 1 || docs[0].Nro != "T001-23" || docs[0].TipoDesc != "Guía de Remisión Remitente" {
		t.Fatalf("docs: %+v", docs)
	}
}

// El último conductor y vehículo usados no se pierden porque la guía más reciente haya sido de
// transporte público (sin conductor ni vehículo propios).
func TestLastDespatchTransport_CadaDatoDeLaGuiaMasRecienteQueLoDeclaro(t *testing.T) {
	db := setupDespatchListDB(t)
	svc := &BillingService{db: db}
	privada := `{"tipoDoc":"09","envio":{"modTraslado":"02","vehiculo":{"placa":"ABC123"},"choferes":[{"nroDoc":"44556677"}]}}`
	publica := `{"tipoDoc":"09","envio":{"modTraslado":"01","transportista":{"numDoc":"20100070970"}}}`
	for _, p := range []string{privada, publica} {
		if err := db.Create(&database.TenantDespatch{BranchID: 1, SeriesID: 1, Series: "T001", PayloadJSON: p}).Error; err != nil {
			t.Fatal(err)
		}
	}
	got := svc.GetLastDespatchTransport(1, "09")
	if !got.Found || got.Placa != "ABC123" || got.DriverDoc != "44556677" || got.CarrierDoc != "20100070970" {
		t.Fatalf("got %+v", got)
	}
	if got.ModTraslado != "01" {
		t.Fatalf("la modalidad es la de la guía más reciente: %q", got.ModTraslado)
	}
}

func TestFillRelatedGreIssuer_SoloTransportistaConRucDelRemitente(t *testing.T) {
	docs := []facturador.DespatchAdditionalDoc{{Tipo: "09", Nro: "T001-9"}, {Tipo: "01", Nro: "F001-1"}}
	fillRelatedGreIssuer(docs, "31", DespatchDestinatarioInput{NumDoc: "20131312955"})
	if docs[0].Emisor != "20131312955" || docs[1].Emisor != "" {
		t.Fatalf("docs: %+v", docs)
	}
	other := []facturador.DespatchAdditionalDoc{{Tipo: "09", Nro: "T001-9"}}
	fillRelatedGreIssuer(other, "09", DespatchDestinatarioInput{NumDoc: "20131312955"})
	if other[0].Emisor != "" {
		t.Fatal("la guía remitente no completa el emisor")
	}
}

func TestRelatedDocs_EmisorExplicitoSeRespetaYNoSeDuplica(t *testing.T) {
	docs, err := buildDespatchRelatedDocs([]DespatchRelatedDocInput{
		{Tipo: "09", Nro: "t001-9", Emisor: "20131312955"},
		{Tipo: "09", Nro: "T001-9", Emisor: "20131312955"},
	})
	if err != nil || len(docs) != 1 || docs[0].Emisor != "20131312955" || docs[0].Nro != "T001-9" {
		t.Fatalf("docs=%+v err=%v", docs, err)
	}
	// Emisor explícito distinto del remitente: fillRelatedGreIssuer no lo pisa.
	fillRelatedGreIssuer(docs, "31", DespatchDestinatarioInput{NumDoc: "20100070970"})
	if docs[0].Emisor != "20131312955" {
		t.Fatalf("el emisor explícito debía respetarse: %q", docs[0].Emisor)
	}
}

func TestRelatedDocs_EmisorDeducidoDelRemitenteSiNoViene(t *testing.T) {
	docs, err := buildDespatchRelatedDocs([]DespatchRelatedDocInput{{Tipo: "09", Nro: "T001-9"}})
	if err != nil {
		t.Fatal(err)
	}
	fillRelatedGreIssuer(docs, "31", DespatchDestinatarioInput{NumDoc: "20100070970"})
	if docs[0].Emisor != "20100070970" {
		t.Fatalf("emisor deducido: %q", docs[0].Emisor)
	}
}

func TestRelatedDocs_FormatoInvalidoSeRechaza(t *testing.T) {
	for _, in := range []DespatchRelatedDocInput{
		{Tipo: "09", Nro: "T001-9", Emisor: "2013131295"},
		{Tipo: "09", Nro: "T001-9", Emisor: "20131312abc"},
		{Tipo: "09", Nro: "T1-9"},
		{Tipo: "09", Nro: "T001-"},
		{Tipo: "99", Nro: "T001-9"},
	} {
		if err := validateDespatchRelatedDocs([]DespatchRelatedDocInput{in}); err == nil {
			t.Fatalf("debía rechazar %+v", in)
		}
	}
	in := baseTraslado04()
	in.DocsRelacionados = []DespatchRelatedDocInput{{Tipo: "09", Nro: "T001-9", Emisor: "123"}}
	if err := validateDespatchBusinessRules(in, "09", testEmisorRUC); err == nil {
		t.Fatal("las reglas de negocio deben validar el RUC del emisor del documento relacionado")
	}
}
