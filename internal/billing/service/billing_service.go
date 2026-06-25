package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tukifac/config"
	salesvc "tukifac/internal/sales/service"
	detraccionsvc "tukifac/internal/detraccion"
	"tukifac/internal/fiscal/salecontext"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/facturador"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/sunat"
	"tukifac/pkg/tax"
	"tukifac/pkg/tenantstorage"

	"gorm.io/gorm"
)

type BillingService struct {
	db              *gorm.DB
	baseURL         string
	token           string
	orchestrator    *InvoiceOrchestrator
	centralTenantID uint // ID tenant en BD central (cuota documentos)
	tenantSlug      string
}

// SetCentralTenantID asocia el tenant SaaS para control de cupo documentos.
func (s *BillingService) SetCentralTenantID(id uint) { s.centralTenantID = id }

// SetTenantSlug asocia slug SaaS (webhook / fiscal async).
func (s *BillingService) SetTenantSlug(slug string) { s.tenantSlug = slug }

func (s *BillingService) facturadorConfigured() bool {
	return s.baseURL != "" && s.token != ""
}

func NewBillingService(db *gorm.DB) *BillingService {
	svc := &BillingService{db: db}
	if config.AppConfig.FacturadorBaseURL == "" || config.AppConfig.FacturadorToken == "" {
		svc.orchestrator = NewInvoiceOrchestrator(db, &fiscalEmitterAdapter{svc: svc})
		return svc
	}
	svc.baseURL = strings.TrimSuffix(config.AppConfig.FacturadorBaseURL, "/")
	svc.token = config.AppConfig.FacturadorToken
	svc.orchestrator = NewInvoiceOrchestrator(db, &fiscalEmitterAdapter{svc: svc})
	return svc
}

// round2 redondea a 2 decimales para montos SUNAT (evita discrepancias 4310/4312).
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// resolveUbigeoToAddress obtiene los nombres de departamento, provincia y distrito desde las tablas de ubigeo.
// SUNAT no acepta "-" en estos campos; se debe enviar el nombre real.
func (s *BillingService) resolveUbigeoToAddress(ubigeo string) (dep, prov, dist string, err error) {
	ubigeo = strings.TrimSpace(ubigeo)
	if ubigeo == "" {
		return "", "", "", fmt.Errorf("ubigeo es obligatorio para el domicilio fiscal")
	}
	var distrito database.UbiDistrito
	if err := s.db.Where("id = ?", ubigeo).First(&distrito).Error; err != nil || distrito.ID == "" {
		return "", "", "", fmt.Errorf("ubigeo %s no encontrado en catálogo; configure un distrito válido", ubigeo)
	}
	var provincia database.UbiProvincia
	if err := s.db.Where("id = ?", distrito.ProvinciaID).First(&provincia).Error; err != nil || provincia.ID == "" {
		return "", "", "", fmt.Errorf("provincia del ubigeo no encontrada")
	}
	var region database.UbiRegion
	if err := s.db.Where("id = ?", distrito.RegionID).First(&region).Error; err != nil || region.ID == "" {
		return "", "", "", fmt.Errorf("departamento del ubigeo no encontrado")
	}
	return region.Nombre, provincia.Nombre, distrito.Nombre, nil
}

// buildInvoiceAddressFromUbigeo arma una InvoiceAddress con nombres reales (sin "-") para SUNAT.
func (s *BillingService) buildInvoiceAddressFromUbigeo(ubigeo, direccion string) (facturador.InvoiceAddress, error) {
	ubigeo = strings.TrimSpace(ubigeo)
	direccion = strings.TrimSpace(direccion)
	if ubigeo == "" || direccion == "" {
		return facturador.InvoiceAddress{}, fmt.Errorf("ubigeo y dirección son obligatorios para SUNAT")
	}
	dep, prov, dist, err := s.resolveUbigeoToAddress(ubigeo)
	if err != nil {
		return facturador.InvoiceAddress{}, err
	}
	return facturador.InvoiceAddress{
		Ubigueo: ubigeo, CodigoPais: "PE",
		Departamento: dep, Provincia: prov, Distrito: dist,
		Urbanizacion: "", Direccion: direccion,
	}, nil
}

// GetNotificationCounts devuelve cantidades de comprobantes electrónicos por estado (solo 01, 03, 07, 08).
// Para notificaciones en el header del tenant.
func (s *BillingService) GetNotificationCounts() (pending, errorCount, rejected int64, err error) {
	electronicCodes := []string{"01", "03", "07", "08", "09", "31"}
	for _, status := range []string{"pending", "error", "rejected"} {
		var n int64
		e := s.db.Model(&database.TenantSale{}).
			Joins("JOIN tenant_document_series ON tenant_document_series.id = tenant_sales.series_id").
			Where("tenant_document_series.sunat_code IN ?", electronicCodes).
			Where("tenant_sales.billing_status = ?", status).
			Count(&n).Error
		if e != nil {
			return 0, 0, 0, e
		}
		switch status {
		case "pending":
			pending = n
		case "error":
			errorCount = n
		case "rejected":
			rejected = n
		}
	}
	return pending, errorCount, rejected, nil
}

// SendToSUNAT encola emisión fiscal en el facturador (único camino).
func (s *BillingService) SendToSUNAT(saleID uint) (*database.TenantInvoice, error) {
	if s.orchestrator == nil {
		s.orchestrator = NewInvoiceOrchestrator(s.db, &fiscalEmitterAdapter{svc: s})
	}
	return s.orchestrator.SendToSUNAT(saleID)
}

func (s *BillingService) sendToFacturador(saleID uint, companyCfg *database.TenantCompanyConfig) (*database.TenantInvoice, error) {
	if s.baseURL == "" || s.token == "" {
		return nil, errors.New("URL o token del facturador no configurados — configura FACTURADOR_BASE_URL y FACTURADOR_TOKEN en el servidor")
	}
	if err := requireFiscalClient(); err != nil {
		return nil, err
	}
	return s.emitFiscalDocumentBySale(saleID, companyCfg)
}

