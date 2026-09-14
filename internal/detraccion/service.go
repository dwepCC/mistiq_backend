package detraccion

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	sunatdet "tukifac/pkg/sunat/detraccion"

	"gorm.io/gorm"
)

// Service persiste y carga detracción por venta.
type Service struct {
	db      *gorm.DB
	catalog *sunatdet.CatalogProvider
}

func NewService(db *gorm.DB) *Service {
	cat, _ := sunatdet.DefaultCatalog()
	return &Service{db: db, catalog: cat}
}

// SaleInput datos de detracción enviados al crear venta.
type SaleInput struct {
	GoodCode          string `json:"good_code"`
	PaymentMethodCode string `json:"payment_method_code,omitempty"`
	// Campos exclusivos de 1004 (transporte de carga por vía terrestre). Todos obligatorios
	// cuando OperationTypeCode=1004 (ver evaluate) — SUNAT los exige como
	// cac:InvoiceLine/cac:Item/cac:AdditionalItemProperty (Catálogo N° 55), captura manual.
	ValorReferencialPen    float64 `json:"valor_referencial_pen,omitempty"`
	MtcRegistro            string  `json:"mtc_registro,omitempty"`
	ConfiguracionVehicular string  `json:"configuracion_vehicular,omitempty"`
	PuntoOrigen            string  `json:"punto_origen,omitempty"`
	PuntoDestino           string  `json:"punto_destino,omitempty"`
	CargaEfectivaTm        float64 `json:"carga_efectiva_tm,omitempty"`
	CargaUtilTm            float64 `json:"carga_util_tm,omitempty"`
}

// PersistInput datos completos para guardar detracción.
type PersistInput struct {
	SaleID            uint
	OperationTypeCode string
	SunatDocCode      string
	Currency          string
	ExchangeRate      *float64
	SaleTotal         float64
	GravadoTotal      float64
	BankAccount       string
	PaymentMethodCode string
	Detraccion        *SaleInput
	ContactEsPercepcion bool
}

// Evaluate calcula detracción sin persistir (validación de pagos en venta 1001).
func (s *Service) Evaluate(in PersistInput) (sunatdet.CalcResult, error) {
	return s.evaluate(in)
}

func (s *Service) evaluate(in PersistInput) (sunatdet.CalcResult, error) {
	op := in.OperationTypeCode
	if op != sunatdet.OpDetraccionGeneral && op != sunatdet.OpDetraccionTransporte {
		return sunatdet.CalcResult{}, nil
	}
	if in.Detraccion == nil {
		return sunatdet.CalcResult{}, errors.New("operación sujeta a detracción requiere datos de detracción")
	}
	if op == sunatdet.OpDetraccionTransporte {
		if err := validateTransporteFields(in.Detraccion); err != nil {
			return sunatdet.CalcResult{}, err
		}
	}
	paymentCode := strings.TrimSpace(in.PaymentMethodCode)
	if paymentCode == "" {
		paymentCode = strings.TrimSpace(in.Detraccion.PaymentMethodCode)
	}
	return sunatdet.Evaluate(s.catalog, sunatdet.CalcInput{
		OperationTypeCode:   op,
		SunatDocCode:        in.SunatDocCode,
		Currency:            in.Currency,
		ExchangeRate:        in.ExchangeRate,
		GravadoTotalPEN:     in.GravadoTotal,
		SaleTotalPEN:        in.SaleTotal,
		GoodCode:            in.Detraccion.GoodCode,
		BankAccount:         in.BankAccount,
		PaymentMethodCode:   paymentCode,
		ContactEsPercepcion: in.ContactEsPercepcion,
		ValorReferencialPEN: in.Detraccion.ValorReferencialPen,
	})
}

// validateTransporteFields exige los datos que el formulario oficial de SUNAT (caso práctico
// cpe.sunat.gob.pe) marca con asterisco para 1004: registro MTC, configuración vehicular,
// origen, destino, carga efectiva, carga útil y valor referencial. Todos van al comprobante como
// AdditionalItemProperty (ver ApplyToInvoicePayload); sin ellos el XML quedaría incompleto.
func validateTransporteFields(in *SaleInput) error {
	if strings.TrimSpace(in.MtcRegistro) == "" {
		return errors.New("el registro MTC del transportista es obligatorio para detracción por transporte de carga")
	}
	if strings.TrimSpace(in.ConfiguracionVehicular) == "" {
		return errors.New("la configuración vehicular es obligatoria para detracción por transporte de carga")
	}
	if strings.TrimSpace(in.PuntoOrigen) == "" {
		return errors.New("el punto de origen es obligatorio para detracción por transporte de carga")
	}
	if strings.TrimSpace(in.PuntoDestino) == "" {
		return errors.New("el punto de destino es obligatorio para detracción por transporte de carga")
	}
	if in.CargaEfectivaTm <= 0 {
		return errors.New("la carga efectiva (TM) es obligatoria para detracción por transporte de carga")
	}
	if in.CargaUtilTm <= 0 {
		return errors.New("la carga útil del vehículo (TM) es obligatoria para detracción por transporte de carga")
	}
	if in.ValorReferencialPen <= 0 {
		return errors.New("el valor referencial del servicio de transporte es obligatorio para detracción por transporte de carga")
	}
	return nil
}

