package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"errors"

	"tukifac/config"
	"tukifac/internal/billing/service"
	"tukifac/internal/sales/branchguard"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas/docusage"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type BillingHandler struct{}

func NewBillingHandler() *BillingHandler { return &BillingHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}
func email(c fiber.Ctx) string {
	v, _ := c.Locals("user_email").(string)
	return v
}
func tenantName(c fiber.Ctx) string {
	if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
		return t.Name
	}
	return ""
}

func billingSvc(c fiber.Ctx) *service.BillingService {
	svc := service.NewBillingService(db(c))
	if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
		svc.SetCentralTenantID(t.ID)
		svc.SetTenantSlug(t.Slug)
	} else if slug, ok := c.Locals("tenant_slug").(string); ok && slug != "" {
		svc.SetTenantSlug(slug)
	}
	return svc
}

// notFound 404 uniforme de las guardas de sucursal: no revela si el ID existe en otra sucursal.
func notFound(c fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
}

// listScope sucursal de un listado fiscal: branch_id pedido (si el usuario accede a ella), "all"
// solo para quien ve todas las sucursales, y en otro caso la activa. 0 = todas.
func listScope(c fiber.Ctx) uint {
	raw := strings.TrimSpace(c.Query("branch_id"))
	if strings.EqualFold(raw, "all") {
		return branch.ResolveReportBranchFilter(c, 0, true)
	}
	req, _ := strconv.ParseUint(raw, 10, 32)
	return branch.ResolveReportBranchFilter(c, uint(req), true)
}

func (h *BillingHandler) ListPage(c fiber.Ctx) error {
	svc := billingSvc(c)
	invoices, _ := svc.ListInvoices(service.InvoiceListParams{
		Status: c.Query("status"),
	})
	return c.Render("billing/index", fiber.Map{
		"Title":      "Facturación Electrónica",
		"UserEmail":  email(c),
		"TenantName": tenantName(c),
		"IsDev":      config.AppConfig.IsDev(),
		"Invoices":   invoices,
		"Status":     c.Query("status"),
		"Success":    c.Query("success"),
	}, "layouts/base")
}

func (h *BillingHandler) SendToSUNAT(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}

	if err := branchguard.SaleWritable(c, db(c), uint(saleID)); err != nil {
		return notFound(c, "venta no encontrada")
	}

	tenant, _ := c.Locals("tenant").(*database.Tenant)
	slug, _ := c.Locals("tenant_slug").(string)
	var tenantID uint
	if tenant != nil {
		tenantID = tenant.ID
	}

	svc := billingSvc(c)
	result, err := svc.ManualSendToSUNAT(uint(saleID), tenantID, slug)
	if err != nil {
		code := fiber.StatusBadRequest
		if errors.Is(err, docusage.ErrQuotaExceeded) {
			code = fiber.StatusPaymentRequired
		}
		return c.Status(code).JSON(fiber.Map{
			"error":  err.Error(),
			"code":   "DOCUMENT_QUOTA_EXCEEDED",
			"status": "error",
		})
	}
	return c.JSON(result)
}

// GetBillingStatus GET /api/billing/status/:saleId — estado verificable (polling).
func (h *BillingHandler) GetBillingStatus(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := branchguard.SaleReadable(c, db(c), uint(saleID)); err != nil {
		return notFound(c, "venta no encontrada")
	}
	svc := billingSvc(c)
	st, err := svc.GetBillingStatus(uint(saleID))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(st)
}

// GetBillingJobStatus GET /api/billing/job/:saleId
func (h *BillingHandler) GetBillingJobStatus(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := branchguard.SaleReadable(c, db(c), uint(saleID)); err != nil {
		return notFound(c, "comprobante no encontrado")
	}
	svc := billingSvc(c)
	inv, err := svc.GetBillingJobStatus(uint(saleID))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "comprobante no encontrado"})
	}
	return c.JSON(fiber.Map{"invoice": inv})
}

