package service

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"tukifac/internal/exchangerate"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/facturador"
	"tukifac/pkg/salecurrency"

	"gorm.io/gorm"
)

var (
	retentionSeriesRe  = regexp.MustCompile(`^R[A-Z0-9]{3}$`)
	perceptionSeriesRe = regexp.MustCompile(`^P[A-Z0-9]{3}$`)
)

// Tasa esperada por régimen SUNAT (Cat. 23 retención, Cat. 22 percepción).
var retentionRegimenTasa = map[string]float64{
	"01": 3.0,
}

var perceptionRegimenTasa = map[string]float64{
	"01": 2.0,
	"02": 1.0,
	"03": 0.5,
}

type fiscalPartyInput struct {
	TipoDoc   string
	NumDoc    string
	RznSocial string
	Address   string
	Ubigeo    string
}

type retentionPaymentInput struct {
	Moneda  string  `json:"moneda"`
	Importe float64 `json:"importe"`
	Fecha   string  `json:"fecha"`
}

// exchangeRateLookup consulta el tipo de cambio por día (AAAA-MM-DD); variable para poder inyectarlo en tests.
var exchangeRateLookup = func(day string) (*exchangerate.QueryResult, error) {
	return exchangerate.DefaultService().GetExchangeRate(day)
}

type retentionExchangeInput struct {
	MonedaRef string  `json:"moneda_ref"`
	MonedaObj string  `json:"moneda_obj"`
	Factor    float64 `json:"factor"`
	Fecha     string  `json:"fecha"`
}

func (s *BillingService) resolveDefaultBranchID(branchID uint) (uint, error) {
	if branchID > 0 {
		return branchID, nil
	}
	var cfg database.TenantCompanyConfig
	if err := s.db.Select("default_branch_id").First(&cfg).Error; err != nil {
		return 0, errors.New("configure una sucursal para emitir el comprobante")
	}
	if cfg.DefaultBranchID == nil || *cfg.DefaultBranchID == 0 {
		return 0, errors.New("configure la sucursal principal en Empresa")
	}
	return *cfg.DefaultBranchID, nil
}

func (s *BillingService) loadFiscalPartyFromContact(contactID uint, party *fiscalPartyInput) error {
	if contactID == 0 {
		return nil
	}
	var c database.TenantContact
	if err := s.db.First(&c, contactID).Error; err != nil {
		return errors.New("contacto no encontrado")
	}
	if strings.TrimSpace(party.TipoDoc) == "" {
		party.TipoDoc = strings.TrimSpace(c.DocType)
	}
	if strings.TrimSpace(party.NumDoc) == "" {
		party.NumDoc = strings.TrimSpace(c.DocNumber)
	}
	if strings.TrimSpace(party.RznSocial) == "" {
		party.RznSocial = strings.TrimSpace(c.BusinessName)
	}
	if strings.TrimSpace(party.Address) == "" {
		party.Address = strings.TrimSpace(c.Address)
	}
	if strings.TrimSpace(party.Ubigeo) == "" {
		party.Ubigeo = strings.TrimSpace(c.Ubigeo)
	}
	if strings.TrimSpace(party.Address) == "" || strings.TrimSpace(party.Ubigeo) == "" {
		return fmt.Errorf("complete dirección y ubigeo del contacto en Contactos antes de emitir (ID %d)", contactID)
	}
	return nil
}

func validateFiscalParty(party fiscalPartyInput, label string) error {
	if strings.TrimSpace(party.NumDoc) == "" {
		return fmt.Errorf("%s: número de documento es obligatorio", label)
	}
	if strings.TrimSpace(party.RznSocial) == "" {
		return fmt.Errorf("%s: razón social es obligatoria", label)
	}
	if strings.TrimSpace(party.Address) == "" || strings.TrimSpace(party.Ubigeo) == "" {
		return fmt.Errorf("%s: ubigeo y dirección son obligatorios para SUNAT", label)
	}
	if strings.TrimSpace(party.TipoDoc) == "" {
		return fmt.Errorf("%s: tipo de documento es obligatorio", label)
	}
	return nil
}

