package detraccion

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	sunatdet "tukifac/pkg/sunat/detraccion"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupDetraccionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSaleDetraccion{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// transporteSaleInput reproduce el caso oficial de SUNAT (Lima-Casma, S/2,000 incluido IGV,
// valor referencial S/1,232.28) usado también en pkg/sunat/detraccion/calculator_test.go.
func transporteSaleInput() *SaleInput {
	return &SaleInput{
		GoodCode:               "027",
		ValorReferencialPen:    1232.28,
		MtcRegistro:            "15X123CNG",
		ConfiguracionVehicular: "C3",
		PuntoOrigen:            "AV. INDUSTRIAL 578 - LIMA - LIMA - LIMA",
		PuntoDestino:           "JR. BUENAVISTA 234 - ANCASH - CASMA - CASMA",
		CargaEfectivaTm:        12,
		CargaUtilTm:            15,
		TripDetail:             "Transporte de cemento en bolsas, viaje directo sin trasbordo",

		OriginUbigeo:             "150101",
		OriginAddress:            "AV. INDUSTRIAL 578 - LIMA",
		DestinationUbigeo:        "021803",
		DestinationAddress:       "JR. BUENAVISTA 234 - CASMA",
		ValorRefCargaEfectivaPen: 1100.50,
		ValorRefCargaUtilPen:     1300.75,
	}
}

func TestPersist1004_GuardaCamposDeTransporte(t *testing.T) {
	db := setupDetraccionTestDB(t)
	svc := NewService(db)

	row, err := svc.Persist(PersistInput{
		SaleID:            1,
		OperationTypeCode: sunatdet.OpDetraccionTransporte,
		SunatDocCode:      "01",
		Currency:          "PEN",
		SaleTotal:         2000,
		BankAccount:       "0004-1234567890",
		Detraccion:        transporteSaleInput(),
	})
	if err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if row.OperationTypeCode != sunatdet.OpDetraccionTransporte {
		t.Errorf("operation_type_code = %q, want %q", row.OperationTypeCode, sunatdet.OpDetraccionTransporte)
	}
	if row.DetractionAmountPen != 80 {
		t.Errorf("detraction_amount_pen = %v, want 80 (caso oficial SUNAT)", row.DetractionAmountPen)
	}
	if row.ValorReferencialPen == nil || *row.ValorReferencialPen != 1232.28 {
		t.Errorf("valor_referencial_pen = %v, want 1232.28", row.ValorReferencialPen)
	}
	if row.MtcRegistro != "15X123CNG" || row.ConfiguracionVehicular != "C3" {
		t.Errorf("datos de transporte no persistidos: mtc=%q config=%q", row.MtcRegistro, row.ConfiguracionVehicular)
	}
	if row.CargaEfectivaTm == nil || *row.CargaEfectivaTm != 12 {
		t.Errorf("carga_efectiva_tm = %v, want 12", row.CargaEfectivaTm)
	}
	if row.TripDetail != transporteSaleInput().TripDetail {
		t.Errorf("trip_detail no persistido: %q", row.TripDetail)
	}

	// Releer de la BD, no solo el valor en memoria devuelto por Persist.
	var reloaded database.TenantSaleDetraccion
	if err := db.First(&reloaded, "sale_id = ?", 1).Error; err != nil {
		t.Fatalf("releer fila: %v", err)
	}
	if reloaded.PuntoOrigen != transporteSaleInput().PuntoOrigen {
		t.Errorf("punto_origen no persistió correctamente: %q", reloaded.PuntoOrigen)
	}
}

// Decisión de negocio (ver evaluate): el valor referencial es una captura manual sin tabla oficial
// que la respalde en este sistema, y NUNCA debe usarse para calcular la detracción — ni siquiera
// cuando el usuario lo ingresa y es mayor al importe de la venta. Solo es informativo/impreso. Esto
// difiere a propósito de calculator.go (que sí implementa la comparación "el mayor" de la R.S.
// 073-2006-SUNAT Art. 4 como primitiva reutilizable, ver TestEvaluateDetraccion1004_ValorReferencialMayor)
// — a nivel de servicio, deliberadamente no se invoca esa comparación.
func TestPersist1004_ValorReferencialNuncaAumentaLaDetraccion(t *testing.T) {
	db := setupDetraccionTestDB(t)
	svc := NewService(db)

	in := transporteSaleInput()
	in.ValorReferencialPen = 999999 // muy por encima del importe de la venta (2000)

	row, err := svc.Persist(PersistInput{
		SaleID: 1, OperationTypeCode: sunatdet.OpDetraccionTransporte, SunatDocCode: "01",
		Currency: "PEN", SaleTotal: 2000, BankAccount: "0004-123",
		Detraccion: in,
	})
	if err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if row.DetractionAmountPen != 80 {
		t.Errorf("un valor referencial alto no debe aumentar la detracción: detraction_amount_pen = %v, want 80 (4%% de la venta real)", row.DetractionAmountPen)
	}
	if row.ValorReferencialPen == nil || *row.ValorReferencialPen != 999999 {
		t.Errorf("el valor referencial sí debe persistirse para mostrarse en el PDF, aunque no afecte el cálculo: %v", row.ValorReferencialPen)
	}
}

// Origen y destino son los únicos campos obligatorios de 1004: lo mínimo para identificar el
// servicio. Verifica cada uno por separado para no depender de qué validación corre primero.
func TestPersist1004_RechazaSinCamposObligatorios(t *testing.T) {
	db := setupDetraccionTestDB(t)
	svc := NewService(db)

	base := func() PersistInput {
		return PersistInput{
			SaleID: 1, OperationTypeCode: sunatdet.OpDetraccionTransporte, SunatDocCode: "01",
			Currency: "PEN", SaleTotal: 2000, BankAccount: "0004-123",
		}
	}

	cases := []struct {
		name   string
		mutate func(*SaleInput)
		want   string
	}{
		// SUNAT 3116-3126: cada uno de estos faltantes rechaza el comprobante.
		{"sin ubigeo origen (3116)", func(s *SaleInput) { s.OriginUbigeo = "" }, "ubigeo del punto de origen"},
		{"ubigeo origen mal formado", func(s *SaleInput) { s.OriginUbigeo = "15010" }, "ubigeo del punto de origen"},
		{"ubigeo origen con letras", func(s *SaleInput) { s.OriginUbigeo = "15O101" }, "ubigeo del punto de origen"},
		{"sin dirección origen (3117)", func(s *SaleInput) { s.OriginAddress = "  " }, "dirección detallada del punto de origen"},
		{"sin ubigeo destino (3118)", func(s *SaleInput) { s.DestinationUbigeo = "" }, "ubigeo del punto de destino"},
		{"sin dirección destino (3119)", func(s *SaleInput) { s.DestinationAddress = "" }, "dirección detallada del punto de destino"},
		{"sin detalle del viaje (3120)", func(s *SaleInput) { s.TripDetail = "" }, "detalle del viaje"},
		{"sin valor ref. servicio (3124)", func(s *SaleInput) { s.ValorReferencialPen = 0 }, "valor referencial del servicio"},
		{"sin valor ref. carga efectiva (3125)", func(s *SaleInput) { s.ValorRefCargaEfectivaPen = 0 }, "carga efectiva"},
		{"sin valor ref. carga útil (3126)", func(s *SaleInput) { s.ValorRefCargaUtilPen = 0 }, "carga útil nominal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			s := transporteSaleInput()
			c.mutate(s)
			in.Detraccion = s
			_, err := svc.Persist(in)
			if err == nil {
				t.Fatalf("%s: esperaba error", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: error %q no menciona %q", c.name, err.Error(), c.want)
			}
		})
	}
}

// Registro MTC, configuración vehicular, carga efectiva, carga útil, detalle del viaje y valor
// referencial son opcionales: ninguno es obligatorio en el esquema UBL 2.1 de SUNAT
// (cac:AdditionalItemProperty tiene cardinalidad 0..n), y calcular el valor referencial real
// exige la tabla oficial del MTC (D.S. 020-2021-MTC) que este sistema no tiene integrada — la
// propia norma permite usar solo el importe de la operación cuando no hay valor referencial
// determinable (ver validateTransporteFields). Persist debe aceptar la venta sin ellos, calcular
// la detracción sobre el importe de la venta, y el comprobante no debe llevar AdditionalItemProperty
// vacíos ni en "0.00" para los códigos correspondientes.
func TestPersist1004_CamposDescriptivosSonOpcionales(t *testing.T) {
	db := setupDetraccionTestDB(t)
	svc := NewService(db)

	in := transporteSaleInput()
	in.MtcRegistro = ""
	in.ConfiguracionVehicular = ""
	in.CargaEfectivaTm = 0
	in.CargaUtilTm = 0

	row, err := svc.Persist(PersistInput{
		SaleID: 1, OperationTypeCode: sunatdet.OpDetraccionTransporte, SunatDocCode: "01",
		Currency: "PEN", SaleTotal: 2000, BankAccount: "0004-123",
		Detraccion: in,
	})
	if err != nil {
		t.Fatalf("Persist no debía rechazar la venta sin los campos descriptivos opcionales: %v", err)
	}
	if row.CargaEfectivaTm != nil || row.CargaUtilTm != nil {
		t.Errorf("TM de carga efectiva/útil sin completar deben quedar nil, got ce=%v cu=%v", row.CargaEfectivaTm, row.CargaUtilTm)
	}
	if row.DetractionAmountPen != 80 {
		t.Errorf("sin valor referencial, la detracción debe calcularse sobre el importe de la venta: detraction_amount_pen = %v, want 80 (4%% de 2000)", row.DetractionAmountPen)
	}

	payload := &facturador.InvoicePayload{
		Details: []facturador.InvoiceDetail{{Descripcion: "Servicio de transporte de carga"}},
	}
	ApplyToInvoicePayload(payload, row)
	for _, a := range payload.Details[0].Atributos {
		if a.Code == "3006" || a.Code == "3007" || a.Code == "3010" || a.Code == "3013" || a.Code == "3014" {
			t.Errorf("no debía enviarse AdditionalItemProperty %s vacío, got %+v", a.Code, a)
		}
	}
	// Solo los 8 datos de cac:Delivery (origen, destino, viaje, 3 valores referenciales).
	if len(payload.Details[0].Atributos) != 8 {
		t.Errorf("esperaba 8 atributos de cac:Delivery, got %d", len(payload.Details[0].Atributos))
	}
}

// El código de bien debe ser 027 — igual que ya cubre calculator_test.go, pero acá verificando
// que el rechazo se propague hasta Persist (no solo Evaluate).
func TestPersist1004_RechazaCodigoDistintoDe027(t *testing.T) {
	db := setupDetraccionTestDB(t)
	svc := NewService(db)
	in := PersistInput{
		SaleID: 1, OperationTypeCode: sunatdet.OpDetraccionTransporte, SunatDocCode: "01",
		Currency: "PEN", SaleTotal: 2000, BankAccount: "0004-123",
	}
	s := transporteSaleInput()
	s.GoodCode = "014"
	in.Detraccion = s
	if _, err := svc.Persist(in); err == nil {
		t.Fatal("esperaba error: 1004 exige el código 027")
	}
}

func TestApplyToInvoicePayload_1004(t *testing.T) {
	db := setupDetraccionTestDB(t)
	svc := NewService(db)
	if _, err := svc.Persist(PersistInput{
		SaleID: 1, OperationTypeCode: sunatdet.OpDetraccionTransporte, SunatDocCode: "01",
		Currency: "PEN", SaleTotal: 2000, BankAccount: "0004-1234567890",
		Detraccion: transporteSaleInput(),
	}); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	row, err := svc.LoadBySaleID(1)
	if err != nil || row == nil {
		t.Fatalf("LoadBySaleID: %v", err)
	}

	payload := &facturador.InvoicePayload{
		Details: []facturador.InvoiceDetail{
			{Descripcion: "Servicio de transporte de carga Lima-Casma"},
		},
	}
	ApplyToInvoicePayload(payload, row)

	if payload.TipoOperacion != sunatdet.OpDetraccionTransporte {
		t.Errorf("tipoOperacion = %q, want %q (no debe quedar hardcodeado a 1001)", payload.TipoOperacion, sunatdet.OpDetraccionTransporte)
	}
	if payload.Detraccion == nil || payload.Detraccion.CodBienDetraccion != "027" {
		t.Fatalf("detraccion no aplicada correctamente: %+v", payload.Detraccion)
	}

	var legendFound bool
	for _, l := range payload.Legends {
		if l.Code == "2006" {
			legendFound = true
			if l.Value != sunatdet.Legend2006TextTransporte {
				t.Errorf("leyenda 2006 = %q, want el texto de transporte", l.Value)
			}
		}
	}
	if !legendFound {
		t.Fatal("falta la leyenda 2006")
	}

	// 4 AdditionalItemProperty (MTC, config. vehicular, TM efectiva y útil) + 8 datos de cac:Delivery.
	if len(payload.Details) != 1 || len(payload.Details[0].Atributos) != 12 {
		t.Fatalf("esperaba 12 atributos en el ítem, got %d", len(payload.Details[0].Atributos))
	}
	codes := map[string]string{}
	for _, a := range payload.Details[0].Atributos {
		codes[a.Code] = a.Value
	}
	want := map[string]string{
		"3006": "15X123CNG",
		"3007": "C3",
		"3013": "12.00",
		"3014": "15.00",
		// cac:InvoiceLine/cac:Delivery (SUNAT 3116-3126), que el facturador convierte a XML.
		"T1004_ORIG_UBIGEO":       "150101",
		"T1004_ORIG_DIR":          "AV. INDUSTRIAL 578 - LIMA",
		"T1004_DEST_UBIGEO":       "021803",
		"T1004_DEST_DIR":          "JR. BUENAVISTA 234 - CASMA",
		"T1004_VIAJE":             "Transporte de cemento en bolsas, viaje directo sin trasbordo",
		"T1004_VR_SERVICIO":       "1232.28",
		"T1004_VR_CARGA_EFECTIVA": "1100.50",
		"T1004_VR_CARGA_UTIL":     "1300.75",
	}
	for code, val := range want {
		if codes[code] != val {
			t.Errorf("AdditionalItemProperty %s = %q, want %q", code, codes[code], val)
		}
	}
	// Origen/destino y valor referencial ya no van como item property 3008/3009/3010: los reemplaza
	// cac:Delivery.
	for code := range codes {
		if code == "3008" || code == "3009" || code == "3010" {
			t.Errorf("no debía enviarse el AdditionalItemProperty %s cuando hay cac:Delivery", code)
		}
	}
}

// Con varias líneas, los datos de cac:Delivery van solo en la primera (SUNAT pide uno y solo uno de
// cada valor referencial).
func TestApplyToInvoicePayload_1004_DeliverySoloEnPrimeraLinea(t *testing.T) {
	db := setupDetraccionTestDB(t)
	svc := NewService(db)
	if _, err := svc.Persist(PersistInput{
		SaleID: 1, OperationTypeCode: sunatdet.OpDetraccionTransporte, SunatDocCode: "01",
		Currency: "PEN", SaleTotal: 2000, BankAccount: "0004-1234567890", Detraccion: transporteSaleInput(),
	}); err != nil {
		t.Fatal(err)
	}
	row, _ := svc.LoadBySaleID(1)
	payload := &facturador.InvoicePayload{Details: []facturador.InvoiceDetail{{Descripcion: "a"}, {Descripcion: "b"}}}
	ApplyToInvoicePayload(payload, row)
	count := func(d facturador.InvoiceDetail) int {
		n := 0
		for _, a := range d.Atributos {
			if strings.HasPrefix(a.Code, "T1004_") {
				n++
			}
		}
		return n
	}
	if count(payload.Details[0]) != 8 || count(payload.Details[1]) != 0 {
		t.Errorf("T1004_* en línea 1 = %d (want 8), en línea 2 = %d (want 0)", count(payload.Details[0]), count(payload.Details[1]))
	}
}

// Ventas guardadas antes de la v163 (solo texto libre, sin ubigeo) siguen enviándose como antes:
// 3008/3009/3010 y sin atributos reservados.
func TestApplyToInvoicePayload_1004_FilaAnteriorSoloTexto(t *testing.T) {
	vr, ce, cu := 1232.28, 12.0, 15.0
	row := &database.TenantSaleDetraccion{
		SaleID: 1, OperationTypeCode: sunatdet.OpDetraccionTransporte, GoodCode: "027",
		PaymentMethodCode: "001", BankAccount: "0004-1", RatePercent: 4, DetractionAmountPen: 80,
		PuntoOrigen: "LIMA", PuntoDestino: "CASMA", ValorReferencialPen: &vr, CargaEfectivaTm: &ce, CargaUtilTm: &cu,
	}
	payload := &facturador.InvoicePayload{Details: []facturador.InvoiceDetail{{Descripcion: "a"}}}
	ApplyToInvoicePayload(payload, row)
	codes := map[string]string{}
	for _, a := range payload.Details[0].Atributos {
		codes[a.Code] = a.Value
	}
	if codes["3008"] != "LIMA" || codes["3009"] != "CASMA" || codes["3010"] != "1232.28" {
		t.Errorf("fila anterior debe conservar 3008/3009/3010, got %v", codes)
	}
	for code := range codes {
		if strings.HasPrefix(code, "T1004_") {
			t.Errorf("fila anterior no debe llevar atributos reservados: %s", code)
		}
	}
}

// 1001 no debe verse afectado: sin Atributos, leyenda estándar, tipoOperacion=1001.
func TestApplyToInvoicePayload_1001_SinCambios(t *testing.T) {
	db := setupDetraccionTestDB(t)
	svc := NewService(db)
	if _, err := svc.Persist(PersistInput{
		SaleID: 1, OperationTypeCode: sunatdet.OpDetraccionGeneral, SunatDocCode: "01",
		Currency: "PEN", GravadoTotal: 1180, SaleTotal: 1180, BankAccount: "0004-1234567890",
		Detraccion: &SaleInput{GoodCode: "014"},
	}); err != nil {
		t.Fatalf("Persist 1001: %v", err)
	}
	row, err := svc.LoadBySaleID(1)
	if err != nil || row == nil {
		t.Fatalf("LoadBySaleID: %v", err)
	}

	payload := &facturador.InvoicePayload{
		Details: []facturador.InvoiceDetail{{Descripcion: "Servicio administrativo"}},
	}
	ApplyToInvoicePayload(payload, row)

	if payload.TipoOperacion != sunatdet.OpDetraccionGeneral {
		t.Errorf("tipoOperacion = %q, want %q", payload.TipoOperacion, sunatdet.OpDetraccionGeneral)
	}
	if payload.Details[0].Atributos != nil {
		t.Errorf("1001 no debe agregar AdditionalItemProperty, got %+v", payload.Details[0].Atributos)
	}
	var legendValue string
	for _, l := range payload.Legends {
		if l.Code == "2006" {
			legendValue = l.Value
		}
	}
	if legendValue != sunatdet.Legend2006Text {
		t.Errorf("leyenda 2006 = %q, want el texto general (sin cambios respecto a antes)", legendValue)
	}
}

// Regresión de compatibilidad: filas guardadas ANTES de esta migración no tienen
// operation_type_code (columna nueva, default '1001' a nivel de esquema, pero el struct en Go
// vía Save() escribiría ” si no se setea explícitamente — este test simula justamente ese caso
// leyendo una fila con el campo vacío) y deben seguir tratándose como 1001.
func TestApplyToInvoicePayload_FilaAntiguaSinOperationTypeCode(t *testing.T) {
	db := setupDetraccionTestDB(t)
	old := database.TenantSaleDetraccion{
		SaleID: 1, GoodCode: "014", PaymentMethodCode: "001", BankAccount: "0004-123",
		RatePercent: 4, BaseAmountPen: 1000, DetractionAmountPen: 40, InvoiceTotalPen: 1000,
		NetPayablePen: 960, BnConfirmationStatus: "pending",
	}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	// Forzar operation_type_code vacío directamente en la BD, simulando una fila pre-migración
	// (el default de columna solo aplica en el INSERT del motor real; en SQLite de test el
	// struct ya trae el default de Go 'not null;default:1001' vía GORM al crear, así que hay que
	// pisarlo a mano para simular el escenario real).
	if err := db.Model(&database.TenantSaleDetraccion{}).Where("sale_id = ?", 1).
		Update("operation_type_code", "").Error; err != nil {
		t.Fatal(err)
	}

	var row database.TenantSaleDetraccion
	if err := db.First(&row, "sale_id = ?", 1).Error; err != nil {
		t.Fatal(err)
	}
	if row.OperationTypeCode != "" {
		t.Fatalf("precondición: esperaba operation_type_code vacío, got %q", row.OperationTypeCode)
	}

	payload := &facturador.InvoicePayload{Details: []facturador.InvoiceDetail{{}}}
	ApplyToInvoicePayload(payload, &row)
	if payload.TipoOperacion != sunatdet.OpDetraccionGeneral {
		t.Errorf("fila sin operation_type_code debe caer a 1001 (compat), got %q", payload.TipoOperacion)
	}
	if strings.TrimSpace(payload.Detraccion.CodBienDetraccion) != "014" {
		t.Errorf("detraccion no aplicada: %+v", payload.Detraccion)
	}
}
