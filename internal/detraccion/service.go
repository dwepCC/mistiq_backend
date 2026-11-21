package detraccion

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	sunatdet "tukifac/pkg/sunat/detraccion"
	"tukifac/pkg/taxpayment"

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
	// N° de constancia de pago (opcional, 1001 y 1004) — ver comentario en
	// database.TenantSaleDetraccion.PayConstancyNumber. Solo referencia, no viaja a SUNAT.
	PayConstancyNumber string `json:"pay_constancy_number,omitempty"`
	// Campos exclusivos de 1004 (transporte de carga por vía terrestre), enviados al comprobante
	// como cac:InvoiceLine/cac:Item/cac:AdditionalItemProperty (Catálogo N° 55). Obligatorios
	// cuando OperationTypeCode=1004 (ver validateTransporteFields): solo origen y destino.
	// ValorReferencialPen, CargaEfectivaTm, CargaUtilTm, MtcRegistro, ConfiguracionVehicular y
	// TripDetail son opcionales. ValorReferencialPen además NUNCA participa en el cálculo de la
	// detracción (decisión de negocio, ver evaluate) — es puramente informativo/impreso.
	ValorReferencialPen    float64 `json:"valor_referencial_pen,omitempty"`
	MtcRegistro            string  `json:"mtc_registro,omitempty"`
	ConfiguracionVehicular string  `json:"configuracion_vehicular,omitempty"`
	PuntoOrigen            string  `json:"punto_origen,omitempty"`
	PuntoDestino           string  `json:"punto_destino,omitempty"`
	CargaEfectivaTm        float64 `json:"carga_efectiva_tm,omitempty"`
	CargaUtilTm            float64 `json:"carga_util_tm,omitempty"`
	// Detalle del viaje (obligatorio en 1004) — ver comentario en
	// database.TenantSaleDetraccion.TripDetail. Solo referencia interna, no viaja a SUNAT.
	TripDetail string `json:"trip_detail,omitempty"`
	// Ubigeo (cat. 13, 6 dígitos) y dirección detallada del origen y del destino, y valores
	// referenciales (PEN) de la carga efectiva y de la carga útil nominal. SUNAT los exige en el XML
	// de 1004 (errores 3116-3126) — ver validateTransporteFields y ApplyToInvoicePayload.
	OriginUbigeo             string  `json:"origin_ubigeo,omitempty"`
	OriginAddress            string  `json:"origin_address,omitempty"`
	DestinationUbigeo        string  `json:"destination_ubigeo,omitempty"`
	DestinationAddress       string  `json:"destination_address,omitempty"`
	ValorRefCargaEfectivaPen float64 `json:"valor_ref_carga_efectiva_pen,omitempty"`
	ValorRefCargaUtilPen     float64 `json:"valor_ref_carga_util_pen,omitempty"`
	// SelfDetraction: autodetracción — el cliente paga el TOTAL al proveedor y este deposita la detracción en su
	// propia cuenta BN. Solo cambia el cobro interno (cuotas y saldo por el total, sin pago "Detracción BN"); el
	// XML, el PDF fiscal y el facturador reciben la detracción y las cuotas netas de siempre. Se guarda como
	// bn_confirmation_status = taxpayment.BnStatusSelfPending (sin columna nueva ni migración).
	SelfDetraction bool `json:"self_detraction,omitempty"`
}

// PersistInput datos completos para guardar detracción.
type PersistInput struct {
	SaleID              uint
	OperationTypeCode   string
	SunatDocCode        string
	Currency            string
	ExchangeRate        *float64
	SaleTotal           float64
	GravadoTotal        float64
	BankAccount         string
	PaymentMethodCode   string
	Detraccion          *SaleInput
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
		// ValorReferencialPEN deliberadamente NO se envía: a pedido del negocio, el valor
		// referencial es una captura manual sin tabla oficial que la respalde en este sistema, y no
		// debe poder inflar la detracción por encima de lo que realmente se vendió. La detracción de
		// 1004 se calcula siempre sobre el importe real de la venta; el campo queda solo para
		// mostrarse impreso en el comprobante (ver PrintFiscalContext.detraccion_valor_referencial).
	})
}