// ResendToSUNAT sincroniza SSOT, reconcilia estado local y reenvía de forma síncrona si procede.
func (h *BillingHandler) ResendToSUNAT(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := branchguard.SaleWritable(c, db(c), uint(saleID)); err != nil {
		return notFound(c, "venta no encontrada")
	}
	svc := billingSvc(c)
	result, err := svc.ManualResendToSUNAT(uint(saleID))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":  err.Error(),
			"status": "error",
		})
	}
	return c.JSON(result)
}

// ReissueToSUNAT POST /api/billing/reissue/:saleId
//
// Reenvía un comprobante con otra fecha de emisión conservando su numeración.
// La ruta está detrás de middleware.RequireMasterAccess: el tenant no puede
// ejecutarla, solo soporte entrando por acceso maestro desde el panel central.
func (h *BillingHandler) ReissueToSUNAT(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}

	var body struct {
		IssueDate   string  `json:"issue_date"`
		Observation *string `json:"observation"`
		Reason      string  `json:"reason"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	issueDate, err := parseReissueDate(body.IssueDate)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	claims, _ := c.Locals("tenant_claims").(*middleware.TenantClaims)
	in := service.ReissueInput{
		SaleID:    uint(saleID),
		IssueDate: issueDate,
		Reason:    body.Reason,
		ClientIP:  c.IP(),
	}
	if claims != nil {
		in.ActorID = claims.MasterActorID
		in.ActorEmail = claims.MasterActorEmail
	}
	if body.Observation != nil {
		in.SetObservation = true
		in.Observation = *body.Observation
	}

	result, err := billingSvc(c).ReissueSale(in)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":  err.Error(),
			"status": "error",
		})
	}
	return c.JSON(result)
}

// parseReissueDate acepta la fecha en formato de día (YYYY-MM-DD) o ISO completo.
// Se interpreta en America/Lima: el día calendario es lo que cuenta para el plazo
// de SUNAT, y tomarlo en UTC correría la fecha en las primeras horas del día.
func parseReissueDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("la fecha de emisión es obligatoria")
	}
	loc, err := time.LoadLocation("America/Lima")
	if err != nil {
		loc = time.Local
	}
	if d, err := time.ParseInLocation("2006-01-02", raw, loc); err == nil {
		return d, nil
	}
	if d, err := time.Parse(time.RFC3339, raw); err == nil {
		return d.In(loc), nil
	}
	return time.Time{}, errors.New("formato de fecha inválido; use YYYY-MM-DD")
}

// VoidWithCreditNoteAPI anula la venta generando y enviando una nota de crédito a SUNAT; luego anula la venta original.
func (h *BillingHandler) VoidWithCreditNoteAPI(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Reason     string                      `json:"reason"`
		ReasonCode string                      `json:"reason_code"`
		Items      []service.NoteItemSelection `json:"items"`
		// Amount: importe total (con IGV) a acreditar, repartido entre Items (o todas las líneas si no
		// hay ítems). Nota por monto — solo motivos 04, 05, 09, 10.
		Amount float64 `json:"amount"`
		// IssueDate: fecha de emisión de la nota (AAAA-MM-DD). Vacía = hoy.
		IssueDate string `json:"issue_date"`
	}
	_ = c.Bind().Body(&body)
	if err := branchguard.SaleWritable(c, db(c), uint(saleID)); err != nil {
		return notFound(c, "venta no encontrada")
	}
	svc := billingSvc(c)
	ncSale, ncInvoice, err := svc.CreateCreditNote(uint(saleID), strings.TrimSpace(body.Reason), strings.TrimSpace(body.ReasonCode), body.Items, body.Amount, strings.TrimSpace(body.IssueDate))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":   err.Error(),
			"nc_sale": ncSale,
			"invoice": ncInvoice,
		})
	}
	// Solo el motivo "01" anula la venta al aceptarse (ver PostFiscalAcceptSideEffects) — el
	// mensaje no puede decir siempre "se anulará" ahora que hay otros motivos disponibles.
	message := "Nota de crédito encolada"
	if ncSale != nil {
		code := strings.TrimSpace(ncSale.NoteReasonCode)
		if code == "" || code == "01" {
			message = "Nota de crédito encolada; la venta original se anulará al aceptar SUNAT"
		}
	}
	return c.JSON(fiber.Map{
		"success": true,
		"message": message,
		"async":   true,
		"nc_sale": ncSale,
		"invoice": ncInvoice,
	})
}

func (h *BillingHandler) CreateDebitNoteAPI(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Reason     string `json:"reason"`
		ReasonCode string `json:"reason_code"`
	}
	_ = c.Bind().Body(&body)
	if err := branchguard.SaleWritable(c, db(c), uint(saleID)); err != nil {
		return notFound(c, "venta no encontrada")
	}
	svc := billingSvc(c)
	ndSale, ndInvoice, err := svc.CreateDebitNoteForSale(uint(saleID), strings.TrimSpace(body.Reason), strings.TrimSpace(body.ReasonCode))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":   err.Error(),
			"nd_sale": ndSale,
			"invoice": ndInvoice,
		})
	}
	return c.JSON(fiber.Map{
		"success": true,
		"message": "Nota de débito encolada para emisión SUNAT",
		"async":   true,
		"nd_sale": ndSale,
		"invoice": ndInvoice,
	})
}

// CreateIndependentNoteAPI emite una NC/ND que no nace de una venta de Tukifac (Fase 3) — el
// comprobante afectado se declara a mano (tipo, serie, número).
func (h *BillingHandler) CreateIndependentNoteAPI(c fiber.Ctx) error {
	var body service.IndependentNoteInput
	if err := c.Bind().Body(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	// branch_id del body: solo una sucursal a la que el usuario accede (0 = la activa).
	branchID, err := branch.ResolveWriteBranchID(c, body.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	body.BranchID = branchID
	svc := billingSvc(c)
	noteSale, invoice, err := svc.CreateIndependentNote(body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":   err.Error(),
			"nc_sale": noteSale,
			"invoice": invoice,
		})
	}
	return c.JSON(fiber.Map{
		"success": true,
		"message": "Nota encolada para emisión SUNAT",
		"async":   true,
		"nc_sale": noteSale,
		"invoice": invoice,
	})
}

func (h *BillingHandler) GetInvoiceAPI(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}

	if err := branchguard.SaleReadable(c, db(c), uint(saleID)); err != nil {
		return notFound(c, "Sin registro de facturación")
	}
	svc := billingSvc(c)
	invoice, err := svc.GetInvoice(uint(saleID))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if invoice == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sin registro de facturación"})
	}
	return c.JSON(invoice)
}

func (h *BillingHandler) GetInvoiceDocumentAPI(c fiber.Ctx) error {
	saleID, err := strconv.ParseUint(c.Params("saleId"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	kind := c.Params("kind") // xml, xml-generated, cdr, pdf
	validKind := kind == "xml" || kind == "xml-generated" || kind == "cdr" || kind == "pdf"
	if !validKind {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Tipo de documento no válido. Use: xml, xml-generated, cdr o pdf"})
	}
	if err := branchguard.SaleReadable(c, db(c), uint(saleID)); err != nil {
		return notFound(c, "Documento no disponible")
	}
	svc := billingSvc(c)

	c.Set(fiber.HeaderAccessControlExposeHeaders, "Content-Disposition")

	// Nombre de archivo según formato SUNAT (ej. 03-B001-26.pdf, 03-B001-26.xml).
	filename, _ := svc.GetInvoiceDocumentFilename(uint(saleID), kind)
	if filename == "" {
		switch kind {
		case "pdf":
			filename = "comprobante.pdf"
		case "xml":
			filename = "comprobante-enviado.xml"
		case "xml-generated":
			filename = "comprobante-generado.xml"
		case "cdr":
			filename = "cdr.zip"
		default:
			filename = "comprobante"
		}
	}

	// PDF: no se almacena; se sirve desde Lycet (POST /invoice/pdf).
	if kind == "pdf" {
		pdfBytes, pdfErr := svc.GetInvoicePDFContent(uint(saleID))
		if pdfErr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": pdfErr.Error()})
		}
		if len(pdfBytes) > 0 {
			c.Set(fiber.HeaderContentType, "application/pdf")
			c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`inline; filename="%s"`, filename))
			return c.Send(pdfBytes)
		}
	}

	// XML generado (firmado) sin envío a SUNAT: se obtiene de Lycet (POST /invoice/xml), no se almacena.
	if kind == "xml-generated" {
		xmlBytes, xmlErr := svc.GetInvoiceXMLGeneratedContent(uint(saleID))
		if xmlErr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": xmlErr.Error()})
		}
		if len(xmlBytes) > 0 {
			c.Set(fiber.HeaderContentType, "text/xml; charset=utf-8")
			c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
			return c.Send(xmlBytes)
		}
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "XML generado no disponible"})
	}

	// XML enviado a SUNAT y CDR: disco tenant, URL fiscal o proxy al facturador (SSOT).
	if kind == "xml" || kind == "cdr" {
		data, contentType, docErr := svc.GetInvoiceDocumentContent(uint(saleID), kind)
		if docErr != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": docErr.Error()})
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Set(fiber.HeaderContentType, contentType)
		c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
		return c.Send(data)
	}

	fullPath, err := svc.GetInvoiceDocumentPath(uint(saleID), kind)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if fullPath == "" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Documento no disponible"})
	}
	if kind == "pdf" {
		c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`inline; filename="%s"`, filename))
	}
	return c.SendFile(fullPath)
}

// --- Resúmenes diarios y Comunicaciones de baja ---

// ListSummariesAPI GET /billing/summaries
func (h *BillingHandler) ListSummariesAPI(c fiber.Ctx) error {
	// El resumen diario es un documento del RUC completo (incluye las boletas de todas las
	// sucursales) y no tiene sucursal propia: solo lo ven/emiten quienes ven todas las sucursales.
	if !branchguard.CoversAllBranches(c, db(c)) {
		return c.JSON(fiber.Map{"summaries": []any{}})
	}
	svc := billingSvc(c)
	list, err := svc.ListSummaries()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"summaries": list})
}

// CreateSummaryAPI POST /billing/summaries — body: { "fec_resumen": "2026-03-05" }
func (h *BillingHandler) CreateSummaryAPI(c fiber.Ctx) error {
	var body struct {
		FecResumen string `json:"fec_resumen"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "fec_resumen requerido (YYYY-MM-DD)"})
	}
	fecResumen, err := parseDate(body.FecResumen)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "fec_resumen inválido (use YYYY-MM-DD)"})
	}
	if !branchguard.CoversAllBranches(c, db(c)) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "el resumen diario incluye todas las sucursales: solo lo puede emitir quien accede a todas",
			"code":  branch.CodeBranchForbidden,
		})
	}
	svc := billingSvc(c)
	rec, err := svc.CreateSummary(fecResumen)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "summary": rec})
}