func (s *BillingService) emitInvoiceDocument(saleID uint, companyCfg *database.TenantCompanyConfig) (*database.TenantInvoice, error) {
	var sale database.TenantSale
	if err := s.db.First(&sale, saleID).Error; err != nil {
		return nil, errors.New("venta no encontrada")
	}
	var items []database.TenantSaleItem
	s.db.Where("sale_id = ?", saleID).Find(&items)
	var contact database.TenantContact
	if sale.ContactID != nil {
		s.db.First(&contact, *sale.ContactID)
	}
	tipoDoc := "03"
	if sale.DocType == "FACTURA" || strings.TrimSpace(getSeriesSunatCode(s.db, sale.SeriesID)) == "01" {
		tipoDoc = "01"
	}
	fechaEmision := facturador.FormatFiscalDateTime(sale.IssueDate)
	ubigueo := strings.TrimSpace(companyCfg.Ubigeo)
	if ubigueo == "" {
		return nil, fmt.Errorf("configure el ubigeo del domicilio fiscal en Configuración → Empresa")
	}
	dep, prov, dist, err := s.resolveUbigeoToAddress(ubigueo)
	if err != nil {
		return nil, err
	}
	direccionEmpresa := strings.TrimSpace(companyCfg.Address)
	if direccionEmpresa == "" {
		return nil, fmt.Errorf("configure la dirección completa del domicilio fiscal en Configuración → Empresa")
	}
	companyAddr := facturador.InvoiceAddress{
		Ubigueo:      ubigueo,
		CodigoPais:   "PE",
		Departamento: dep,
		Provincia:    prov,
		Distrito:     dist,
		Urbanizacion: "",
		Direccion:    direccionEmpresa,
	}
	tipoOperacion := strings.TrimSpace(sale.OperationTypeCode)
	if tipoOperacion == "" {
		tipoOperacion = salecontext.DefaultOperationType
	}
	// Cliente: tipoDoc catálogo 06 (1=DNI, 6=RUC, 4=CE…); numDoc obligatorio
	clientTipoDoc := "6"
	clientNumDoc := "00000000000"
	clientRzn := "CLIENTE VARIOS"
	clientDir := ""
	clientUbigeo := ""
	if contact.ID > 0 {
		clientRzn = contact.BusinessName
		clientNumDoc = contact.DocNumber
		clientDir = strings.TrimSpace(contact.Address)
		clientUbigeo = strings.TrimSpace(contact.Ubigeo)
		clientDir, clientUbigeo = database.NormalizeTenantContactAddressUbigeo(clientDir, clientUbigeo)
		switch strings.ToUpper(contact.DocType) {
		case "DNI", "1":
			clientTipoDoc = "1"
		case "RUC", "6":
			clientTipoDoc = "6"
		case "CE", "4", "CARNET":
			clientTipoDoc = "4"
		case "PASAPORTE", "7":
			clientTipoDoc = "7"
		default:
			if len(contact.DocNumber) == 8 {
				clientTipoDoc = "1"
			} else if len(contact.DocNumber) == 11 {
				clientTipoDoc = "6"
			}
		}
	}
	if clientRzn == "" {
		clientRzn = "CLIENTE VARIOS"
	}
	if clientNumDoc == "" {
		clientNumDoc = "00000000000"
	}
	// Cliente no documentado (Lycet: schemeID="0" en XML): enviar tipoDoc "0" y numDoc "99999999999"
	if clientNumDoc == "00000000000" || clientNumDoc == "00000000" || clientNumDoc == "99999999" ||
		(contact.ID > 0 && (strings.ToUpper(strings.TrimSpace(contact.DocType)) == "SIN DOCUMENTO" || strings.TrimSpace(contact.DocType) == "0")) {
		clientTipoDoc = "0"
		clientNumDoc = "99999999999"
	}
	// Dirección cliente: SUNAT no acepta "-"; debe ser dirección y ubigeo reales
	clientAddr := facturador.InvoiceAddress{Ubigueo: "150101", CodigoPais: "PE", Departamento: "Lima", Provincia: "Lima", Distrito: "Lima", Urbanizacion: "", Direccion: clientDir}
	if contact.ID > 0 {
		depC, provC, distC, errC := s.resolveUbigeoToAddress(clientUbigeo)
		if errC != nil {
			return nil, fmt.Errorf("cliente: %w", errC)
		}
		clientAddr = facturador.InvoiceAddress{
			Ubigueo: clientUbigeo, CodigoPais: "PE",
			Departamento: depC, Provincia: provC, Distrito: distC,
			Urbanizacion: "", Direccion: clientDir,
		}
	} else {
		// Cliente genérico (sin contacto): SUNAT exige dirección real; no se acepta "-"
		return nil, errors.New("para facturación electrónica debe asignar un cliente con dirección y ubigeo completos en la venta")
	}
	companyTaxRate, err := s.resolveCompanyTaxRate()
	if err != nil {
		return nil, err
	}
	details := make([]facturador.InvoiceDetail, len(items))
	for i, item := range items {
		aff := strings.TrimSpace(item.IgvAffectationType)
		if aff == "" {
			return nil, fmt.Errorf("el ítem «%s» no tiene tipo de afectación IGV; configúrelo en el producto (Catálogo SUNAT 07: 10 Gravado, 20 Exonerado, 30 Inafecto)", strings.TrimSpace(item.Description))
		}
		if aff == "10" && item.TaxRate <= 0 {
			return nil, fmt.Errorf("el ítem «%s» es gravado pero tiene porcentaje IGV en 0; configúrelo en el producto", strings.TrimSpace(item.Description))
		}
		mtoValorVenta := round2(item.Subtotal)
		igv := round2(item.TaxAmount)
		cantidad := item.Quantity
		if cantidad <= 0 {
			return nil, fmt.Errorf("el ítem «%s» tiene cantidad inválida", strings.TrimSpace(item.Description))
		}
		mtoValorUnitario := round2(mtoValorVenta / cantidad)
		// MtoPrecioUnitario debe cumplir (MtoPrecioUnitario * Cantidad) = (MtoValorVenta + Igv) para que el facturador no discrepe en TaxInclusiveAmount
		mtoPrecioUnitario := round2((mtoValorVenta + igv) / cantidad)
		codProd := strings.TrimSpace(item.Code)
		if codProd == "" {
			return nil, fmt.Errorf("el ítem «%s» no tiene código de producto", strings.TrimSpace(item.Description))
		}
		desc := strings.TrimSpace(item.Description)
		if desc == "" {
			return nil, fmt.Errorf("ítem en posición %d sin descripción", i+1)
		}
		porcentajeIgv := round2(item.TaxRate)
		if aff != "10" {
			porcentajeIgv = round2(companyTaxRate)
		}
		details[i] = facturador.InvoiceDetail{
			Unidad:            normUnit(item.Unit),
			Cantidad:          cantidad,
			CodProducto:       codProd,
			Descripcion:       desc,
			MtoValorUnitario:  mtoValorUnitario,
			MtoValorVenta:     mtoValorVenta,
			TipAfeIgv:         aff,
			MtoBaseIgv:        mtoValorVenta, // Por línea: gravado = base; exonerado/inafecto = valor de la línea para que Lycet genere cac:TaxTotal con tributo (evita 3105)
			PorcentajeIgv:     porcentajeIgv,
			Igv:               igv,
			TotalImpuestos:    igv,
			MtoPrecioUnitario: mtoPrecioUnitario,
		}
	}
	// Totales por tipo de operación (SUNAT exige tag del total del tributo si hay líneas con ese tipo — error 2638)
	var mtoOperGravadas, mtoOperExoneradas, mtoOperInafectas, mtoIGV float64
	for _, d := range details {
		switch d.TipAfeIgv {
		case "10":
			mtoOperGravadas += d.MtoValorVenta
			mtoIGV += d.Igv
		case "20":
			mtoOperExoneradas += d.MtoValorVenta
		case "30":
			mtoOperInafectas += d.MtoValorVenta
		default:
			// Otros códigos (ej. 40 exportación) se pueden sumar a gravadas o manejar según catálogo 07
			mtoOperGravadas += d.MtoValorVenta
			mtoIGV += d.Igv
		}
	}
	mtoOperGravadas = round2(mtoOperGravadas)
	mtoOperExoneradas = round2(mtoOperExoneradas)
	mtoOperInafectas = round2(mtoOperInafectas)
	mtoIGV = round2(mtoIGV)
	valorVenta := round2(mtoOperGravadas + mtoOperExoneradas + mtoOperInafectas)
	mtoImpVenta := round2(valorVenta + mtoIGV)
	// Total de la venta en BD es la referencia para Lycet (leyenda 1000 usa mtoImpVenta del JSON).
	if sale.Total > 0 {
		mtoImpVenta = round2(sale.Total)
	}
	tipoMoneda := sale.Currency
	if tipoMoneda == "" {
		tipoMoneda = "PEN"
	}
	var legends []facturador.InvoiceLegend
	facturador.SetSUNATLegend1000(&legends, mtoImpVenta, tipoMoneda)
	nombreComercial := companyCfg.TradeName
	if nombreComercial == "" {
		nombreComercial = companyCfg.BusinessName
	}
	// Payload según PAYLOAD-FACTURA-BOLETA.md: todos los campos obligatorios enviados a Lycet
	serie := strings.TrimSpace(sale.Series)
	if serie == "" {
		serie = "B001"
		if tipoDoc == "01" {
			serie = "F001"
		}
	}
	correlativo := strconv.FormatUint(uint64(sale.Correlative), 10)
	// fecVencimiento solo para factura (01); mismo formato datetime ISO que fechaEmision (JMS/Greenter).
	var fecVencimiento string
	if tipoDoc == "01" {
		if sale.DueDate != nil {
			fecVencimiento = facturador.FormatFiscalDateTime(*sale.DueDate)
		} else {
			fecVencimiento = facturador.FormatFiscalDateTime(sale.IssueDate.AddDate(0, 0, 8))
		}
	}
	payload := &facturador.InvoicePayload{
		UBLVersion:        "2.1",
		TipoOperacion:     tipoOperacion,
		TipoDoc:           tipoDoc,
		Serie:             serie,
		Correlativo:       correlativo,
		FechaEmision:      fechaEmision,
		FecVencimiento:    fecVencimiento,
		FormaPago:         &facturador.InvoiceFormaPago{Tipo: "Contado"},
		Company:           facturador.InvoiceCompany{RUC: companyCfg.RUC, RazonSocial: companyCfg.BusinessName, NombreComercial: nombreComercial, Address: companyAddr},
		Client:            facturador.InvoiceClient{TipoDoc: clientTipoDoc, NumDoc: clientNumDoc, RznSocial: clientRzn, Address: clientAddr},
		TipoMoneda:        tipoMoneda,
		MtoOperGravadas:   mtoOperGravadas,
		MtoOperExoneradas: mtoOperExoneradas,
		MtoOperInafectas:  mtoOperInafectas,
		MtoIGV:            mtoIGV,
		TotalImpuestos:    mtoIGV,
		ValorVenta:        valorVenta,
		SubTotal:          mtoImpVenta,
		MtoImpVenta:       mtoImpVenta,
		Details:           details,
		Legends:           legends,
	}
	if fiscalEnrich, err := salecontext.LoadInvoiceEnrichment(s.db, saleID, sale.Total); err == nil && fiscalEnrich != nil {
		salecontext.ApplyToInvoicePayload(payload, fiscalEnrich)
	}
	if det, err := detraccionsvc.NewService(s.db).LoadBySaleID(saleID); err == nil && det != nil {
		detraccionsvc.ApplyToInvoicePayload(payload, det)
	}
	payloadBytes, _ := json.Marshal(payload)
	payloadJSON := string(payloadBytes)

	return s.enqueueFiscalMicroservice(saleID, companyCfg, payload, payloadJSON)
}

// ResendToSUNAT reenvía el comprobante regenerando el XML (y payload) desde la venta actual.
// Solo se permite cuando sunat_status es "error". Si fue rechazado por SUNAT no se debe reenviar el mismo; debe emitirse uno nuevo con correcciones.
func (s *BillingService) ResendToSUNAT(saleID uint) (*database.TenantInvoice, error) {
	var invoice database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&invoice).Error; err != nil || invoice.ID == 0 {
		return nil, errors.New("comprobante no encontrado")
	}
	if billingstate.HasAcceptanceEvidence(&invoice) || billingstate.HasFinalSunatOutcome(&invoice) {
		return nil, errors.New("el documento ya fue aceptado por SUNAT; no se puede reenviar")
	}
	if invoice.SunatStatus == "rejected" {
		return nil, errors.New("documento rechazado por SUNAT; debe emitir uno nuevo (corrija errores y use nuevo correlativo), no reenviar el mismo")
	}
	if strings.ToLower(strings.TrimSpace(invoice.SunatStatus)) != "error" {
		return &invoice, errors.New("solo se puede reenviar cuando el estado es error")
	}

	return s.SendToSUNAT(saleID)
}

// CreateCreditNoteAndVoidSale genera una nota de crédito para anular la venta y la envía a SUNAT; luego anula la venta original.
// La venta debe ser factura o boleta ya aceptada por SUNAT.
func (s *BillingService) CreateCreditNoteAndVoidSale(originalSaleID uint, reason string) (*database.TenantSale, *database.TenantInvoice, error) {
	if !s.facturadorConfigured() {
		return nil, nil, errors.New("la anulación con nota de crédito requiere facturador configurado")
	}
	var cfg database.TenantCompanyConfig
	if err := s.db.First(&cfg).Error; err != nil || !cfg.SunatEnabled {
		return nil, nil, errors.New("la conexión con SUNAT no está activada")
	}
	var orig database.TenantSale
	if err := s.db.First(&orig, originalSaleID).Error; err != nil {
		return nil, nil, errors.New("venta no encontrada")
	}
	if orig.Status == "cancelled" {
		return nil, nil, errors.New("la venta ya está anulada")
	}
	if orig.DocType != "FACTURA" && orig.DocType != "BOLETA" {
		return nil, nil, errors.New("solo se puede anular con nota de crédito una factura o boleta")
	}
	if orig.BillingStatus != "accepted" {
		return nil, nil, errors.New("el comprobante debe estar aceptado por SUNAT antes de anularlo con nota de crédito")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, nil, errors.New("indique el motivo de anulación")
	}
	if orig.ContactID == nil {
		return nil, nil, errors.New("para nota de crédito electrónica debe asignar un cliente con dirección y ubigeo en la venta original")
	}
	ncSeries, err := s.resolveCreditNoteSeries(orig.BranchID, &orig)
	if err != nil {
		return nil, nil, err
	}
	saleSvc := salesvc.NewSaleService(s.db)
	nextCorr, err := saleSvc.NextCorrelative(ncSeries.ID)
	if err != nil {
		return nil, nil, err
	}
	numberStr := fmt.Sprintf("%s-%08d", ncSeries.Series, nextCorr)
	now := time.Now()
	origIDRef := originalSaleID
	ncSale := database.TenantSale{
		BranchID:       orig.BranchID,
		ContactID:      orig.ContactID,
		UserID:         orig.UserID,
		CashSessionID:  nil,
		SeriesID:       ncSeries.ID,
		DocType:        "NOTA_CREDITO",
		Series:         ncSeries.Series,
		Correlative:    nextCorr,
		Number:         numberStr,
		IssueDate:      now,
		Subtotal:       orig.Subtotal,
		TaxAmount:      orig.TaxAmount,
		Total:          orig.Total,
		Currency:       orig.Currency,
		PaymentMethod:  orig.PaymentMethod,
		Notes:          reason,
		Status:         "paid",
		BillingStatus:  "pending",
		OriginalSaleID: &origIDRef,
	}
	if err := s.db.Create(&ncSale).Error; err != nil {
		return nil, nil, fmt.Errorf("crear venta nota de crédito: %w", err)
	}
	if err := s.reserveGenericDocument("credit_note", ncSale.ID, ncSale.Number); err != nil {
		return nil, nil, err
	}
	var origItems []database.TenantSaleItem
	s.db.Where("sale_id = ?", originalSaleID).Find(&origItems)
	for _, it := range origItems {
		ncItem := database.TenantSaleItem{
			SaleID:             ncSale.ID,
			ProductID:          it.ProductID,
			Code:               it.Code,
			Description:        it.Description,
			Unit:               it.Unit,
			Quantity:           it.Quantity,
			UnitPrice:          it.UnitPrice,
			Discount:           it.Discount,
			TaxRate:            it.TaxRate,
			IgvAffectationType: it.IgvAffectationType,
			Subtotal:           it.Subtotal,
			TaxAmount:          it.TaxAmount,
			Total:              it.Total,
		}
		s.db.Create(&ncItem)
	}
	notePayload, err := s.buildCreditNotePayload(ncSale.ID)
	if err != nil {
		return nil, nil, err
	}
	notePayloadJSON, _ := json.Marshal(notePayload)
	// DRAFT + payload: EnqueueSendToSUNAT pasa a PENDING_QUEUE y encola el job (no marcar PENDING_QUEUE aquí).
	inv := database.TenantInvoice{
		SaleID:          ncSale.ID,
		NotePayloadJSON: string(notePayloadJSON),
		PipelineStatus:  billingstate.DRAFT,
		SunatStatus:     "pending",
	}
	if err := s.db.Create(&inv).Error; err != nil {
		return nil, nil, fmt.Errorf("crear registro fiscal NC: %w", err)
	}

	tenantDB := s.lookupTenantDBName(s.centralTenantID)
	inv2, err := s.EnqueueSendToSUNAT(ncSale.ID, s.centralTenantID, s.tenantSlug, tenantDB, FiscalSourceQueue)
	if err != nil {
		return &ncSale, inv2, err
	}
	if inv2 != nil {
		inv = *inv2
	}
	return &ncSale, &inv, nil
}