func validateRegimenTasa(regimen string, tasa float64, catalog map[string]float64, docLabel string) error {
	regimen = strings.TrimSpace(regimen)
	if regimen == "" {
		return fmt.Errorf("régimen %s es obligatorio", docLabel)
	}
	expected, ok := catalog[regimen]
	if !ok {
		return fmt.Errorf("régimen %s no válido para %s", regimen, docLabel)
	}
	if math.Abs(tasa-expected) > 0.011 {
		return fmt.Errorf("régimen %s exige tasa %.2f%% (recibido %.2f)", regimen, expected, tasa)
	}
	return nil
}

func validateRetentionSeries(series database.TenantDocumentSeries) error {
	code := strings.TrimSpace(series.SunatCode)
	if code != "20" {
		return errors.New("la serie debe ser de comprobante de retención (SUNAT 20)")
	}
	s := strings.ToUpper(strings.TrimSpace(series.Series))
	if !retentionSeriesRe.MatchString(s) {
		return errors.New("serie de retención debe tener formato R### (ej. R001)")
	}
	return nil
}

func validatePerceptionSeries(series database.TenantDocumentSeries) error {
	code := strings.TrimSpace(series.SunatCode)
	if code != "40" {
		return errors.New("la serie debe ser de comprobante de percepción (SUNAT 40)")
	}
	s := strings.ToUpper(strings.TrimSpace(series.Series))
	if !perceptionSeriesRe.MatchString(s) {
		return errors.New("serie de percepción debe tener formato P### (ej. P001)")
	}
	return nil
}

func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}

func moneyEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.02
}

func normalizeFiscalMoneda(m string) string {
	m = strings.ToUpper(strings.TrimSpace(m))
	if m == "" {
		return salecurrency.CurrencyPEN
	}
	return m
}

// fiscalDateTime lleva una fecha de entrada (solo día, con o sin hora, RFC3339) al formato que
// exige el facturador para TODA fecha de retención/percepción: Y-m-d\TH:i:sP (RFC3339 con
// offset de Lima). Un valor vacío equivale a "hoy" en Lima, como antes. Ver
// facturador.FiscalDateTimeFromInput para las reglas de zona horaria.
func fiscalDateTime(value, label string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return facturador.FormatFiscalDateTime(time.Now()), nil
	}
	out, err := facturador.FiscalDateTimeFromInput(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	return out, nil
}

// fiscalCalendarDay devuelve el día (AAAA-MM-DD, Lima) de una fecha de entrada; vacío = hoy en Lima.
// Se usa donde se necesita un día y no un instante (consulta de tipo de cambio).
func fiscalCalendarDay(value, label string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return facturador.FiscalCalendarDay(facturador.FormatFiscalDateTime(time.Now()))
	}
	out, err := facturador.FiscalCalendarDay(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	return out, nil
}

func (s *BillingService) resolveDetailExchange(moneda string, fechaEmision string, manual *retentionExchangeInput) (*facturador.RetentionExchange, error) {
	moneda = normalizeFiscalMoneda(moneda)
	if moneda == salecurrency.CurrencyPEN {
		return nil, nil
	}
	if manual != nil && manual.Factor > 0 {
		obj := strings.TrimSpace(manual.MonedaObj)
		if obj == "" {
			obj = salecurrency.CurrencyPEN
		}
		ref := strings.TrimSpace(manual.MonedaRef)
		if ref == "" {
			ref = moneda
		}
		// Fecha del tipo de cambio: la indicada o, si falta, la del comprobante relacionado.
		fuente := manual.Fecha
		if strings.TrimSpace(fuente) == "" {
			fuente = fechaEmision
		}
		fecha, err := fiscalDateTime(fuente, "fecha del tipo de cambio")
		if err != nil {
			return nil, err
		}
		return &facturador.RetentionExchange{
			MonedaRef: ref,
			MonedaObj: obj,
			Factor:    manual.Factor,
			Fecha:     fecha,
		}, nil
	}
	// La consulta del tipo de cambio es por DÍA (AAAA-MM-DD de Lima); el payload lleva el datetime.
	dia, err := fiscalCalendarDay(fechaEmision, "fecha del comprobante relacionado")
	if err != nil {
		return nil, err
	}
	fecha, err := fiscalDateTime(fechaEmision, "fecha del comprobante relacionado")
	if err != nil {
		return nil, err
	}
	res, err := exchangeRateLookup(dia)
	if err != nil {
		return nil, fmt.Errorf("tipo de cambio: %w", err)
	}
	if res == nil || !res.Success || res.Venta <= 0 {
		msg := "tipo de cambio no disponible"
		if res != nil && res.ErrorMessage != "" {
			msg = res.ErrorMessage
		}
		return nil, fmt.Errorf("tipo de cambio USD/PEN (%s): %s", dia, msg)
	}
	return &facturador.RetentionExchange{
		MonedaRef: moneda,
		MonedaObj: salecurrency.CurrencyPEN,
		Factor:    res.Venta,
		Fecha:     fecha,
	}, nil
}