// GetSummaryStatusAPI GET /billing/summaries/:id/status
func (h *BillingHandler) GetSummaryStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if !branchguard.CoversAllBranches(c, db(c)) {
		return notFound(c, "no encontrado")
	}
	svc := billingSvc(c)
	rec, err := svc.GetSummaryStatus(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(rec)
}

// ListVoidedAPI GET /billing/voided
func (h *BillingHandler) ListVoidedAPI(c fiber.Ctx) error {
	svc := billingSvc(c)
	list, err := svc.ListVoided()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// Sin branch_id propio: la sucursal de una baja es la de los comprobantes que da de baja.
	scope := listScope(c)
	if scope != 0 {
		kept := make([]database.TenantSunatVoided, 0, len(list))
		for _, v := range list {
			if branchguard.LinesInScope(db(c), v.PayloadJSON, scope) {
				kept = append(kept, v)
			}
		}
		list = kept
	}
	return c.JSON(fiber.Map{"voided": list})
}

// CreateVoidedAPI POST /billing/voided — body: { "details": [ { "tipo_doc", "serie", "correlativo", "des_motivo_baja" } ] }
func (h *BillingHandler) CreateVoidedAPI(c fiber.Ctx) error {
	var body struct {
		Details []service.CreateVoidedInput `json:"details"`
	}
	if err := c.Bind().JSON(&body); err != nil || len(body.Details) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "details requerido (array con al menos un comprobante)"})
	}
	// Cada comprobante a dar de baja debe ser de la sucursal activa (se resuelve por sale_id o por
	// serie/correlativo, igual que el servicio).
	for _, d := range body.Details {
		if d.SaleID > 0 {
			if err := branchguard.SaleWritable(c, db(c), d.SaleID); err != nil {
				return notFound(c, "comprobante no encontrado")
			}
			continue
		}
		if !branchguard.LineWritable(c, db(c), d.TipoDoc, d.Serie, d.Correlativo) {
			return notFound(c, "comprobante no encontrado")
		}
	}
	svc := billingSvc(c)
	rec, err := svc.CreateVoided(body.Details)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "voided": rec})
}