// Persist evalúa y guarda tenant_sale_detraccion cuando operation_type = 1001 o 1004.
func (s *Service) Persist(in PersistInput) (*database.TenantSaleDetraccion, error) {
	op := in.OperationTypeCode
	if op != sunatdet.OpDetraccionGeneral && op != sunatdet.OpDetraccionTransporte {
		return nil, nil
	}
	eval, err := s.evaluate(in)
	if err != nil {
		return nil, err
	}
	if !eval.Applicable {
		return nil, errors.New(eval.Reason)
	}

	row := database.TenantSaleDetraccion{
		SaleID:               in.SaleID,
		OperationTypeCode:    op,
		GoodCode:             eval.GoodCode,
		PaymentMethodCode:    eval.PaymentMethodCode,
		BankAccount:          eval.BankAccount,
		RatePercent:          eval.RatePercent,
		BaseAmountPen:        eval.BaseAmountPEN,
		DetractionAmountPen:  eval.DetractionAmountPEN,
		InvoiceTotalPen:      in.SaleTotal,
		NetPayablePen:        eval.NetPayablePEN,
		BnConfirmationStatus: "pending",
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	if op == sunatdet.OpDetraccionTransporte && in.Detraccion != nil {
		vr := in.Detraccion.ValorReferencialPen
		ce := in.Detraccion.CargaEfectivaTm
		cu := in.Detraccion.CargaUtilTm
		row.ValorReferencialPen = &vr
		row.MtcRegistro = strings.TrimSpace(in.Detraccion.MtcRegistro)
		row.ConfiguracionVehicular = strings.TrimSpace(in.Detraccion.ConfiguracionVehicular)
		row.PuntoOrigen = strings.TrimSpace(in.Detraccion.PuntoOrigen)
		row.PuntoDestino = strings.TrimSpace(in.Detraccion.PuntoDestino)
		row.CargaEfectivaTm = &ce
		row.CargaUtilTm = &cu
	}
	if err := s.db.Save(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// LoadBySaleID carga detracción de una venta.
func (s *Service) LoadBySaleID(saleID uint) (*database.TenantSaleDetraccion, error) {
	var row database.TenantSaleDetraccion
	err := s.db.First(&row, "sale_id = ?", saleID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ApplyToInvoicePayload añade detraccion, leyenda 2006 y (solo 1004) los AdditionalItemProperty
// de transporte al payload Lycet.
func ApplyToInvoicePayload(payload *facturador.InvoicePayload, row *database.TenantSaleDetraccion) {
	if payload == nil || row == nil {
		return
	}
	// Filas creadas antes de esta columna (default 'default:1001' en la migración) son todas de
	// la única operación que existía entonces.
	op := strings.TrimSpace(row.OperationTypeCode)
	if op == "" {
		op = sunatdet.OpDetraccionGeneral
	}
	payload.TipoOperacion = op
	payload.Detraccion = &facturador.InvoiceDetraction{
		Percent:           row.RatePercent,
		Mount:             row.DetractionAmountPen,
		CtaBanco:          row.BankAccount,
		CodMedioPago:      row.PaymentMethodCode,
		CodBienDetraccion: row.GoodCode,
	}
	if op == sunatdet.OpDetraccionTransporte {
		facturador.AppendSUNATLegendText(&payload.Legends, "2006", sunatdet.Legend2006TextTransporte)
		attrs := transporteAttributes(row)
		for i := range payload.Details {
			payload.Details[i].Atributos = attrs
		}
		return
	}
	facturador.AppendSUNATLegend2006(&payload.Legends)
}

// transporteAttributes arma los AdditionalItemProperty exigidos por SUNAT para 1004 (Catálogo N°
// 55 — Guía de Elaboración de Documentos XML Factura Electrónica UBL 2.1, cpe.sunat.gob.pe).
// Códigos verificados contra el ejemplo oficial de la propia guía (3002-3005 hidrobiológicos),
// que confirma el orden secuencial de la tabla.
func transporteAttributes(row *database.TenantSaleDetraccion) []facturador.DetailAttribute {
	f2 := func(v *float64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatFloat(*v, 'f', 2, 64)
	}
	return []facturador.DetailAttribute{
		{Code: "3006", Name: "Detracciones: Transporte Bienes vía terrestre – Numero Registro MTC", Value: row.MtcRegistro},
		{Code: "3007", Name: "Detracciones: Transporte Bienes vía terrestre – Configuración Vehicular", Value: row.ConfiguracionVehicular},
		{Code: "3008", Name: "Detracciones: Transporte Bienes vía terrestre – Punto de Origen", Value: row.PuntoOrigen},
		{Code: "3009", Name: "Detracciones: Transporte Bienes vía terrestre – Punto Destino", Value: row.PuntoDestino},
		{Code: "3010", Name: "Detracciones: Transporte Bienes – Valor Referencial Preliminar por Viaje", Value: f2(row.ValorReferencialPen)},
		{Code: "3013", Name: "Detracciones: Transporte Bienes – Carga Efectiva en TM por Vehículo", Value: f2(row.CargaEfectivaTm)},
		{Code: "3014", Name: "Detracciones: Transporte Bienes – Carga Útil en TM del Vehículo en Viaje", Value: f2(row.CargaUtilTm)},
	}
}