func mapPaymentsInput(pagos []retentionPaymentInput, defaultMoneda, defaultFecha string) ([]facturador.RetentionPayment, error) {
	if len(pagos) == 0 {
		return nil, errors.New("se requiere al menos un pago/cobro por comprobante relacionado")
	}
	out := make([]facturador.RetentionPayment, len(pagos))
	for i, p := range pagos {
		moneda := normalizeFiscalMoneda(p.Moneda)
		if moneda == "" {
			moneda = normalizeFiscalMoneda(defaultMoneda)
		}
		if p.Importe <= 0 {
			return nil, fmt.Errorf("pago/cobro #%d: importe debe ser mayor a cero", i+1)
		}
		fuente := strings.TrimSpace(p.Fecha)
		if fuente == "" {
			fuente = defaultFecha
		}
		fecha, err := fiscalDateTime(fuente, fmt.Sprintf("pago/cobro #%d: fecha", i+1))
		if err != nil {
			return nil, err
		}
		out[i] =facturador.RetentionPayment{
			Moneda:  moneda,
			Importe: roundMoney(p.Importe),
			Fecha:   fecha,
		}
	}
	return out, nil
}

type retentionDetailBuildInput struct {
	TipoDoc        string
	NumDoc         string
	FechaEmision   string
	ImpTotal       float64
	Moneda         string
	Pagos          []retentionPaymentInput
	FechaRetencion string
	ImpRetenido    float64
	ImpPagar       float64
	TipoCambio     *retentionExchangeInput
}

var relatedDocNumRe = regexp.MustCompile(`^[A-Za-z0-9]{1,4}-\d{1,8}$`)
var relatedDocNumElectronicRe = regexp.MustCompile(`^[FBEfbe][A-Za-z0-9]{3}-\d{1,8}$`)

// validateRelatedDocument corta antes de enviar lo que SUNAT/Greenter rechaza sin explicar (el facturador solo devuelve
// «Server Error»): número con formato SERIE-CORRELATIVO y fecha del comprobante relacionado no posterior a la emisión.
func validateRelatedDocument(idx int, tipoDoc, numDoc, docFecha, emisionFecha string) error {
	tipoDoc = strings.TrimSpace(tipoDoc)
	numDoc = strings.TrimSpace(numDoc)
	re := relatedDocNumRe
	if tipoDoc == "01" || tipoDoc == "03" || tipoDoc == "07" || tipoDoc == "08" {
		re = relatedDocNumElectronicRe
	}
	if !re.MatchString(numDoc) {
		return fmt.Errorf("detalle #%d: el número de documento %q no tiene el formato SERIE-CORRELATIVO (ej. F001-00000123); corrija la serie y el número en la compra/venta origen", idx, numDoc)
	}
	dDoc, err1 := facturador.FiscalCalendarDay(docFecha)
	dEm, err2 := facturador.FiscalCalendarDay(emisionFecha)
	if err1 == nil && err2 == nil && dDoc > dEm {
		return fmt.Errorf("detalle #%d: la fecha del comprobante relacionado (%s) es posterior a la fecha de emisión (%s)", idx, dDoc, dEm)
	}
	return nil
}