// GetVoidedStatusAPI GET /billing/voided/:id/status
func (h *BillingHandler) GetVoidedStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var vrec database.TenantSunatVoided
	if db(c).First(&vrec, uint(id)).Error != nil || !branchguard.LinesReadable(c, db(c), vrec.PayloadJSON) {
		return notFound(c, "no encontrado")
	}
	svc := billingSvc(c)
	rec, err := svc.GetVoidedStatus(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(rec)
}

// NotificationCountsAPI GET /billing/notification-counts — cantidades de comprobantes electrónicos por estado (para campanita del header).
func (h *BillingHandler) NotificationCountsAPI(c fiber.Ctx) error {
	svc := billingSvc(c)
	// La campanita muestra solo la sucursal activa; quien ve todas las sucursales, el total.
	pending, errorCount, rejected, err := svc.GetNotificationCountsForBranch(branch.ResolveReportBranchFilter(c, 0, true))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"pending":  pending,
		"error":    errorCount,
		"rejected": rejected,
	})
}

// ConsultInvoiceStatusAPI GET /billing/invoice-status?tipo=01&serie=F001&numero=1
func (h *BillingHandler) ConsultInvoiceStatusAPI(c fiber.Ctx) error {
	tipo := c.Query("tipo")
	serie := c.Query("serie")
	numero := c.Query("numero")
	if tipo == "" || serie == "" || numero == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "tipo, serie y numero son requeridos"})
	}
	// Si el comprobante existe localmente, debe ser legible desde la sucursal del usuario.
	if b, found := branchguard.DocBranch(db(c), tipo, serie, numero); found && !branch.RecordReadable(c, b) {
		return notFound(c, "comprobante no encontrado")
	}
	svc := billingSvc(c)
	result, err := svc.ConsultInvoiceStatus(tipo, serie, numero)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(result)
}