// CreateDebitNoteForSale genera una nota de débito (08) vinculada a una venta aceptada y la encola a SUNAT.
func (s *BillingService) CreateDebitNoteForSale(originalSaleID uint) (*database.TenantSale, *database.TenantInvoice, error) {
	if !s.facturadorConfigured() {
		return nil, nil, errors.New("la nota de débito requiere facturador configurado")
	}
	var cfg database.TenantCompanyConfig
	if err := s.db.First(&cfg).Error; err != nil || !cfg.SunatEnabled {
		return nil, nil, errors.New("la conexión con SUNAT no está activada")
	}
	var orig database.TenantSale
	if err := s.db.First(&orig, originalSaleID).Error; err != nil {
		return nil, nil, errors.New("venta no encontrada")
	}
	if orig.DocType != "FACTURA" && orig.DocType != "BOLETA" {
		return nil, nil, errors.New("solo se puede emitir nota de débito sobre factura o boleta")
	}
	if orig.BillingStatus != "accepted" {
		return nil, nil, errors.New("el comprobante debe estar aceptado por SUNAT antes de emitir nota de débito")
	}
	if orig.ContactID == nil {
		return nil, nil, errors.New("debe asignar un cliente con dirección y ubigeo en la venta original")
	}
	var ndSeries database.TenantDocumentSeries
	if err := s.db.Where("branch_id = ? AND category = ? AND active = ?", orig.BranchID, "nota_debito", true).First(&ndSeries).Error; err != nil {
		return nil, nil, errors.New("no hay serie de nota de débito configurada para esta sucursal")
	}
	saleSvc := salesvc.NewSaleService(s.db)
	nextCorr, err := saleSvc.NextCorrelative(ndSeries.ID)
	if err != nil {
		return nil, nil, err
	}
	numberStr := fmt.Sprintf("%s-%08d", ndSeries.Series, nextCorr)
	now := time.Now()
	origIDRef := originalSaleID
	ndSale := database.TenantSale{
		BranchID:       orig.BranchID,
		ContactID:      orig.ContactID,
		UserID:         orig.UserID,
		SeriesID:       ndSeries.ID,
		DocType:        "NOTA_DEBITO",
		Series:         ndSeries.Series,
		Correlative:    nextCorr,
		Number:         numberStr,
		IssueDate:      now,
		Subtotal:       orig.Subtotal,
		TaxAmount:      orig.TaxAmount,
		Total:          orig.Total,
		Currency:       orig.Currency,
		PaymentMethod:  orig.PaymentMethod,
		Notes:          "Aumento en el valor",
		Status:         "paid",
		BillingStatus:  "pending",
		OriginalSaleID: &origIDRef,
	}
	if err := s.db.Create(&ndSale).Error; err != nil {
		return nil, nil, fmt.Errorf("crear venta nota de débito: %w", err)
	}
	if err := s.reserveGenericDocument("debit_note", ndSale.ID, ndSale.Number); err != nil {
		return nil, nil, err
	}
	var origItems []database.TenantSaleItem
	s.db.Where("sale_id = ?", originalSaleID).Find(&origItems)
	for _, it := range origItems {
		ndItem := database.TenantSaleItem{
			SaleID:             ndSale.ID,
			ProductID:          it.ProductID,
			Code:               it.Code,
			Description:        it.Description,
			Unit:               it.Unit,
			Quantity:           it.Quantity,
			UnitPrice:          it.UnitPrice,
			Discount:           it.Discount,
			TaxRate:            it.TaxRate,
			IgvAffectationType: it.IgvAffectationType,
			Subtotal:           it.Subtotal,
			TaxAmount:          it.TaxAmount,
			Total:              it.Total,
		}
		s.db.Create(&ndItem)
	}
	notePayload, err := s.buildNotePayload(ndSale.ID)
	if err != nil {
		return nil, nil, err
	}
	notePayloadJSON, _ := json.Marshal(notePayload)
	inv := database.TenantInvoice{
		SaleID:          ndSale.ID,
		NotePayloadJSON: string(notePayloadJSON),
		PipelineStatus:  billingstate.DRAFT,
		SunatStatus:     "pending",
	}
	if err := s.db.Create(&inv).Error; err != nil {
		return nil, nil, fmt.Errorf("crear registro fiscal ND: %w", err)
	}
	tenantDB := s.lookupTenantDBName(s.centralTenantID)
	inv2, err := s.EnqueueSendToSUNAT(ndSale.ID, s.centralTenantID, s.tenantSlug, tenantDB, FiscalSourceQueue)
	if err != nil {
		return &ndSale, inv2, err
	}
	if inv2 != nil {
		inv = *inv2
	}
	return &ndSale, &inv, nil
}

func getSeriesSunatCode(db *gorm.DB, seriesID uint) string {
	var ser database.TenantDocumentSeries
	if err := db.Select("sunat_code").First(&ser, seriesID).Error; err != nil {
		return ""
	}
	return ser.SunatCode
}

// resolveCreditNoteSeries elige la serie NC (SUNAT 07) según el comprobante a anular: FC## factura, BC## boleta.
func (s *BillingService) resolveCreditNoteSeries(branchID uint, orig *database.TenantSale) (database.TenantDocumentSeries, error) {
	if orig == nil {
		return database.TenantDocumentSeries{}, errors.New("venta original no indicada")
	}
	prefix := docseries.CreditNoteSeriesPrefixForAffected(orig.DocType, getSeriesSunatCode(s.db, orig.SeriesID))
	var rows []database.TenantDocumentSeries
	err := s.db.Where("branch_id = ? AND category = ? AND active = ? AND TRIM(sunat_code) = ?",
		branchID, "nota_credito", true, "07").Order("id ASC").Find(&rows).Error
	if err != nil {
		return database.TenantDocumentSeries{}, err
	}
	for _, row := range rows {
		if docseries.SeriesMatchesCreditNotePrefix(row.Series, prefix) {
			return row, nil
		}
	}
	docLabel := docseries.AffectedDocLabel(orig.DocType, getSeriesSunatCode(s.db, orig.SeriesID))
	if len(rows) == 0 {
		return database.TenantDocumentSeries{}, fmt.Errorf(
			"no hay serie de nota de crédito en esta sucursal — cree una serie %s## activa (categoría Nota de crédito, SUNAT 07) para anular %ss",
			prefix, docLabel,
		)
	}
	return database.TenantDocumentSeries{}, fmt.Errorf(
		"ninguna serie de nota de crédito coincide con %s: configure serie %s## (ej. %s01) para anular %ss; las series FC## son solo para facturas y BC## solo para boletas",
		docLabel, prefix, prefix, docLabel,
	)
}

func normUnit(u string) string {
	return sunat.NormalizeUnit(u, "")
}

// sunatNotesToJSON serializa las notas del CDR para guardar en BD (panel tenant).
func sunatNotesToJSON(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	b, _ := json.Marshal(notes)
	return string(b)
}