func (s *BillingService) buildRetentionDetails(details []retentionDetailBuildInput, docFechaEmision string) ([]facturador.RetentionDetail, error) {
	if len(details) == 0 {
		return nil, errors.New("se requiere al menos un comprobante relacionado")
	}
	out := make([]facturador.RetentionDetail, len(details))
	for i, d := range details {
		if strings.TrimSpace(d.TipoDoc) == "" || strings.TrimSpace(d.NumDoc) == "" {
			return nil, fmt.Errorf("detalle #%d: tipo y número de documento son obligatorios", i+1)
		}
		if err := validateRelatedDocument(i+1, d.TipoDoc, d.NumDoc, d.FechaEmision, docFechaEmision); err != nil {
			return nil, err
		}
		if d.ImpTotal <= 0 {
			return nil, fmt.Errorf("detalle #%d: importe total debe ser mayor a cero", i+1)
		}
		moneda := normalizeFiscalMoneda(d.Moneda)
		fechaRet := strings.TrimSpace(d.FechaRetencion)
		if fechaRet == "" {
			fechaRet = docFechaEmision
		}
		pagos, err := mapPaymentsInput(d.Pagos, moneda, fechaRet)
		if err != nil {
			return nil, fmt.Errorf("detalle #%d: %w", i+1, err)
		}
		var paySum float64
		for _, p := range pagos {
			if normalizeFiscalMoneda(p.Moneda) != moneda {
				return nil, fmt.Errorf("detalle #%d: moneda de pago debe coincidir con moneda del documento", i+1)
			}
			paySum += p.Importe
		}
		if paySum > d.ImpTotal+0.02 {
			return nil, fmt.Errorf("detalle #%d: suma de pagos no puede superar importe total del documento", i+1)
		}
		if d.ImpRetenido < 0 || d.ImpPagar < 0 {
			return nil, fmt.Errorf("detalle #%d: importes retenido y neto no pueden ser negativos", i+1)
		}
		if !moneyEqual(d.ImpPagar, roundMoney(paySum-d.ImpRetenido)) {
			return nil, fmt.Errorf("detalle #%d: importe neto a pagar incoherente con pagos y retención", i+1)
		}
		exchange, err := s.resolveDetailExchange(moneda, d.FechaEmision, d.TipoCambio)
		if err != nil {
			return nil, fmt.Errorf("detalle #%d: %w", i+1, err)
		}
		fechaDoc, err := fiscalDateTime(d.FechaEmision, fmt.Sprintf("detalle #%d: fecha de emisión", i+1))
		if err != nil {
			return nil, err
		}
		fechaRetFiscal, err := fiscalDateTime(fechaRet, fmt.Sprintf("detalle #%d: fecha de retención", i+1))
		if err != nil {
			return nil, err
		}
		out[i] = facturador.RetentionDetail{
			TipoDoc:        strings.TrimSpace(d.TipoDoc),
			NumDoc:         strings.TrimSpace(d.NumDoc),
			FechaEmision:   fechaDoc,
			ImpTotal:       roundMoney(d.ImpTotal),
			Moneda:         moneda,
			Pagos:          pagos,
			FechaRetencion: fechaRetFiscal,
			ImpRetenido:    roundMoney(d.ImpRetenido),
			ImpPagar:       roundMoney(d.ImpPagar),
			TipoCambio:     exchange,
		}
	}
	return out, nil
}

type perceptionDetailBuildInput struct {
	TipoDoc         string
	NumDoc          string
	FechaEmision    string
	ImpTotal        float64
	Moneda          string
	Cobros          []retentionPaymentInput
	FechaPercepcion string
	ImpPercibido    float64
	ImpCobrar       float64
	TipoCambio      *retentionExchangeInput
}