func parseDate(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, time.Local)
}

// --- Guías de remisión ---

func (h *BillingHandler) ListDespatchesAPI(c fiber.Ctx) error {
	svc := billingSvc(c)
	list, err := svc.ListDespatches()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// Antes devolvía las guías de todas las sucursales.
	if scope := listScope(c); scope != 0 {
		kept := make([]service.DespatchListItem, 0, len(list))
		for _, d := range list {
			if branchguard.InScope(scope, d.BranchID) {
				kept = append(kept, d)
			}
		}
		list = kept
	}
	return c.JSON(fiber.Map{"despatches": list})
}

func (h *BillingHandler) CreateDespatchAPI(c fiber.Ctx) error {
	var input service.CreateDespatchInput
	if err := c.Bind().JSON(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "payload inválido"})
	}
	// branch_id y serie vienen del body: la sucursal debe ser accesible (la serie la valida el
	// servicio contra esa sucursal) y la venta origen, de esa misma sucursal y de la activa.
	branchID, err := branch.ResolveWriteBranchID(c, input.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	input.BranchID = branchID
	if input.SourceSaleID != nil && *input.SourceSaleID > 0 {
		if err := branchguard.SaleBranchMatches(c, db(c), *input.SourceSaleID, branchID); err != nil {
			return notFound(c, "venta origen no encontrada")
		}
	}
	svc := billingSvc(c)
	rec, err := svc.CreateAndSendDespatch(input)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "async": true, "message": "Guía encolada para emisión SUNAT", "despatch": rec})
}