// validateTransporteFields exige, para 1004, lo que SUNAT valida en el XML (Anexo 1 de la R.S.
// 123-2022 y catálogo de errores, "Detracciones - Servicio de transporte de carga"):
//   - 3116/3118: ubigeo (cat. 13, 6 dígitos) del punto de origen y del destino.
//   - 3117/3119: dirección detallada del origen y del destino.
//   - 3120: detalle del viaje.
//   - 3121-3126: UN valor referencial del servicio de transporte, UNO sobre la carga efectiva y UNO
//     sobre la carga útil nominal, en PEN.
//
// Opcionales: registro MTC, configuración vehicular y las TM de carga efectiva/útil (ya no son los
// valores referenciales). El valor referencial NUNCA participa en el cálculo de la detracción
// (decisión de negocio, ver evaluate): es captura manual sin la tabla oficial del MTC (D.S.
// 020-2021-MTC) y no debe inflar la detracción por encima de lo realmente vendido; solo viaja al XML y
// se imprime. Se juntan todos los faltantes en un solo mensaje para que el usuario los corrija de una vez.
func validateTransporteFields(in *SaleInput) error {
	var missing []string
	checkPoint := func(label, ubigeo, address string) {
		u := strings.TrimSpace(ubigeo)
		if !isUbigeo(u) {
			missing = append(missing, "ubigeo del punto de "+label+" (6 dígitos)")
		}
		if strings.TrimSpace(address) == "" {
			missing = append(missing, "dirección detallada del punto de "+label)
		}
	}
	checkPoint("origen", in.OriginUbigeo, in.OriginAddress)
	checkPoint("destino", in.DestinationUbigeo, in.DestinationAddress)
	if strings.TrimSpace(in.TripDetail) == "" {
		missing = append(missing, "detalle del viaje")
	}
	if in.ValorReferencialPen <= 0 {
		missing = append(missing, "valor referencial del servicio de transporte")
	}
	if in.ValorRefCargaEfectivaPen <= 0 {
		missing = append(missing, "valor referencial sobre la carga efectiva")
	}
	if in.ValorRefCargaUtilPen <= 0 {
		missing = append(missing, "valor referencial sobre la carga útil nominal")
	}
	if len(missing) > 0 {
		return errors.New("detracción por transporte de carga: falta " + strings.Join(missing, ", "))
	}
	return nil
}