func (s *BillingService) buildPerceptionDetails(details []perceptionDetailBuildInput, docFechaEmision string) ([]facturador.PerceptionDetail, error) {
	if len(details) == 0 {
		return nil, errors.New("se requiere al menos un comprobante relacionado")
	}
	out := make([]facturador.PerceptionDetail, len(details))
	for i, d := range details {
		if strings.TrimSpace(d.TipoDoc) == "" || strings.TrimSpace(d.NumDoc) == "" {
			return nil, fmt.Errorf("detalle #%d: tipo y número de documento son obligatorios", i+1)
		}
		if err := validateRelatedDocument(i+1, d.TipoDoc, d.NumDoc, d.FechaEmision, docFechaEmision); err != nil {
			return nil, err
		}
		if d.ImpTotal <= 0 {
			return nil, fmt.Errorf("detalle #%d: importe total debe ser mayor a cero", i+1)
		}
		moneda := normalizeFiscalMoneda(d.Moneda)
		fechaPerc := strings.TrimSpace(d.FechaPercepcion)
		if fechaPerc == "" {
			fechaPerc = docFechaEmision
		}
		cobros, err := mapPaymentsInput(d.Cobros, moneda, fechaPerc)
		if err != nil {
			return nil, fmt.Errorf("detalle #%d: %w", i+1, err)
		}
		var cobSum float64
		for _, c := range cobros {
			if normalizeFiscalMoneda(c.Moneda) != moneda {
				return nil, fmt.Errorf("detalle #%d: moneda de cobro debe coincidir con moneda del documento", i+1)
			}
			cobSum += c.Importe
		}
		if cobSum > d.ImpTotal+0.02 {
			return nil, fmt.Errorf("detalle #%d: suma de cobros no puede superar importe total del documento", i+1)
		}
		if !moneyEqual(d.ImpCobrar, roundMoney(cobSum-d.ImpPercibido)) {
			return nil, fmt.Errorf("detalle #%d: importe neto a cobrar incoherente con cobros y percepción", i+1)
		}
		exchange, err := s.resolveDetailExchange(moneda, d.FechaEmision, d.TipoCambio)
		if err != nil {
			return nil, fmt.Errorf("detalle #%d: %w", i+1, err)
		}
		fechaDoc, err := fiscalDateTime(d.FechaEmision, fmt.Sprintf("detalle #%d: fecha de emisión", i+1))
		if err != nil {
			return nil, err
		}
		fechaPercFiscal, err := fiscalDateTime(fechaPerc, fmt.Sprintf("detalle #%d: fecha de percepción", i+1))
		if err != nil {
			return nil, err
		}
		out[i] = facturador.PerceptionDetail{
			TipoDoc:         strings.TrimSpace(d.TipoDoc),
			NumDoc:          strings.TrimSpace(d.NumDoc),
			FechaEmision:    fechaDoc,
			ImpTotal:        roundMoney(d.ImpTotal),
			Moneda:          moneda,
			Cobros:          cobros,
			FechaPercepcion: fechaPercFiscal,
			ImpPercibido:    roundMoney(d.ImpPercibido),
			ImpCobrar:       roundMoney(d.ImpCobrar),
			TipoCambio:      exchange,
		}
	}
	return out, nil
}

func validateRetentionTotals(details []facturador.RetentionDetail, impRetenido, impPagado float64) error {
	var sumRet, sumNet float64
	for _, d := range details {
		sumRet += d.ImpRetenido
		sumNet += d.ImpPagar
	}
	sumRet = roundMoney(sumRet)
	sumNet = roundMoney(sumNet)
	if !moneyEqual(sumRet, impRetenido) {
		return fmt.Errorf("importe total retenido (%.2f) debe igualar suma de detalle (%.2f)", impRetenido, sumRet)
	}
	if !moneyEqual(sumNet, impPagado) {
		return fmt.Errorf("importe total pagado (%.2f) debe igualar suma de detalle (%.2f)", impPagado, sumNet)
	}
	return nil
}

func validatePerceptionTotals(details []facturador.PerceptionDetail, impPercibido, impCobrado float64) error {
	var sumPerc, sumNet float64
	for _, d := range details {
		sumPerc += d.ImpPercibido
		sumNet += d.ImpCobrar
	}
	sumPerc = roundMoney(sumPerc)
	sumNet = roundMoney(sumNet)
	if !moneyEqual(sumPerc, impPercibido) {
		return fmt.Errorf("importe total percibido (%.2f) debe igualar suma de detalle (%.2f)", impPercibido, sumPerc)
	}
	if !moneyEqual(sumNet, impCobrado) {
		return fmt.Errorf("importe total cobrado (%.2f) debe igualar suma de detalle (%.2f)", impCobrado, sumNet)
	}
	return nil
}