func (h *BillingHandler) LastDespatchTransportAPI(c fiber.Ctx) error {
	branchID, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	// Nunca "todas": 0 = sucursal activa; otra solo si el usuario accede a ella.
	return c.JSON(billingSvc(c).GetLastDespatchTransport(branch.ResolveReadBranchFilter(c, uint(branchID)), strings.TrimSpace(c.Query("tipo"))))
}

func (h *BillingHandler) LookupDespatchAPI(c fiber.Ctx) error {
	if !branchguard.DespatchNumberReadable(c, db(c), c.Query("number")) {
		return notFound(c, "guía no encontrada")
	}
	res, err := billingSvc(c).LookupOwnDespatch(c.Query("number"))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}

func (h *BillingHandler) GetDespatchStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := branchguard.DespatchReadable(c, db(c), uint(id)); err != nil {
		return notFound(c, "no encontrado")
	}
	svc := billingSvc(c)
	rec, err := svc.GetDespatchStatus(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// Mismo shape que el listado: si se devolviera el registro crudo, el refresco de estado
	// borraría billing_status/guia_sunat_code en la fila del frontend (el listado no vuelve a
	// pedirlos), y la guía se caería de su vista dedicada (remitente/transportista).
	item, err := svc.GetDespatchListItem(*rec)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(item)
}

// --- Retención ---

func parseFiscalAuxListParams(c fiber.Ctx) service.FiscalAuxListParams {
	purchaseID, _ := strconv.ParseUint(c.Query("purchase_id"), 10, 32)
	sourceSaleID, _ := strconv.ParseUint(c.Query("source_sale_id"), 10, 32)
	var from, to *time.Time
	if f := strings.TrimSpace(c.Query("from")); f != "" {
		if t, err := time.ParseInLocation("2006-01-02", f, time.Local); err == nil {
			start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
			from = &start
		}
	}
	if t := strings.TrimSpace(c.Query("to")); t != "" {
		if ts, err := time.ParseInLocation("2006-01-02", t, time.Local); err == nil {
			end := time.Date(ts.Year(), ts.Month(), ts.Day(), 23, 59, 59, 999999999, time.Local)
			to = &end
		}
	}
	return service.FiscalAuxListParams{
		Q:             c.Query("q"),
		Status:        c.Query("status"),
		BillingStatus: c.Query("billing_status"),
		Serie:         c.Query("serie"),
		Correlativo:   c.Query("correlativo"),
		PurchaseID:    uint(purchaseID),
		SourceSaleID:  uint(sourceSaleID),
		From:          from,
		To:            to,
	}
}

func (h *BillingHandler) ListRetentionsAPI(c fiber.Ctx) error {
	svc := billingSvc(c)
	list, err := svc.ListRetentionsFiltered(parseFiscalAuxListParams(c))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// Sin branch_id propio: la sucursal sale de la compra origen o de la serie.
	if scope := listScope(c); scope != 0 {
		kept := make([]service.RetentionListItem, 0, len(list))
		for _, r := range list {
			if branchguard.InScope(scope, branchguard.RetentionBranch(db(c), r.TenantRetention)) {
				kept = append(kept, r)
			}
		}
		list = kept
	}
	return c.JSON(fiber.Map{"retentions": list})
}

func (h *BillingHandler) CreateRetentionAPI(c fiber.Ctx) error {
	var input service.CreateRetentionInput
	if err := c.Bind().JSON(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "payload inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, input.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	input.BranchID = branchID
	if input.SourcePurchaseID != nil && *input.SourcePurchaseID > 0 {
		if err := branchguard.PurchaseBranchMatches(c, db(c), *input.SourcePurchaseID, branchID); err != nil {
			return notFound(c, "compra origen no encontrada")
		}
	}
	svc := billingSvc(c)
	rec, err := svc.CreateAndSendRetention(input)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "async": true, "message": "Retención encolada para emisión SUNAT", "retention": rec})
}

