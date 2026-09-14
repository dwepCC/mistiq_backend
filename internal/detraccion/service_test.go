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

	// Releer de la BD, no solo el valor en memoria devuelto por Persist.
	var reloaded database.TenantSaleDetraccion
	if err := db.First(&reloaded, "sale_id = ?", 1).Error; err != nil {
		t.Fatalf("releer fila: %v", err)
	}
	if reloaded.PuntoOrigen != transporteSaleInput().PuntoOrigen {
		t.Errorf("punto_origen no persistió correctamente: %q", reloaded.PuntoOrigen)
	}
}

// Los 7 campos son obligatorios (formulario oficial de SUNAT los marca con asterisco). Verifica
// cada uno por separado para no depender de qué validación corre primero.
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
	}{
		{"sin registro MTC", func(s *SaleInput) { s.MtcRegistro = "" }},
		{"sin configuración vehicular", func(s *SaleInput) { s.ConfiguracionVehicular = "" }},
		{"sin punto origen", func(s *SaleInput) { s.PuntoOrigen = "" }},
		{"sin punto destino", func(s *SaleInput) { s.PuntoDestino = "" }},
		{"sin carga efectiva", func(s *SaleInput) { s.CargaEfectivaTm = 0 }},
		{"sin carga útil", func(s *SaleInput) { s.CargaUtilTm = 0 }},
		{"sin valor referencial", func(s *SaleInput) { s.ValorReferencialPen = 0 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			s := transporteSaleInput()
			c.mutate(s)
			in.Detraccion = s
			if _, err := svc.Persist(in); err == nil {
				t.Fatalf("%s: esperaba error", c.name)
			}
		})
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

	if len(payload.Details) != 1 || len(payload.Details[0].Atributos) != 7 {
		t.Fatalf("esperaba 7 AdditionalItemProperty en el ítem, got %d", len(payload.Details[0].Atributos))
	}
	codes := map[string]string{}
	for _, a := range payload.Details[0].Atributos {
		codes[a.Code] = a.Value
	}
	want := map[string]string{
		"3006": "15X123CNG",
		"3007": "C3",
		"3008": "AV. INDUSTRIAL 578 - LIMA - LIMA - LIMA",
		"3009": "JR. BUENAVISTA 234 - ANCASH - CASMA - CASMA",
		"3010": "1232.28",
		"3013": "12.00",
		"3014": "15.00",
	}
	for code, val := range want {
		if codes[code] != val {
			t.Errorf("AdditionalItemProperty %s = %q, want %q", code, codes[code], val)
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