func (s *BillingService) reserveRetentionPerceptionSeries(branchID, seriesID uint, validateFn func(database.TenantDocumentSeries) error) (database.TenantDocumentSeries, uint, string, error) {
	branchID, err := s.resolveDefaultBranchID(branchID)
	if err != nil {
		return database.TenantDocumentSeries{}, 0, "", err
	}
	if seriesID == 0 {
		return database.TenantDocumentSeries{}, 0, "", errors.New("seleccione una serie documental")
	}
	series, err := docseries.ValidateForBranch(s.db, seriesID, branchID)
	if err != nil {
		if errors.Is(err, docseries.ErrSeriesNotFound) {
			return series, 0, "", errors.New("serie no encontrada")
		}
		if errors.Is(err, docseries.ErrSeriesInactive) {
			return series, 0, "", errors.New("la serie seleccionada está inactiva; actívela en Empresa → Series")
		}
		if errors.Is(err, docseries.ErrSeriesWrongBranch) {
			return series, 0, "", errors.New("la serie no pertenece a la sucursal seleccionada")
		}
		return series, 0, "", fmt.Errorf("serie no válida: %w", err)
	}
	if err := validateFn(series); err != nil {
		return series, 0, "", err
	}
	nextCorr, err := docseries.ReserveNextStandalone(s.db, seriesID)
	if err != nil {
		return series, 0, "", fmt.Errorf("reservar correlativo: %w", err)
	}
	return series, nextCorr, strconvFormatCorrelative(nextCorr), nil
}

func (s *BillingService) reserveRetentionPerceptionSeriesTx(tx *gorm.DB, branchID, seriesID uint, validateFn func(database.TenantDocumentSeries) error) (database.TenantDocumentSeries, uint, string, error) {
	branchID, err := s.resolveDefaultBranchID(branchID)
	if err != nil {
		return database.TenantDocumentSeries{}, 0, "", err
	}
	if seriesID == 0 {
		return database.TenantDocumentSeries{}, 0, "", errors.New("seleccione una serie documental")
	}
	series, err := docseries.ValidateForBranch(tx, seriesID, branchID)
	if err != nil {
		if errors.Is(err, docseries.ErrSeriesNotFound) {
			return series, 0, "", errors.New("serie no encontrada")
		}
		if errors.Is(err, docseries.ErrSeriesInactive) {
			return series, 0, "", errors.New("la serie seleccionada está inactiva; actívela en Empresa → Series")
		}
		if errors.Is(err, docseries.ErrSeriesWrongBranch) {
			return series, 0, "", errors.New("la serie no pertenece a la sucursal seleccionada")
		}
		return series, 0, "", fmt.Errorf("serie no válida: %w", err)
	}
	if err := validateFn(series); err != nil {
		return series, 0, "", err
	}
	nextCorr, _, err := docseries.ReserveNext(tx, seriesID)
	if err != nil {
		return series, 0, "", fmt.Errorf("reservar correlativo: %w", err)
	}
	return series, nextCorr, strconvFormatCorrelative(nextCorr), nil
}

func strconvFormatCorrelative(n uint) string {
	return fmt.Sprintf("%d", n)
}

func validateReversionDetail(tipoDoc, serie, correlativo, motivo string) error {
	tipoDoc = strings.TrimSpace(tipoDoc)
	serie = strings.ToUpper(strings.TrimSpace(serie))
	correlativo = strings.TrimSpace(correlativo)
	motivo = strings.TrimSpace(motivo)
	if tipoDoc != "20" && tipoDoc != "40" {
		return errors.New("tipo de documento a revertir debe ser 20 (retención) o 40 (percepción)")
	}
	if correlativo == "" {
		return errors.New("correlativo del comprobante a revertir es obligatorio")
	}
	if motivo == "" {
		return errors.New("motivo de reversión es obligatorio")
	}
	if len(motivo) > 100 {
		return errors.New("motivo de reversión no puede superar 100 caracteres")
	}
	if tipoDoc == "20" && !retentionSeriesRe.MatchString(serie) {
		return errors.New("serie de retención a revertir debe tener formato R###")
	}
	if tipoDoc == "40" && !perceptionSeriesRe.MatchString(serie) {
		return errors.New("serie de percepción a revertir debe tener formato P###")
	}
	return nil
}

// validateFiscalEmissionNotFuture rechaza una fecha de emisión posterior a hoy (día de Lima): SUNAT no admite comprobantes
// con fecha futura. La fecha ya viene normalizada por fiscalDateTime.
func validateFiscalEmissionNotFuture(fechaEmision string) error {
	day, err := facturador.FiscalCalendarDay(fechaEmision)
	if err != nil {
		return nil
	}
	today, err := facturador.FiscalCalendarDay(facturador.FormatFiscalDateTime(time.Now()))
	if err != nil {
		return nil
	}
	if day > today {
		return fmt.Errorf("la fecha de emisión (%s) no puede ser posterior a hoy (%s)", day, today)
	}
	return nil
}