func (h *BillingHandler) GetRetentionStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := branchguard.RetentionReadable(c, db(c), uint(id)); err != nil {
		return notFound(c, "no encontrado")
	}
	svc := billingSvc(c)
	rec, err := svc.GetRetentionStatus(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(rec)
}

// --- Percepción ---

func (h *BillingHandler) ListPerceptionsAPI(c fiber.Ctx) error {
	svc := billingSvc(c)
	list, err := svc.ListPerceptionsFiltered(parseFiscalAuxListParams(c))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if scope := listScope(c); scope != 0 {
		kept := make([]service.PerceptionListItem, 0, len(list))
		for _, p := range list {
			if branchguard.InScope(scope, branchguard.PerceptionBranch(db(c), p.TenantPerception)) {
				kept = append(kept, p)
			}
		}
		list = kept
	}
	return c.JSON(fiber.Map{"perceptions": list})
}

func (h *BillingHandler) CreatePerceptionAPI(c fiber.Ctx) error {
	var input service.CreatePerceptionInput
	if err := c.Bind().JSON(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "payload inválido"})
	}
	branchID, err := branch.ResolveWriteBranchID(c, input.BranchID)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	input.BranchID = branchID
	if input.SourceSaleID != nil && *input.SourceSaleID > 0 {
		if err := branchguard.SaleBranchMatches(c, db(c), *input.SourceSaleID, branchID); err != nil {
			return notFound(c, "venta origen no encontrada")
		}
	}
	svc := billingSvc(c)
	rec, err := svc.CreateAndSendPerception(input)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "async": true, "message": "Percepción encolada para emisión SUNAT", "perception": rec})
}

func (h *BillingHandler) GetPerceptionStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := branchguard.PerceptionReadable(c, db(c), uint(id)); err != nil {
		return notFound(c, "no encontrado")
	}
	svc := billingSvc(c)
	rec, err := svc.GetPerceptionStatus(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(rec)
}

// --- Reversión ---

func (h *BillingHandler) ListReversionsAPI(c fiber.Ctx) error {
	svc := billingSvc(c)
	list, err := svc.ListReversionsFiltered(parseFiscalAuxListParams(c))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if scope := listScope(c); scope != 0 {
		kept := make([]service.ReversionListItem, 0, len(list))
		for _, r := range list {
			if branchguard.LinesInScope(db(c), r.PayloadJSON, scope) {
				kept = append(kept, r)
			}
		}
		list = kept
	}
	return c.JSON(fiber.Map{"reversions": list})
}

func (h *BillingHandler) CreateReversionAPI(c fiber.Ctx) error {
	var body struct {
		Details []service.CreateVoidedInput `json:"details"`
	}
	if err := c.Bind().JSON(&body); err != nil || len(body.Details) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "details requerido (array con al menos un comprobante)"})
	}
	for _, d := range body.Details {
		if !branchguard.LineWritable(c, db(c), d.TipoDoc, d.Serie, d.Correlativo) {
			return notFound(c, "comprobante no encontrado")
		}
	}
	svc := billingSvc(c)
	rec, err := svc.CreateReversion(body.Details)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "reversion": rec})
}

func (h *BillingHandler) GetReversionStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var rrec database.TenantSunatReversion
	if db(c).First(&rrec, uint(id)).Error != nil || !branchguard.LinesReadable(c, db(c), rrec.PayloadJSON) {
		return notFound(c, "no encontrado")
	}
	svc := billingSvc(c)
	rec, err := svc.GetReversionStatus(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(rec)
}

func (h *BillingHandler) GetRetentionPrintAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := branchguard.RetentionReadable(c, db(c), uint(id)); err != nil {
		return notFound(c, "no encontrado")
	}
	rec, err := billingSvc(c).GetRetentionPrintData(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(rec)
}

func (h *BillingHandler) GetPerceptionPrintAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := branchguard.PerceptionReadable(c, db(c), uint(id)); err != nil {
		return notFound(c, "no encontrado")
	}
	rec, err := billingSvc(c).GetPerceptionPrintData(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(rec)
}