// isUbigeo indica si s tiene el formato del catálogo 13 de SUNAT: exactamente 6 dígitos.
func isUbigeo(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
	if in.Detraccion != nil {
		row.PayConstancyNumber = strings.TrimSpace(in.Detraccion.PayConstancyNumber)
		if in.Detraccion.SelfDetraction {
			row.BnConfirmationStatus = taxpayment.BnStatusSelfPending
		}
	}
	if op == sunatdet.OpDetraccionTransporte && in.Detraccion != nil {
		row.MtcRegistro = strings.TrimSpace(in.Detraccion.MtcRegistro)
		row.ConfiguracionVehicular = strings.TrimSpace(in.Detraccion.ConfiguracionVehicular)
		row.PuntoOrigen = strings.TrimSpace(in.Detraccion.PuntoOrigen)
		row.PuntoDestino = strings.TrimSpace(in.Detraccion.PuntoDestino)
		// Valor referencial, carga efectiva y carga útil ahora son opcionales (ver
		// validateTransporteFields): si el usuario no las completó llegan en 0, y un puntero a 0.0
		// formatearía "0.00" en transporteAttributes en vez de omitirse — solo se guarda el
		// puntero cuando hay un valor real.
		if in.Detraccion.ValorReferencialPen > 0 {
			vr := in.Detraccion.ValorReferencialPen
			row.ValorReferencialPen = &vr
		}
		if in.Detraccion.CargaEfectivaTm > 0 {
			ce := in.Detraccion.CargaEfectivaTm
			row.CargaEfectivaTm = &ce
		}
		if in.Detraccion.CargaUtilTm > 0 {
			cu := in.Detraccion.CargaUtilTm
			row.CargaUtilTm = &cu
		}
		row.TripDetail = strings.TrimSpace(in.Detraccion.TripDetail)
		row.OriginUbigeo = strings.TrimSpace(in.Detraccion.OriginUbigeo)
		row.OriginAddress = strings.TrimSpace(in.Detraccion.OriginAddress)
		row.DestinationUbigeo = strings.TrimSpace(in.Detraccion.DestinationUbigeo)
		row.DestinationAddress = strings.TrimSpace(in.Detraccion.DestinationAddress)
		// PuntoOrigen/PuntoDestino son la etiqueta legible (la que se imprime); si el cliente no la
		// mandó, se arma con la dirección detallada.
		if row.PuntoOrigen == "" {
			row.PuntoOrigen = row.OriginAddress
		}
		if row.PuntoDestino == "" {
			row.PuntoDestino = row.DestinationAddress
		}
		if in.Detraccion.ValorRefCargaEfectivaPen > 0 {
			v := in.Detraccion.ValorRefCargaEfectivaPen
			row.ValorRefCargaEfectivaPen = &v
		}
		if in.Detraccion.ValorRefCargaUtilPen > 0 {
			v := in.Detraccion.ValorRefCargaUtilPen
			row.ValorRefCargaUtilPen = &v
		}
	}
	if err := s.db.Save(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// SelfDetractionAmount devuelve el monto de detracción de una venta con AUTODETRACCIÓN, o 0 si la venta no la
// tiene (sin detracción o detracción normal). Lo usan el payload a SUNAT y el PDF para presentar las cuotas
// FISCALES (netas) aunque internamente se cobren por el total.
func (s *Service) SelfDetractionAmount(saleID uint) float64 {
	row, err := s.LoadBySaleID(saleID)
	if err != nil || row == nil || !taxpayment.IsSelfDetractionStatus(row.BnConfirmationStatus) {
		return 0
	}
	return row.DetractionAmountPen
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
		// Nodos cac:InvoiceLine/cac:Delivery (origen, destino, viaje, valores referenciales): SUNAT los
		// quiere una sola vez, así que van en la primera línea. El facturador los convierte a XML.
		if delivery := transporteDeliveryAttributes(row); len(delivery) > 0 && len(payload.Details) > 0 {
			first := make([]facturador.DetailAttribute, 0, len(attrs)+len(delivery))
			first = append(first, attrs...)
			first = append(first, delivery...)
			payload.Details[0].Atributos = first
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
	// Registro MTC (3006) y configuración vehicular (3007) son opcionales (ver
	// validateTransporteFields) — se omiten del comprobante si el usuario no los completó, en vez
	// de mandar un AdditionalItemProperty con Value vacío.
	all := []facturador.DetailAttribute{
		{Code: "3006", Name: "Detracciones: Transporte Bienes vía terrestre – Numero Registro MTC", Value: row.MtcRegistro},
		{Code: "3007", Name: "Detracciones: Transporte Bienes vía terrestre – Configuración Vehicular", Value: row.ConfiguracionVehicular},
	}
	if !hasTransportDelivery(row) {
		// Fila anterior a la v163 (solo texto libre, sin ubigeo): se mantiene el envío de siempre.
		all = append(all,
			facturador.DetailAttribute{Code: "3008", Name: "Detracciones: Transporte Bienes vía terrestre – Punto de Origen", Value: row.PuntoOrigen},
			facturador.DetailAttribute{Code: "3009", Name: "Detracciones: Transporte Bienes vía terrestre – Punto Destino", Value: row.PuntoDestino},
			facturador.DetailAttribute{Code: "3010", Name: "Detracciones: Transporte Bienes – Valor Referencial Preliminar por Viaje", Value: f2(row.ValorReferencialPen)},
		)
	}
	all = append(all,
		facturador.DetailAttribute{Code: "3013", Name: "Detracciones: Transporte Bienes – Carga Efectiva en TM por Vehículo", Value: f2(row.CargaEfectivaTm)},
		facturador.DetailAttribute{Code: "3014", Name: "Detracciones: Transporte Bienes – Carga Útil en TM del Vehículo en Viaje", Value: f2(row.CargaUtilTm)},
	)
	attrs := make([]facturador.DetailAttribute, 0, len(all))
	for _, a := range all {
		if strings.TrimSpace(a.Value) == "" {
			continue
		}
		attrs = append(attrs, a)
	}
	return attrs
}

// hasTransportDelivery indica si la fila trae los datos que SUNAT exige en cac:Delivery (v163):
// ubigeo y dirección de origen y destino. Las filas anteriores solo tienen texto libre.
func hasTransportDelivery(row *database.TenantSaleDetraccion) bool {
	return isUbigeo(row.OriginUbigeo) && isUbigeo(row.DestinationUbigeo) &&
		strings.TrimSpace(row.OriginAddress) != "" && strings.TrimSpace(row.DestinationAddress) != ""
}

// Códigos reservados que el facturador (App\Xml\Transporte1004) convierte en cac:Delivery. NO son
// códigos del catálogo 55: el facturador los retira de los AdditionalItemProperty.
const (
	attrOrigUbigeo  = "T1004_ORIG_UBIGEO"
	attrOrigDir     = "T1004_ORIG_DIR"
	attrDestUbigeo  = "T1004_DEST_UBIGEO"
	attrDestDir     = "T1004_DEST_DIR"
	attrViaje       = "T1004_VIAJE"
	attrVRServicio  = "T1004_VR_SERVICIO"
	attrVRCargaEfec = "T1004_VR_CARGA_EFECTIVA"
	attrVRCargaUtil = "T1004_VR_CARGA_UTIL"
)

// transporteDeliveryAttributes arma los datos de cac:InvoiceLine/cac:Delivery de 1004 (SUNAT, Anexo 1
// de la R.S. 123-2022, campos 113-118): origen -> Despatch/DespatchAddress, destino ->
// DeliveryLocation/Address, detalle del viaje -> Despatch/Instructions y valores referenciales ->
// DeliveryTerms 01/02/03. Vacío si la fila no los tiene completos (filas anteriores a la v163).
func transporteDeliveryAttributes(row *database.TenantSaleDetraccion) []facturador.DetailAttribute {
	if !hasTransportDelivery(row) {
		return nil
	}
	f2 := func(v *float64) string {
		if v == nil {
			return "0.00"
		}
		return strconv.FormatFloat(*v, 'f', 2, 64)
	}
	trip := strings.TrimSpace(row.TripDetail)
	return []facturador.DetailAttribute{
		{Code: attrOrigUbigeo, Name: "Origen ubigeo", Value: strings.TrimSpace(row.OriginUbigeo)},
		{Code: attrOrigDir, Name: "Origen dirección", Value: strings.TrimSpace(row.OriginAddress)},
		{Code: attrDestUbigeo, Name: "Destino ubigeo", Value: strings.TrimSpace(row.DestinationUbigeo)},
		{Code: attrDestDir, Name: "Destino dirección", Value: strings.TrimSpace(row.DestinationAddress)},
		{Code: attrViaje, Name: "Detalle del viaje", Value: trip},
		{Code: attrVRServicio, Name: "Valor referencial servicio de transporte", Value: f2(row.ValorReferencialPen)},
		{Code: attrVRCargaEfec, Name: "Valor referencial carga efectiva", Value: f2(row.ValorRefCargaEfectivaPen)},
		{Code: attrVRCargaUtil, Name: "Valor referencial carga útil nominal", Value: f2(row.ValorRefCargaUtilPen)},
	}
}