func facturadorResponseToJSON(resp *facturador.SunatResponse) string {
	if resp == nil {
		return ""
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// saveInvoiceFile guarda un archivo en basePath/tenants/ruc/provider/{xml|cdr|signed|pdf}/... y retorna la ruta relativa.
func saveInvoiceFile(basePath, ruc, provider, tipoDoc, serie, correlativo, ext string, content []byte) (relativePath string, err error) {
	safeRUC := tenantstorage.SanitizeRUC(ruc)
	safeProvider := tenantstorage.SanitizePathSegment(provider)
	kind := invoiceFileFolderFromExt(ext)
	dir := filepath.Join(basePath, "tenants", safeRUC, safeProvider, kind)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s-%s-%s.%s", safeRUC, tipoDoc, serie, correlativo, ext)
	fullPath := filepath.Join(dir, name)
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join("tenants", safeRUC, safeProvider, kind, name)), nil
}

func invoiceFileFolderFromExt(ext string) string {
	e := strings.ToLower(strings.TrimSpace(ext))
	switch {
	case strings.HasPrefix(e, "cdr"):
		return "cdr"
	case strings.Contains(e, "signed"):
		return "signed"
	case strings.Contains(e, "pdf"):
		return "pdf"
	case strings.HasSuffix(e, "xml"):
		return "xml"
	default:
		return "misc"
	}
}

func (s *BillingService) GetInvoice(saleID uint) (*database.TenantInvoice, error) {
	var invoice database.TenantInvoice
	err := s.db.Where("sale_id = ?", saleID).First(&invoice).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &invoice, err
}

// GetInvoiceDocumentPath devuelve la ruta absoluta del archivo XML, CDR o PDF del comprobante para permitir su descarga.
func (s *BillingService) GetInvoiceDocumentPath(saleID uint, kind string) (string, error) {
	invoice, err := s.GetInvoice(saleID)
	if err != nil || invoice == nil {
		return "", err
	}
	basePath := config.AppConfig.InvoiceStoragePath
	if basePath == "" {
		basePath = "./storage/invoices"
	}
	var rel string
	switch kind {
	case "xml":
		rel = invoice.XMLURL
	case "cdr":
		rel = invoice.CDRURL
	case "pdf":
		rel = invoice.PDFURL
	default:
		return "", fmt.Errorf("tipo de documento no válido: %s", kind)
	}
	if rel == "" || rel == "(CDR recibido)" {
		return "", nil
	}

	fullPath := filepath.Join(basePath, filepath.FromSlash(rel))
	if _, statErr := os.Stat(fullPath); statErr == nil {
		return fullPath, nil
	}
	if basePath == "./storage/invoices" {
		legacyPath := filepath.Join("./storage", filepath.FromSlash(rel))
		if _, statErr := os.Stat(legacyPath); statErr == nil {
			return legacyPath, nil
		}
	}
	return fullPath, nil
}

// GetInvoiceDocumentFilename devuelve el nombre de archivo según formato SUNAT (ej. 03-B001-26.xml, 03-B001-26.cdr.zip, 03-B001-26.pdf).
// Las rutas en BD son del tipo ruc/tipoDoc-serie-correlativo.ext; se devuelve solo el nombre del archivo para Content-Disposition.
func (s *BillingService) GetInvoiceDocumentFilename(saleID uint, kind string) (string, error) {
	invoice, err := s.GetInvoice(saleID)
	if err != nil || invoice == nil {
		return "", err
	}
	switch kind {
	case "xml":
		if invoice.XMLURL != "" && invoice.XMLURL != "(CDR recibido)" {
			return filepath.Base(filepath.FromSlash(invoice.XMLURL)), nil
		}
	case "cdr":
		if invoice.CDRURL != "" && invoice.CDRURL != "(CDR recibido)" {
			return filepath.Base(filepath.FromSlash(invoice.CDRURL)), nil
		}
	case "pdf", "xml-generated":
		if invoice.PayloadJSON != "" {
			var payload facturador.InvoicePayload
			if err := json.Unmarshal([]byte(invoice.PayloadJSON), &payload); err == nil {
				base := fmt.Sprintf("%s-%s-%s", payload.TipoDoc, payload.Serie, payload.Correlativo)
				if kind == "pdf" {
					return base + ".pdf", nil
				}
				return base + "-generado.xml", nil
			}
		}
		if invoice.NotePayloadJSON != "" {
			var note facturador.NotePayload
			if err := json.Unmarshal([]byte(invoice.NotePayloadJSON), &note); err == nil {
				base := fmt.Sprintf("%s-%s-%s", note.TipoDoc, note.Serie, note.Correlativo)
				if kind == "pdf" {
					return base + ".pdf", nil
				}
				return base + "-generado.xml", nil
			}
		}
		if payload, err := s.getDespatchPayloadForSale(saleID); err == nil && payload != nil {
			base := fmt.Sprintf("%s-%s-%s", payload.TipoDoc, payload.Serie, payload.Correlativo)
			if kind == "pdf" {
				return base + ".pdf", nil
			}
			return base + "-generado.xml", nil
		}
	}
	// Fallback
	switch kind {
	case "pdf":
		return "comprobante.pdf", nil
	case "xml":
		return "comprobante-enviado.xml", nil
	case "xml-generated":
		return "comprobante-generado.xml", nil
	case "cdr":
		return "cdr.zip", nil
	default:
		return "comprobante", nil
	}
}

// GetInvoicePDFContent devuelve el PDF del comprobante vía API del facturador (POST /invoice/pdf o /note/pdf).
func (s *BillingService) GetInvoicePDFContent(saleID uint) ([]byte, error) {
	invoice, err := s.GetInvoice(saleID)
	if err != nil || invoice == nil {
		return nil, err
	}
	var sale database.TenantSale
	if s.db.Select("doc_type").First(&sale, saleID).Error != nil {
		return nil, errors.New("venta no encontrada")
	}
	if isNoteSaleDocType(sale.DocType) {
		pdfBytes, err := s.getNoteDocumentPDF(invoice)
		if err != nil {
			return nil, err
		}
		if len(pdfBytes) > 0 {
			return pdfBytes, nil
		}
	}
	if isGuiaSaleDocType(sale.DocType) && s.facturadorConfigured() {
		payload, err := s.getDespatchPayloadForSale(saleID)
		if err != nil {
			return nil, err
		}
		pdfBytes, err := facturador.Shared().GetDespatchPDF(payload)
		if err != nil {
			return nil, err
		}
		if len(pdfBytes) > 0 {
			return pdfBytes, nil
		}
	}
	// Obtener PDF del endpoint del facturador (no generar en este backend).
	if s.facturadorConfigured() && invoice.PayloadJSON != "" {
		var payload facturador.InvoicePayload
		if err := json.Unmarshal([]byte(invoice.PayloadJSON), &payload); err != nil {
			return nil, fmt.Errorf("payload inválido: %w", err)
		}
		var saleTotal float64
		if s.db.Model(&database.TenantSale{}).Where("id = ?", saleID).Pluck("total", &saleTotal).Error != nil {
			saleTotal = payload.MtoImpVenta
		}
		var pdfOpts *facturador.InvoicePDFOptions
		if enrich, err := salecontext.LoadInvoiceEnrichment(s.db, saleID, saleTotal); err == nil && enrich != nil {
			salecontext.ApplyToInvoicePayload(&payload, enrich)
		}
		client := facturador.Shared()
		pdfBytes, err := client.GetInvoicePDF(&payload, pdfOpts)
		if err != nil {
			return nil, err
		}
		if len(pdfBytes) > 0 {
			return pdfBytes, nil
		}
	}
	// Sin payload: intentar desde disco si existe.
	basePath := config.AppConfig.InvoiceStoragePath
	if basePath == "" {
		basePath = "./storage/invoices"
	}
	if invoice.PDFURL != "" && invoice.PDFURL != "(CDR recibido)" {
		fullPath := filepath.Join(basePath, filepath.FromSlash(invoice.PDFURL))
		if data, err := os.ReadFile(fullPath); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	return nil, nil
}

// GetInvoiceXMLGeneratedContent devuelve el XML firmado generado sin envío a SUNAT (POST /invoice/xml o /note/xml de Lycet).
// No se almacena; se obtiene bajo demanda desde el endpoint de Lycet.
func (s *BillingService) GetInvoiceXMLGeneratedContent(saleID uint) ([]byte, error) {
	invoice, err := s.GetInvoice(saleID)
	if err != nil || invoice == nil {
		return nil, nil
	}
	var sale database.TenantSale
	if s.db.Select("doc_type").First(&sale, saleID).Error == nil && isNoteSaleDocType(sale.DocType) {
		xmlBytes, err := s.getNoteDocumentXMLGenerated(invoice)
		if err != nil {
			return nil, err
		}
		if len(xmlBytes) > 0 {
			return xmlBytes, nil
		}
	}
	if s.db.Select("doc_type").First(&sale, saleID).Error == nil && isGuiaSaleDocType(sale.DocType) && s.facturadorConfigured() {
		payload, err := s.getDespatchPayloadForSale(saleID)
		if err != nil {
			return nil, err
		}
		return facturador.Shared().GetDespatchXML(payload)
	}
	if !s.facturadorConfigured() || invoice.PayloadJSON == "" {
		return nil, nil
	}
	var payload facturador.InvoicePayload
	if err := json.Unmarshal([]byte(invoice.PayloadJSON), &payload); err != nil {
		return nil, fmt.Errorf("payload inválido: %w", err)
	}
	client := facturador.Shared()
	return client.GetInvoiceXML(&payload)
}

type InvoiceListParams struct {
	Status   string
	DateFrom *time.Time
	DateTo   *time.Time
}

func (s *BillingService) ListInvoices(params InvoiceListParams) ([]database.TenantInvoice, error) {
	var invoices []database.TenantInvoice
	q := s.db.Model(&database.TenantInvoice{})
	if params.Status != "" {
		q = q.Where("sunat_status = ?", params.Status)
	}
	if params.DateFrom != nil {
		q = q.Where("sent_at >= ?", params.DateFrom)
	}
	if params.DateTo != nil {
		q = q.Where("sent_at <= ?", params.DateTo)
	}
	err := q.Order("created_at DESC").Find(&invoices).Error
	return invoices, err
}

// --- Resumen diario y Comunicación de baja ---

func (s *BillingService) ListSummaries() ([]database.TenantSunatSummary, error) {
	var list []database.TenantSunatSummary
	err := s.db.Order("fec_resumen DESC, created_at DESC").Find(&list).Error
	return list, err
}

// resolveCompanyTaxRate alinea la emisión fiscal con tax.LoadFromDB (misma fuente que las ventas).
func (s *BillingService) resolveCompanyTaxRate() (float64, error) {
	rate := tax.LoadFromDB(s.db).TaxRate
	if rate <= 0 {
		return 0, fmt.Errorf("configure el porcentaje de IGV en Configuración de la empresa (SUNAT)")
	}
	return rate, nil
}

// getCompanyConfigAndAddress obtiene la configuración de la empresa y la dirección para payloads SUNAT (resumen, voided).
// No usa fallbacks "-": SUNAT exige dirección completa y nombres reales de departamento/provincia/distrito.
func (s *BillingService) getCompanyConfigAndAddress() (*database.TenantCompanyConfig, facturador.InvoiceAddress, error) {
	var cfg database.TenantCompanyConfig
	// tax_rate: obligatorio al armar NC/ND (buildNotePayload); facturas ya cargan cfg completa vía First().
	if err := s.db.Select("id", "ruc", "business_name", "trade_name", "address", "ubigeo", "tax_rate").First(&cfg).Error; err != nil {
		return nil, facturador.InvoiceAddress{}, err
	}
	ubigueo := strings.TrimSpace(cfg.Ubigeo)
	if ubigueo == "" {
		return nil, facturador.InvoiceAddress{}, fmt.Errorf("configure el ubigeo del domicilio fiscal en Configuración → Empresa / SUNAT")
	}
	dep, prov, dist, err := s.resolveUbigeoToAddress(ubigueo)
	if err != nil {
		return nil, facturador.InvoiceAddress{}, err
	}
	direccion := strings.TrimSpace(cfg.Address)
	if direccion == "" {
		return nil, facturador.InvoiceAddress{}, fmt.Errorf("configure la dirección completa del domicilio fiscal en Configuración → Empresa")
	}
	addr := facturador.InvoiceAddress{
		Ubigueo:      ubigueo,
		CodigoPais:   "PE",
		Departamento: dep,
		Provincia:    prov,
		Distrito:     dist,
		Urbanizacion: "",
		Direccion:    direccion,
	}
	return &cfg, addr, nil
}

// CreateSummary genera y envía el resumen diario a SUNAT para la fecha indicada. Solo incluye ventas con billing_status = accepted.
// Devuelve el registro guardado con ticket; el estado se consulta con GetSummaryStatus.
func (s *BillingService) CreateSummary(fecResumen time.Time) (*database.TenantSunatSummary, error) {
	if !s.facturadorConfigured() {
		return nil, errors.New("resumen diario requiere facturador configurado")
	}
	companyCfg, companyAddr, err := s.getCompanyConfigAndAddress()
	if err != nil {
		return nil, fmt.Errorf("configuración de empresa: %w", err)
	}
	nombreComercial := companyCfg.TradeName
	if nombreComercial == "" {
		nombreComercial = companyCfg.BusinessName
	}

	// Ventas del día con estado aceptado por SUNAT (facturas/boletas enviadas y aceptadas)
	dayStart := time.Date(fecResumen.Year(), fecResumen.Month(), fecResumen.Day(), 0, 0, 0, 0, fecResumen.Location())
	dayEnd := dayStart.Add(24 * time.Hour)
	var sales []database.TenantSale
	err = s.db.Where("issue_date >= ? AND issue_date < ? AND billing_status = ?",
		dayStart, dayEnd, "accepted").Find(&sales).Error
	if err != nil {
		return nil, err
	}

	// Correlativo del resumen: secuencia por fecha (RC-YYYYMMDD-NNN o simple número)
	var count int64
	s.db.Model(&database.TenantSunatSummary{}).Where("fec_resumen = ?", dayStart).Count(&count)
	correlativo := strconv.FormatInt(count+1, 10)

	details := make([]facturador.SummaryDetail, 0, len(sales))
	for _, sale := range sales {
		serieDoc := getSeriesSunatCode(s.db, sale.SeriesID)
		if serieDoc == "00" {
			continue
		}
		serieNro := sale.Series + "-" + sale.Number
		clienteTipo := "1"
		clienteNro := "00000000"
		if sale.ContactID != nil {
			var c database.TenantContact
			if s.db.Select("doc_type", "doc_number").First(&c, *sale.ContactID).Error == nil {
				if c.DocType == "RUC" || c.DocType == "6" {
					clienteTipo = "6"
				}
				clienteNro = c.DocNumber
			}
		}
		details = append(details, facturador.SummaryDetail{
			TipoDoc:         serieDoc,
			SerieNro:        serieNro,
			ClienteTipo:     clienteTipo,
			ClienteNro:      clienteNro,
			Total:           sale.Total,
			MtoOperGravadas: sale.Subtotal,
			MtoIGV:          sale.TaxAmount,
		})
	}

	now := time.Now()
	payload := &facturador.SummaryPayload{
		Company:       facturador.InvoiceCompany{RUC: companyCfg.RUC, RazonSocial: companyCfg.BusinessName, NombreComercial: nombreComercial, Address: companyAddr},
		Correlativo:   correlativo,
		FecGeneracion: now.Format(time.RFC3339),
		FecResumen:    dayStart.Format("2006-01-02T15:04:05-07:00"),
		Moneda:        "PEN",
		Details:       details,
	}
	payloadJSON, _ := json.Marshal(payload)

	summaryDocID := uint(dayStart.Unix() % 0x7fffffff)
	if err := s.reserveGenericDocument("summary", summaryDocID, correlativo); err != nil {
		return nil, err
	}
	client := facturador.Shared()
	resp, err := client.SendSummary(payload)
	if err != nil {
		return nil, err
	}

	ticket := resp.Ticket()
	rec := &database.TenantSunatSummary{
		FecResumen:   dayStart,
		Correlativo:  correlativo,
		Ticket:       ticket,
		Status:       "pending",
		PayloadJSON:  string(payloadJSON),
		DetailsCount: len(details),
	}
	if err := s.db.Create(rec).Error; err != nil {
		return nil, err
	}
	return rec, nil
}

// GetSummaryStatus consulta en SUNAT el estado del ticket del resumen; si hay CDR, lo guarda en disco y actualiza el registro.
func (s *BillingService) GetSummaryStatus(id uint) (*database.TenantSunatSummary, error) {
	var rec database.TenantSunatSummary
	if err := s.db.First(&rec, id).Error; err != nil {
		return nil, err
	}
	if rec.Ticket == "" {
		return &rec, nil
	}
	var cfg database.TenantCompanyConfig
	if s.db.Select("ruc").First(&cfg).Error != nil {
		return &rec, nil
	}
	client := facturador.Shared()
	result, err := client.GetSummaryStatus(rec.Ticket, cfg.RUC)
	if err != nil {
		return &rec, err
	}
	if result.Success && result.CDRZip != "" {
		basePath := config.AppConfig.InvoiceStoragePath
		if basePath == "" {
			basePath = "./storage/invoices"
		}
		cdrDec, decErr := base64.StdEncoding.DecodeString(result.CDRZip)
		if decErr == nil {
			ruc := cfg.RUC
			if ruc == "" {
				ruc = "default"
			}
			cdrPath, _ := saveInvoiceFile(basePath, ruc, "lycet", "RC", "RC", rec.Correlativo+"-"+rec.FecResumen.Format("20060102"), "cdr.zip", cdrDec)
			rec.CDRURL = cdrPath
		}
		if result.CDRResponse != nil {
			rec.SunatCode = result.CDRResponse.Code
			rec.SunatMessage = result.CDRResponse.Description
			if result.CDRResponse.Accepted {
				rec.Status = "accepted"
			} else {
				rec.Status = "rejected"
			}
		} else {
			rec.Status = "accepted"
		}
	} else {
		if result.Error != nil {
			rec.SunatMessage = result.Error.Message
			rec.SunatCode = result.Error.Code
			rec.Status = "error"
		}
	}
	s.db.Save(&rec)
	return &rec, nil
}

// CreateVoidedInput es un comprobante a dar de baja para una comunicación de baja.
type CreateVoidedInput struct {
	TipoDoc       string `json:"tipo_doc"` // 01, 03, 07, 08
	Serie         string `json:"serie"`
	Correlativo   string `json:"correlativo"`
	DesMotivoBaja string `json:"des_motivo_baja"`
}

// ListVoided lista las comunicaciones de baja enviadas.
func (s *BillingService) ListVoided() ([]database.TenantSunatVoided, error) {
	var list []database.TenantSunatVoided
	err := s.db.Order("fec_comunicacion DESC, created_at DESC").Find(&list).Error
	return list, err
}

// CreateVoided envía una comunicación de baja a SUNAT. Si la respuesta trae ticket, guarda pendiente; si trae CDR directo, guarda estado y CDR.
func (s *BillingService) CreateVoided(details []CreateVoidedInput) (*database.TenantSunatVoided, error) {
	if !s.facturadorConfigured() {
		return nil, errors.New("comunicación de baja requiere facturador configurado")
	}
	if len(details) == 0 {
		return nil, errors.New("se requiere al menos un comprobante para dar de baja")
	}
	companyCfg, companyAddr, err := s.getCompanyConfigAndAddress()
	if err != nil {
		return nil, fmt.Errorf("configuración de empresa: %w", err)
	}
	nombreComercial := companyCfg.TradeName
	if nombreComercial == "" {
		nombreComercial = companyCfg.BusinessName
	}

	now := time.Now()
	var count int64
	s.db.Model(&database.TenantSunatVoided{}).Where("DATE(fec_comunicacion) = ?", now.Format("2006-01-02")).Count(&count)
	correlativo := strconv.FormatInt(count+1, 10)

	voidedDetails := make([]facturador.VoidedDetail, len(details))
	for i, d := range details {
		voidedDetails[i] = facturador.VoidedDetail{
			TipoDoc:       d.TipoDoc,
			Serie:         d.Serie,
			Correlativo:   d.Correlativo,
			DesMotivoBaja: d.DesMotivoBaja,
		}
	}
	payload := &facturador.VoidedPayload{
		Company:         facturador.InvoiceCompany{RUC: companyCfg.RUC, RazonSocial: companyCfg.BusinessName, NombreComercial: nombreComercial, Address: companyAddr},
		Correlativo:     correlativo,
		FecGeneracion:   now.Format(time.RFC3339),
		FecComunicacion: now.Format(time.RFC3339),
		Details:         voidedDetails,
	}
	payloadJSON, _ := json.Marshal(payload)

	voidID := uint(now.Unix() % 0x7fffffff)
	if err := s.reserveGenericDocument("voided", voidID, correlativo); err != nil {
		return nil, err
	}
	client := facturador.Shared()
	resp, err := client.SendVoided(payload)
	if err != nil {
		return nil, err
	}

	rec := &database.TenantSunatVoided{
		FecComunicacion: now,
		Correlativo:     correlativo,
		Ticket:          resp.Ticket(),
		Status:          "pending",
		PayloadJSON:     string(payloadJSON),
		DetailsCount:    len(details),
	}

	if resp.CDRZipBase64() != "" {
		rec.Status = "accepted"
		if resp.SunatResponse != nil && resp.SunatResponse.CDRResponse != nil {
			rec.SunatCode = resp.SunatResponse.CDRResponse.Code
			rec.SunatMessage = resp.SunatResponse.CDRResponse.Description
			if !resp.SunatResponse.CDRResponse.Accepted {
				rec.Status = "rejected"
			}
		}
		basePath := config.AppConfig.InvoiceStoragePath
		if basePath == "" {
			basePath = "./storage/invoices"
		}
		ruc := companyCfg.RUC
		if ruc == "" {
			ruc = "default"
		}
		cdrDec, decErr := base64.StdEncoding.DecodeString(resp.CDRZipBase64())
		if decErr == nil {
			cdrPath, _ := saveInvoiceFile(basePath, ruc, "lycet", "RA", "RA", correlativo+"-"+now.Format("20060102"), "cdr.zip", cdrDec)
			rec.CDRURL = cdrPath
		}
	} else if resp.Message() != "" {
		rec.SunatMessage = resp.Message()
		rec.SunatCode = resp.CDRCode()
		if resp.Success() {
			rec.Status = "accepted"
		} else {
			rec.Status = "rejected"
		}
	}

	if err := s.db.Create(rec).Error; err != nil {
		return nil, err
	}
	return rec, nil
}

// GetVoidedStatus consulta el estado del ticket de la comunicación de baja; si hay CDR, lo guarda y actualiza el registro.
func (s *BillingService) GetVoidedStatus(id uint) (*database.TenantSunatVoided, error) {
	var rec database.TenantSunatVoided
	if err := s.db.First(&rec, id).Error; err != nil {
		return nil, err
	}
	if rec.Ticket == "" {
		return &rec, nil
	}
	var cfg database.TenantCompanyConfig
	if s.db.Select("ruc").First(&cfg).Error != nil {
		return &rec, nil
	}
	client := facturador.Shared()
	result, err := client.GetVoidedStatus(rec.Ticket, cfg.RUC)
	if err != nil {
		return &rec, err
	}
	if result.Success && result.CDRZip != "" {
		basePath := config.AppConfig.InvoiceStoragePath
		if basePath == "" {
			basePath = "./storage/invoices"
		}
		cdrDec, decErr := base64.StdEncoding.DecodeString(result.CDRZip)
		if decErr == nil {
			ruc := cfg.RUC
			if ruc == "" {
				ruc = "default"
			}
			cdrPath, _ := saveInvoiceFile(basePath, ruc, "lycet", "RA", "RA", rec.Correlativo+"-"+rec.FecComunicacion.Format("20060102"), "cdr.zip", cdrDec)
			rec.CDRURL = cdrPath
		}
		if result.CDRResponse != nil {
			rec.SunatCode = result.CDRResponse.Code
			rec.SunatMessage = result.CDRResponse.Description
			if result.CDRResponse.Accepted {
				rec.Status = "accepted"
			} else {
				rec.Status = "rejected"
			}
		} else {
			rec.Status = "accepted"
		}
	} else {
		if result.Error != nil {
			rec.SunatMessage = result.Error.Message
			rec.SunatCode = result.Error.Code
			rec.Status = "error"
		}
	}
	s.db.Save(&rec)
	return &rec, nil
}

// ConsultInvoiceStatus consulta en SUNAT el estado/CDR de un comprobante (GET /invoice/status). Según CONSULTA-COMPROBANTE-CDR.md.
func (s *BillingService) ConsultInvoiceStatus(tipo, serie, numero string) (*facturador.StatusResult, error) {
	if !s.facturadorConfigured() {
		return nil, errors.New("consulta de comprobante requiere facturador configurado")
	}
	var cfg database.TenantCompanyConfig
	if s.db.Select("ruc").First(&cfg).Error != nil {
		return nil, errors.New("no hay configuración de empresa")
	}
	client := facturador.Shared()
	return client.GetInvoiceStatus(tipo, serie, numero, cfg.RUC)
}

// --- Guías de remisión (Despatch) ---

// CreateDespatchInput entrada para crear y enviar una guía de remisión.
type CreateDespatchInput struct {
	BranchID     uint                      `json:"branch_id"`
	SeriesID     uint                      `json:"series_id"`
	SourceSaleID *uint                     `json:"source_sale_id,omitempty"`
	Destinatario DespatchDestinatarioInput `json:"destinatario"`
	Remitente    DespatchDestinatarioInput `json:"remitente,omitempty"`
	Envio        DespatchEnvioInput        `json:"envio"`
	Details      []DespatchDetailInput     `json:"details"`
}

type DespatchDestinatarioInput struct {
	TipoDoc   string `json:"tipo_doc"`
	NumDoc    string `json:"num_doc"`
	RznSocial string `json:"rzn_social"`
	Address   string `json:"address"`
	Ubigeo    string `json:"ubigeo"`
}

type DespatchEnvioInput struct {
	CodTraslado              string  `json:"cod_traslado"`
	DesTraslado              string  `json:"des_traslado"`
	ModTraslado              string  `json:"mod_traslado"`
	FecTraslado              string  `json:"fec_traslado"`
	FecEntregaTransportista  string  `json:"fec_entrega_transportista,omitempty"`
	PartidaUbigueo           string  `json:"partida_ubigueo"`
	PartidaDireccion         string  `json:"partida_direccion"`
	LlegadaUbigueo           string  `json:"llegada_ubigueo"`
	LlegadaDireccion         string  `json:"llegada_direccion"`
	PesoTotal                float64 `json:"peso_total"`
	UndPesoTotal             string  `json:"und_peso_total"`
	NumBultos                int     `json:"num_bultos"`
	TransportistaRUC         string  `json:"transportista_ruc,omitempty"`
	TransportistaRazon       string  `json:"transportista_razon,omitempty"`
	TransportistaPlaca       string  `json:"transportista_placa,omitempty"`
	TransportistaMTC         string  `json:"transportista_mtc,omitempty"`
	VehiculoHabCert          string  `json:"vehiculo_hab_cert,omitempty"`
	VehiculoCodEmisor        string  `json:"vehiculo_cod_emisor,omitempty"`
	ChoferTipoDoc            string  `json:"chofer_tipo_doc,omitempty"`
	ChoferDoc                string  `json:"chofer_doc,omitempty"`
	ChoferLicencia           string  `json:"chofer_licencia,omitempty"`
	ChoferNombres            string  `json:"chofer_nombres,omitempty"`
	ChoferApellidos          string  `json:"chofer_apellidos,omitempty"`
}

type DespatchDetailInput struct {
	Codigo      string  `json:"codigo"`
	Descripcion string  `json:"descripcion"`
	Unidad      string  `json:"unidad"`
	Cantidad    float64 `json:"cantidad"`
}

// DespatchListItem fila de guía con estado fiscal de la venta vinculada (para listado UI).
type DespatchListItem struct {
	database.TenantDespatch
	BillingStatus string `json:"billing_status,omitempty"`
	DocType       string `json:"doc_type,omitempty"`
}

func (s *BillingService) ListDespatches() ([]DespatchListItem, error) {
	var list []database.TenantDespatch
	if err := s.db.Order("issue_date DESC, created_at DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	out := make([]DespatchListItem, len(list))
	saleIDs := make([]uint, 0, len(list))
	for _, d := range list {
		if d.SaleID != nil && *d.SaleID > 0 {
			saleIDs = append(saleIDs, *d.SaleID)
		}
	}
	saleByID := map[uint]database.TenantSale{}
	if len(saleIDs) > 0 {
		var sales []database.TenantSale
		_ = s.db.Select("id, billing_status, doc_type").Where("id IN ?", saleIDs).Find(&sales).Error
		for _, sale := range sales {
			saleByID[sale.ID] = sale
		}
	}
	for i, d := range list {
		item := DespatchListItem{TenantDespatch: d}
		if d.SaleID != nil {
			if sale, ok := saleByID[*d.SaleID]; ok {
				item.BillingStatus = sale.BillingStatus
				item.DocType = sale.DocType
			}
		}
		out[i] = item
	}
	return out, nil
}

func (s *BillingService) CreateAndSendDespatch(input CreateDespatchInput) (*database.TenantDespatch, error) {
	if !s.facturadorConfigured() {
		return nil, errors.New("guías de remisión requieren facturador configurado")
	}
	if input.SourceSaleID != nil && *input.SourceSaleID > 0 {
		if err := s.applyDespatchPrefillFromSale(&input, *input.SourceSaleID); err != nil {
			return nil, err
		}
	}
	var cfg database.TenantCompanyConfig
	if err := s.db.First(&cfg).Error; err != nil || !cfg.SunatEnabled {
		return nil, errors.New("la conexión con SUNAT no está activada")
	}
	companyCfg, companyAddr, err := s.getCompanyConfigAndAddress()
	if err != nil {
		return nil, err
	}
	nombreComercial := companyCfg.TradeName
	if nombreComercial == "" {
		nombreComercial = companyCfg.BusinessName
	}
	var series database.TenantDocumentSeries
	series, err = docseries.ValidateForBranch(s.db, input.SeriesID, input.BranchID)
	if err != nil {
		if errors.Is(err, docseries.ErrSeriesNotFound) {
			return nil, errors.New("serie no encontrada")
		}
		if errors.Is(err, docseries.ErrSeriesInactive) {
			return nil, errors.New("la serie seleccionada está inactiva; actívela en Empresa → Series")
		}
		if errors.Is(err, docseries.ErrSeriesWrongBranch) {
			return nil, errors.New("la serie no pertenece a la sucursal seleccionada")
		}
		return nil, fmt.Errorf("serie no válida: %w", err)
	}
	sunatCode := strings.TrimSpace(series.SunatCode)
	if sunatCode != "09" && sunatCode != "31" {
		return nil, errors.New("Debe configurar una serie para Guía de Remisión Remitente (09) o Guía de Remisión Transportista (31) en Empresa → Series")
	}
	if err := validateDespatchInput(input, sunatCode); err != nil {
		return nil, err
	}
	if err := validateDespatchBusinessRules(input, sunatCode, companyCfg.RUC); err != nil {
		return nil, err
	}
	docType := "GUIA_REMISION"
	reserveKind := "guide_remitter"
	dispatchKind := "guia_remision"
	if sunatCode == "31" {
		docType = "GUIA_TRANSPORTISTA"
		reserveKind = "guide_carrier"
		dispatchKind = "guia_transportista"
	}
	if err := docusage.GuardCountableSunatQuota(s.centralTenantID, sunatCode); err != nil {
		return nil, err
	}
	partidaUbi := strings.TrimSpace(input.Envio.PartidaUbigueo)
	if partidaUbi == "" {
		partidaUbi = strings.TrimSpace(companyCfg.Ubigeo)
	}
	if partidaUbi == "" {
		return nil, fmt.Errorf("ubigeo de partida es obligatorio (o configure ubigeo de empresa)")
	}
	llegadaUbi := strings.TrimSpace(input.Envio.LlegadaUbigueo)
	if llegadaUbi == "" {
		return nil, fmt.Errorf("ubigeo de llegada es obligatorio")
	}
	destAddr, errDest := s.buildInvoiceAddressFromUbigeo(input.Destinatario.Ubigeo, input.Destinatario.Address)
	if errDest != nil {
		return nil, fmt.Errorf("destinatario: %w", errDest)
	}
	partidaDir := strings.TrimSpace(input.Envio.PartidaDireccion)
	if partidaDir == "" {
		partidaDir = strings.TrimSpace(companyCfg.Address)
	}
	if partidaDir == "" {
		return nil, fmt.Errorf("dirección de partida es obligatoria")
	}
	depP, provP, distP, errP := s.resolveUbigeoToAddress(partidaUbi)
	if errP != nil {
		return nil, fmt.Errorf("partida: %w", errP)
	}
	partida := facturador.DespatchDirection{Ubigueo: partidaUbi, CodigoPais: "PE", Departamento: depP, Provincia: provP, Distrito: distP, Direccion: partidaDir}
	llegadaDir := strings.TrimSpace(input.Envio.LlegadaDireccion)
	if llegadaDir == "" {
		llegadaDir = strings.TrimSpace(input.Destinatario.Address)
	}
	if llegadaDir == "" {
		return nil, fmt.Errorf("dirección de llegada es obligatoria")
	}
	depL, provL, distL, errL := s.resolveUbigeoToAddress(llegadaUbi)
	if errL != nil {
		return nil, fmt.Errorf("llegada: %w", errL)
	}
	llegada := facturador.DespatchDirection{Ubigueo: llegadaUbi, CodigoPais: "PE", Departamento: depL, Provincia: provL, Distrito: distL, Direccion: llegadaDir}
	saleSvc := salesvc.NewSaleService(s.db)
	nextCorr, err := saleSvc.NextCorrelative(series.ID)
	if err != nil {
		return nil, err
	}
	correlativoStr := strconv.FormatUint(uint64(nextCorr), 10)
	now := time.Now()
	fechaEmision := facturador.FormatFiscalDateTime(now)
	fecTraslado := strings.TrimSpace(input.Envio.FecTraslado)
	if fecTraslado == "" {
		fecTraslado = fechaEmision
	} else {
		fecTraslado = normalizeDespatchDateTime(fecTraslado, fechaEmision)
	}
	fecEntrega := strings.TrimSpace(input.Envio.FecEntregaTransportista)
	if fecEntrega == "" {
		fecEntrega = fecTraslado
	} else {
		fecEntrega = normalizeDespatchDateTime(fecEntrega, fecTraslado)
	}
	input.Envio.FecTraslado = fecTraslado
	input.Envio.FecEntregaTransportista = fecEntrega
	shipment := buildDespatchShipment(input, sunatCode, fechaEmision, companyCfg.RUC, companyCfg.BusinessName, partida, llegada)
	details := make([]facturador.DespatchDetail, len(input.Details))
	for i, d := range input.Details {
		details[i] = facturador.DespatchDetail{Codigo: d.Codigo, Descripcion: d.Descripcion, Unidad: d.Unidad, Cantidad: d.Cantidad}
		if details[i].Unidad == "" {
			details[i].Unidad = "NIU"
		}
	}
	payload := &facturador.DespatchPayload{
		Version:      "2022",
		TipoDoc:      sunatCode,
		Serie:        series.Series,
		Correlativo:  correlativoStr,
		FechaEmision: fechaEmision,
		Company:      facturador.InvoiceCompany{RUC: companyCfg.RUC, RazonSocial: companyCfg.BusinessName, NombreComercial: nombreComercial, Address: companyAddr},
		Destinatario: facturador.InvoiceClient{TipoDoc: input.Destinatario.TipoDoc, NumDoc: input.Destinatario.NumDoc, RznSocial: input.Destinatario.RznSocial, Address: destAddr},
		Envio:        shipment,
		Details:      details,
	}
	if sunatCode == "31" {
		remAddr, errRem := s.buildInvoiceAddressFromUbigeo(input.Remitente.Ubigeo, input.Remitente.Address)
		if errRem != nil {
			return nil, fmt.Errorf("remitente: %w", errRem)
		}
		remTipoDoc, remNumDoc := normalizeGrePartyDoc(input.Remitente.TipoDoc, input.Remitente.NumDoc)
		payload.Tercero = &facturador.InvoiceClient{
			TipoDoc:   remTipoDoc,
			NumDoc:    remNumDoc,
			RznSocial: strings.TrimSpace(input.Remitente.RznSocial),
			Address:   remAddr,
		}
	}
	if input.SourceSaleID != nil && *input.SourceSaleID > 0 {
		if addDoc := s.despatchAddDocFromSale(*input.SourceSaleID, companyCfg.RUC); addDoc != nil {
			payload.AddDocs = []facturador.DespatchAdditionalDoc{*addDoc}
		}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("serializar payload guía: %w", err)
	}
	payloadJSONStr := enrichDespatchFiscalPayloadJSON(string(payloadJSON), sunatCode, dispatchKind)
	docNum := fmt.Sprintf("%s-%s", series.Series, correlativoStr)
	numberStr := fmt.Sprintf("%s-%08d", series.Series, nextCorr)

	guiaSale := database.TenantSale{
		BranchID:      input.BranchID,
		SeriesID:      input.SeriesID,
		DocType:       docType,
		Series:        series.Series,
		Correlative:   nextCorr,
		Number:        numberStr,
		IssueDate:     now,
		Currency:      "PEN",
		Status:        "paid",
		BillingStatus: "pending",
	}
	if err := s.db.Create(&guiaSale).Error; err != nil {
		return nil, fmt.Errorf("crear venta guía: %w", err)
	}
	for _, d := range input.Details {
		unit := d.Unidad
		if unit == "" {
			unit = "NIU"
		}
		if err := s.db.Create(&database.TenantSaleItem{
			SaleID:      guiaSale.ID,
			Code:        d.Codigo,
			Description: d.Descripcion,
			Unit:        unit,
			Quantity:    d.Cantidad,
		}).Error; err != nil {
			return nil, fmt.Errorf("crear ítem guía: %w", err)
		}
	}
	if err := s.reserveGenericDocument(reserveKind, guiaSale.ID, docNum); err != nil {
		return nil, err
	}

	saleID := guiaSale.ID
	rec := &database.TenantDespatch{
		SaleID:            &saleID,
		BranchID:          input.BranchID,
		SeriesID:          input.SeriesID,
		Series:            series.Series,
		Correlative:       nextCorr,
		IssueDate:         now,
		DestinatarioRUC:   input.Destinatario.NumDoc,
		DestinatarioRazon: input.Destinatario.RznSocial,
		Status:            "pending",
		PayloadJSON:       payloadJSONStr,
		DetailsCount:      len(details),
	}
	if err := s.db.Create(rec).Error; err != nil {
		return nil, err
	}

	tenantDB := s.lookupTenantDBName(s.centralTenantID)
	if _, err := s.EnqueueSendToSUNAT(guiaSale.ID, s.centralTenantID, s.tenantSlug, tenantDB, FiscalSourceAutoCreate); err != nil {
		return rec, err
	}
	if input.SourceSaleID != nil && *input.SourceSaleID > 0 {
		s.linkDespatchFiscalReference(*input.SourceSaleID, sunatCode, docNum, guiaSale.ID)
	}
	return rec, nil
}

func (s *BillingService) GetDespatchStatus(id uint) (*database.TenantDespatch, error) {
	var rec database.TenantDespatch
	if err := s.db.First(&rec, id).Error; err != nil {
		return nil, err
	}
	if rec.SaleID != nil && *rec.SaleID > 0 {
		_ = s.SyncSaleWithSSOT(*rec.SaleID)
		if st, _ := s.GetBillingStatus(*rec.SaleID); st != nil {
			s.syncLinkedDespatchStatus(*rec.SaleID, st.Pipeline)
		}
		_ = s.db.First(&rec, id).Error
		if rec.Status == "accepted" || rec.Status == "rejected" || rec.Status == "error" {
			return &rec, nil
		}
	}
	if rec.Ticket == "" {
		return &rec, nil
	}
	var cfg database.TenantCompanyConfig
	if s.db.Select("ruc").First(&cfg).Error != nil {
		return &rec, nil
	}
	client := facturador.Shared()
	result, err := client.GetDespatchStatus(rec.Ticket, cfg.RUC)
	if err != nil {
		return &rec, err
	}
	if result.Success && result.CDRZip != "" {
		basePath := config.AppConfig.InvoiceStoragePath
		if basePath == "" {
			basePath = "./storage/invoices"
		}
		cdrDec, _ := base64.StdEncoding.DecodeString(result.CDRZip)
		if len(cdrDec) > 0 {
			ruc := cfg.RUC
			if ruc == "" {
				ruc = "default"
			}
			sunatCode := strings.TrimSpace(getSeriesSunatCode(s.db, rec.SeriesID))
			if sunatCode != "09" && sunatCode != "31" {
				sunatCode = "09"
			}
			cdrPath, _ := saveInvoiceFile(basePath, ruc, "lycet", sunatCode, rec.Series, strconv.FormatUint(uint64(rec.Correlative), 10), "cdr.zip", cdrDec)
			rec.CDRURL = cdrPath
		}
		if result.CDRResponse != nil {
			rec.SunatCode = result.CDRResponse.Code
			rec.SunatMessage = result.CDRResponse.Description
			if result.CDRResponse.Accepted {
				rec.Status = "accepted"
			} else {
				rec.Status = "rejected"
			}
		} else {
			rec.Status = "accepted"
		}
	} else if result.Error != nil {
		rec.SunatMessage = result.Error.Message
		rec.SunatCode = result.Error.Code
		rec.Status = "error"
	}
	s.db.Save(&rec)
	return &rec, nil
}

// --- Retención ---

func (s *BillingService) ListRetentions() ([]database.TenantRetention, error) {
	var list []database.TenantRetention
	err := s.db.Order("fecha_emision DESC, created_at DESC").Find(&list).Error
	return list, err
}

type CreateRetentionInput struct {
	Series       string                  `json:"series"`
	Correlativo  string                  `json:"correlativo"`
	FechaEmision string                  `json:"fecha_emision"`
	Proveedor    RetentionProveedorInput `json:"proveedor"`
	Regimen      string                  `json:"regimen"`
	Tasa         float64                 `json:"tasa"`
	ImpRetenido  float64                 `json:"imp_retenido"`
	ImpPagado    float64                 `json:"imp_pagado"`
	Details      []RetentionDetailInput  `json:"details"`
}

type RetentionProveedorInput struct {
	TipoDoc   string `json:"tipo_doc"`
	NumDoc    string `json:"num_doc"`
	RznSocial string `json:"rzn_social"`
	Address   string `json:"address"`
	Ubigeo    string `json:"ubigeo"`
}

type RetentionDetailInput struct {
	TipoDoc        string  `json:"tipo_doc"`
	NumDoc         string  `json:"num_doc"`
	FechaEmision   string  `json:"fecha_emision"`
	ImpTotal       float64 `json:"imp_total"`
	Moneda         string  `json:"moneda"`
	FechaRetencion string  `json:"fecha_retencion"`
	ImpRetenido    float64 `json:"imp_retenido"`
	ImpPagar       float64 `json:"imp_pagar"`
}

func (s *BillingService) CreateAndSendRetention(input CreateRetentionInput) (*database.TenantRetention, error) {
	if !s.facturadorConfigured() {
		return nil, errors.New("retención requiere facturador configurado")
	}
	companyCfg, companyAddr, err := s.getCompanyConfigAndAddress()
	if err != nil {
		return nil, err
	}
	nombreComercial := companyCfg.TradeName
	if nombreComercial == "" {
		nombreComercial = companyCfg.BusinessName
	}
	provAddr, errProv := s.buildInvoiceAddressFromUbigeo(input.Proveedor.Ubigeo, input.Proveedor.Address)
	if errProv != nil {
		return nil, fmt.Errorf("proveedor: %w", errProv)
	}
	details := make([]facturador.RetentionDetail, len(input.Details))
	for i, d := range input.Details {
		details[i] = facturador.RetentionDetail{
			TipoDoc: d.TipoDoc, NumDoc: d.NumDoc, FechaEmision: d.FechaEmision,
			ImpTotal: d.ImpTotal, Moneda: d.Moneda, FechaRetencion: d.FechaRetencion,
			ImpRetenido: d.ImpRetenido, ImpPagar: d.ImpPagar,
		}
		if details[i].Moneda == "" {
			details[i].Moneda = "PEN"
		}
	}
	payload := &facturador.RetentionPayload{
		Serie:        input.Series,
		Correlativo:  input.Correlativo,
		FechaEmision: input.FechaEmision,
		Company:      facturador.InvoiceCompany{RUC: companyCfg.RUC, RazonSocial: companyCfg.BusinessName, NombreComercial: nombreComercial, Address: companyAddr},
		Proveedor:    facturador.InvoiceClient{TipoDoc: input.Proveedor.TipoDoc, NumDoc: input.Proveedor.NumDoc, RznSocial: input.Proveedor.RznSocial, Address: provAddr},
		Regimen:      input.Regimen,
		Tasa:         input.Tasa,
		ImpRetenido:  input.ImpRetenido,
		ImpPagado:    input.ImpPagado,
		Details:      details,
	}
	payloadJSON, _ := json.Marshal(payload)
	corrNum := parseCorrelativeUint(input.Correlativo)
	issueDate := time.Now()
	if t, err := time.Parse(time.RFC3339, input.FechaEmision); err == nil {
		issueDate = t
	}
	var seriesRec database.TenantDocumentSeries
	_ = s.db.Where("series = ?", input.Series).First(&seriesRec).Error

	retSale := database.TenantSale{
		SeriesID:      seriesRec.ID,
		DocType:       "RETENCION",
		Series:        input.Series,
		Correlative:   corrNum,
		Number:        fmt.Sprintf("%s-%s", input.Series, input.Correlativo),
		IssueDate:     issueDate,
		Total:         input.ImpPagado,
		Currency:      "PEN",
		Status:        "paid",
		BillingStatus: "pending",
	}
	if err := s.db.Create(&retSale).Error; err != nil {
		return nil, fmt.Errorf("crear venta retención: %w", err)
	}
	if err := s.reserveGenericDocument("retention", retSale.ID, input.Series+"-"+input.Correlativo); err != nil {
		return nil, err
	}

	saleID := retSale.ID
	rec := &database.TenantRetention{
		SaleID:         &saleID,
		Series:         input.Series,
		Correlative:    input.Correlativo,
		ProveedorRUC:   input.Proveedor.NumDoc,
		ProveedorRazon: input.Proveedor.RznSocial,
		Regimen:        input.Regimen,
		Tasa:           input.Tasa,
		ImpRetenido:    input.ImpRetenido,
		ImpPagado:      input.ImpPagado,
		PayloadJSON:    string(payloadJSON),
		DetailsCount:   len(details),
		Status:         "pending",
		FechaEmision:   issueDate,
	}
	if err := s.db.Create(rec).Error; err != nil {
		return nil, err
	}

	tenantDB := s.lookupTenantDBName(s.centralTenantID)
	if _, err := s.EnqueueSendToSUNAT(retSale.ID, s.centralTenantID, s.tenantSlug, tenantDB, FiscalSourceAutoCreate); err != nil {
		return rec, err
	}
	return rec, nil
}

// --- Percepción ---

func (s *BillingService) ListPerceptions() ([]database.TenantPerception, error) {
	var list []database.TenantPerception
	err := s.db.Order("fecha_emision DESC, created_at DESC").Find(&list).Error
	return list, err
}

type CreatePerceptionInput struct {
	Series       string                   `json:"series"`
	Correlativo  string                   `json:"correlativo"`
	FechaEmision string                   `json:"fecha_emision"`
	Proveedor    PerceptionProveedorInput `json:"proveedor"`
	Regimen      string                   `json:"regimen"`
	Tasa         float64                  `json:"tasa"`
	ImpPercibido float64                  `json:"imp_percibido"`
	ImpCobrado   float64                  `json:"imp_cobrado"`
	Details      []PerceptionDetailInput  `json:"details"`
}

type PerceptionProveedorInput struct {
	TipoDoc   string `json:"tipo_doc"`
	NumDoc    string `json:"num_doc"`
	RznSocial string `json:"rzn_social"`
	Address   string `json:"address"`
	Ubigeo    string `json:"ubigeo"`
}

type PerceptionDetailInput struct {
	TipoDoc         string  `json:"tipo_doc"`
	NumDoc          string  `json:"num_doc"`
	FechaEmision    string  `json:"fecha_emision"`
	ImpTotal        float64 `json:"imp_total"`
	Moneda          string  `json:"moneda"`
	FechaPercepcion string  `json:"fecha_percepcion"`
	ImpPercibido    float64 `json:"imp_percibido"`
	ImpCobrar       float64 `json:"imp_cobrar"`
}

func (s *BillingService) CreateAndSendPerception(input CreatePerceptionInput) (*database.TenantPerception, error) {
	if !s.facturadorConfigured() {
		return nil, errors.New("percepción requiere facturador configurado")
	}
	companyCfg, companyAddr, err := s.getCompanyConfigAndAddress()
	if err != nil {
		return nil, err
	}
	nombreComercial := companyCfg.TradeName
	if nombreComercial == "" {
		nombreComercial = companyCfg.BusinessName
	}
	provAddr, errProv := s.buildInvoiceAddressFromUbigeo(input.Proveedor.Ubigeo, input.Proveedor.Address)
	if errProv != nil {
		return nil, fmt.Errorf("proveedor: %w", errProv)
	}
	details := make([]facturador.PerceptionDetail, len(input.Details))
	for i, d := range input.Details {
		details[i] = facturador.PerceptionDetail{
			TipoDoc: d.TipoDoc, NumDoc: d.NumDoc, FechaEmision: d.FechaEmision,
			ImpTotal: d.ImpTotal, Moneda: d.Moneda, FechaPercepcion: d.FechaPercepcion,
			ImpPercibido: d.ImpPercibido, ImpCobrar: d.ImpCobrar,
		}
		if details[i].Moneda == "" {
			details[i].Moneda = "PEN"
		}
	}
	payload := &facturador.PerceptionPayload{
		Serie:        input.Series,
		Correlativo:  input.Correlativo,
		FechaEmision: input.FechaEmision,
		Company:      facturador.InvoiceCompany{RUC: companyCfg.RUC, RazonSocial: companyCfg.BusinessName, NombreComercial: nombreComercial, Address: companyAddr},
		Proveedor:    facturador.InvoiceClient{TipoDoc: input.Proveedor.TipoDoc, NumDoc: input.Proveedor.NumDoc, RznSocial: input.Proveedor.RznSocial, Address: provAddr},
		Regimen:      input.Regimen,
		Tasa:         input.Tasa,
		ImpPercibido: input.ImpPercibido,
		ImpCobrado:   input.ImpCobrado,
		Details:      details,
	}
	payloadJSON, _ := json.Marshal(payload)
	corrNum := parseCorrelativeUint(input.Correlativo)
	issueDate := time.Now()
	if t, err := time.Parse(time.RFC3339, input.FechaEmision); err == nil {
		issueDate = t
	}
	var seriesRec database.TenantDocumentSeries
	_ = s.db.Where("series = ?", input.Series).First(&seriesRec).Error

	percSale := database.TenantSale{
		SeriesID:      seriesRec.ID,
		DocType:       "PERCEPCION",
		Series:        input.Series,
		Correlative:   corrNum,
		Number:        fmt.Sprintf("%s-%s", input.Series, input.Correlativo),
		IssueDate:     issueDate,
		Total:         input.ImpCobrado,
		Currency:      "PEN",
		Status:        "paid",
		BillingStatus: "pending",
	}
	if err := s.db.Create(&percSale).Error; err != nil {
		return nil, fmt.Errorf("crear venta percepción: %w", err)
	}
	if err := s.reserveGenericDocument("perception", percSale.ID, input.Series+"-"+input.Correlativo); err != nil {
		return nil, err
	}

	saleID := percSale.ID
	rec := &database.TenantPerception{
		SaleID:         &saleID,
		Series:         input.Series,
		Correlative:    input.Correlativo,
		ProveedorRUC:   input.Proveedor.NumDoc,
		ProveedorRazon: input.Proveedor.RznSocial,
		Regimen:        input.Regimen,
		Tasa:           input.Tasa,
		ImpPercibido:   input.ImpPercibido,
		ImpCobrado:     input.ImpCobrado,
		PayloadJSON:    string(payloadJSON),
		DetailsCount:   len(details),
		Status:         "pending",
		FechaEmision:   issueDate,
	}
	if err := s.db.Create(rec).Error; err != nil {
		return nil, err
	}

	tenantDB := s.lookupTenantDBName(s.centralTenantID)
	if _, err := s.EnqueueSendToSUNAT(percSale.ID, s.centralTenantID, s.tenantSlug, tenantDB, FiscalSourceAutoCreate); err != nil {
		return rec, err
	}
	return rec, nil
}

func (s *BillingService) GetRetentionStatus(id uint) (*database.TenantRetention, error) {
	var rec database.TenantRetention
	if err := s.db.First(&rec, id).Error; err != nil {
		return nil, err
	}
	if rec.SaleID != nil && *rec.SaleID > 0 {
		_ = s.SyncSaleWithSSOT(*rec.SaleID)
		if st, _ := s.GetBillingStatus(*rec.SaleID); st != nil {
			s.syncLinkedRetentionStatus(*rec.SaleID, st.Pipeline)
		}
		_ = s.db.First(&rec, id).Error
	}
	return &rec, nil
}

func (s *BillingService) GetPerceptionStatus(id uint) (*database.TenantPerception, error) {
	var rec database.TenantPerception
	if err := s.db.First(&rec, id).Error; err != nil {
		return nil, err
	}
	if rec.SaleID != nil && *rec.SaleID > 0 {
		_ = s.SyncSaleWithSSOT(*rec.SaleID)
		if st, _ := s.GetBillingStatus(*rec.SaleID); st != nil {
			s.syncLinkedPerceptionStatus(*rec.SaleID, st.Pipeline)
		}
		_ = s.db.First(&rec, id).Error
	}
	return &rec, nil
}

// --- Reversión (mismo esquema que voided) ---

func (s *BillingService) ListReversions() ([]database.TenantSunatReversion, error) {
	var list []database.TenantSunatReversion
	err := s.db.Order("fec_comunicacion DESC, created_at DESC").Find(&list).Error
	return list, err
}

func (s *BillingService) CreateReversion(details []CreateVoidedInput) (*database.TenantSunatReversion, error) {
	if !s.facturadorConfigured() {
		return nil, errors.New("reversión requiere facturador configurado")
	}
	if len(details) == 0 {
		return nil, errors.New("se requiere al menos un comprobante para revertir")
	}
	companyCfg, companyAddr, err := s.getCompanyConfigAndAddress()
	if err != nil {
		return nil, err
	}
	nombreComercial := companyCfg.TradeName
	if nombreComercial == "" {
		nombreComercial = companyCfg.BusinessName
	}
	now := time.Now()
	var count int64
	s.db.Model(&database.TenantSunatReversion{}).Where("DATE(fec_comunicacion) = ?", now.Format("2006-01-02")).Count(&count)
	correlativo := strconv.FormatInt(count+1, 10)
	voidedDetails := make([]facturador.VoidedDetail, len(details))
	for i, d := range details {
		voidedDetails[i] = facturador.VoidedDetail{TipoDoc: d.TipoDoc, Serie: d.Serie, Correlativo: d.Correlativo, DesMotivoBaja: d.DesMotivoBaja}
	}
	payload := &facturador.VoidedPayload{
		Company:         facturador.InvoiceCompany{RUC: companyCfg.RUC, RazonSocial: companyCfg.BusinessName, NombreComercial: nombreComercial, Address: companyAddr},
		Correlativo:     correlativo,
		FecGeneracion:   now.Format(time.RFC3339),
		FecComunicacion: now.Format(time.RFC3339),
		Details:         voidedDetails,
	}
	payloadJSON, _ := json.Marshal(payload)
	rec := &database.TenantSunatReversion{
		FecComunicacion: now,
		Correlativo:     correlativo,
		Status:          "pending",
		PayloadJSON:     string(payloadJSON),
		DetailsCount:    len(details),
	}
	if err := s.db.Create(rec).Error; err != nil {
		return nil, err
	}
	if err := s.reserveGenericDocument("reversion", rec.ID, correlativo); err != nil {
		return nil, err
	}
	client := facturador.Shared()
	resp, err := client.SendReversion(payload)
	if err != nil {
		return nil, err
	}
	rec.Ticket = resp.Ticket()
	if resp.CDRZipBase64() != "" {
		rec.Status = "accepted"
		if resp.SunatResponse != nil && resp.SunatResponse.CDRResponse != nil {
			rec.SunatCode = resp.SunatResponse.CDRResponse.Code
			rec.SunatMessage = resp.SunatResponse.CDRResponse.Description
			if !resp.SunatResponse.CDRResponse.Accepted {
				rec.Status = "rejected"
			}
		}
		basePath := config.AppConfig.InvoiceStoragePath
		if basePath == "" {
			basePath = "./storage/invoices"
		}
		ruc := companyCfg.RUC
		if ruc == "" {
			ruc = "default"
		}
		cdrDec, _ := base64.StdEncoding.DecodeString(resp.CDRZipBase64())
		if len(cdrDec) > 0 {
			cdrPath, _ := saveInvoiceFile(basePath, ruc, "lycet", "RR", "RR", correlativo+"-"+now.Format("20060102"), "cdr.zip", cdrDec)
			rec.CDRURL = cdrPath
		}
	} else if resp.Message() != "" {
		rec.SunatMessage = resp.Message()
		rec.SunatCode = resp.CDRCode()
		if resp.Success() {
			rec.Status = "accepted"
		} else {
			rec.Status = "rejected"
		}
	}
	if err := s.db.Save(rec).Error; err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *BillingService) GetReversionStatus(id uint) (*database.TenantSunatReversion, error) {
	var rec database.TenantSunatReversion
	if err := s.db.First(&rec, id).Error; err != nil {
		return nil, err
	}
	if rec.Ticket == "" {
		return &rec, nil
	}
	var cfg database.TenantCompanyConfig
	if s.db.Select("ruc").First(&cfg).Error != nil {
		return &rec, nil
	}
	client := facturador.Shared()
	result, err := client.GetReversionStatus(rec.Ticket, cfg.RUC)
	if err != nil {
		return &rec, err
	}
	if result.Success && result.CDRZip != "" {
		basePath := config.AppConfig.InvoiceStoragePath
		if basePath == "" {
			basePath = "./storage/invoices"
		}
		cdrDec, _ := base64.StdEncoding.DecodeString(result.CDRZip)
		if len(cdrDec) > 0 {
			ruc := cfg.RUC
			if ruc == "" {
				ruc = "default"
			}
			cdrPath, _ := saveInvoiceFile(basePath, ruc, "lycet", "RR", "RR", rec.Correlativo+"-"+rec.FecComunicacion.Format("20060102"), "cdr.zip", cdrDec)
			rec.CDRURL = cdrPath
		}
		if result.CDRResponse != nil {
			rec.SunatCode = result.CDRResponse.Code
			rec.SunatMessage = result.CDRResponse.Description
			if result.CDRResponse.Accepted {
				rec.Status = "accepted"
			} else {
				rec.Status = "rejected"
			}
		} else {
			rec.Status = "accepted"
		}
	} else if result.Error != nil {
		rec.SunatMessage = result.Error.Message
		rec.SunatCode = result.Error.Code
		rec.Status = "error"
	}
	s.db.Save(&rec)
	return &rec, nil
}
