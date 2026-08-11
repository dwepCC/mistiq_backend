package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tukifac/internal/payments/service"
	"tukifac/pkg/database"
	"tukifac/pkg/saas"
	"tukifac/pkg/tenantstorage"
	"tukifac/pkg/uploadlimits"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type PaymentHandler struct {
	svc *service.PaymentService
}

func NewPaymentHandler() *PaymentHandler {
	return &PaymentHandler{svc: service.NewPaymentService()}
}

// GET /api/superadmin/payments?status=
func (h *PaymentHandler) ListAPI(c fiber.Ctx) error {
	payments, err := h.svc.List(c.Query("status"))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": payments})
}

// GET /api/superadmin/payments/:id
func (h *PaymentHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	p, err := h.svc.GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(p)
}

// POST /api/superadmin/payments  (multipart/form-data con receipt opcional)
func (h *PaymentHandler) CreateAPI(c fiber.Ctx) error {
	tenantID, _ := strconv.ParseUint(c.FormValue("tenant_id"), 10, 32)
	amount, _ := strconv.ParseFloat(c.FormValue("amount"), 64)
	months, _ := strconv.Atoi(c.FormValue("period_months"))

	cycleID, _ := strconv.ParseUint(c.FormValue("billing_cycle_id"), 10, 32)
	saUserID, _ := c.Locals("sa_user_id").(uint)

	input := service.CreatePaymentInput{
		TenantID:       uint(tenantID),
		Amount:         amount,
		Currency:       c.FormValue("currency"),
		PeriodMonths:   months,
		Notes:          c.FormValue("notes"),
		PaymentMethod:  c.FormValue("payment_method"),
		BillingCycleID: uint(cycleID),
		ReviewedBy:     saUserID,
	}

	// Subida de comprobante
	file, err := c.FormFile("receipt")
	if err == nil && file != nil {
		ext := strings.ToLower(filepath.Ext(file.Filename))
		allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".pdf": true, ".webp": true}
		if !allowed[ext] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "formato de archivo no permitido"})
		}
		if file.Size > uploadlimits.MaxFileBytes {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "el comprobante no debe superar 10 MB"})
		}
		ruc, rucErr := tenantstorage.TenantRUCFromID(uint(tenantID))
		if rucErr != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": rucErr.Error()})
		}
		filename := fmt.Sprintf("%s_%d%s", uuid.New().String(), time.Now().Unix(), ext)
		dir := tenantstorage.TenantUploadDir(ruc, "receipts")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo crear la carpeta de comprobantes"})
		}
		savePath := filepath.Join(dir, filename)
		if err := c.SaveFile(file, savePath); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error guardando comprobante"})
		}
		input.ReceiptURL = tenantstorage.TenantUploadPublicURL(ruc, "receipts", filename)
	}

	payment, err := h.svc.Create(input)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": payment})
}

// PATCH /api/superadmin/payments/:id/approve
func (h *PaymentHandler) ApproveAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		PlanID     uint   `json:"plan_id"`
		AdminNotes string `json:"admin_notes"`
	}
	c.Bind().JSON(&body)

	reviewerID, _ := c.Locals("sa_user_id").(uint)
	if err := h.svc.Approve(uint(id), service.ApproveInput{
		PlanID:     body.PlanID,
		AdminNotes: body.AdminNotes,
		ReviewerID: reviewerID,
	}); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PATCH /api/superadmin/payments/:id/reject
func (h *PaymentHandler) RejectAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		AdminNotes string `json:"admin_notes"`
	}
	c.Bind().JSON(&body)

	reviewerID, _ := c.Locals("sa_user_id").(uint)
	if err := h.svc.Reject(uint(id), body.AdminNotes, reviewerID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/superadmin/payments/:id/fiscal-document — sube la boleta/factura del pago.
//
// Es el comprobante que la empresa entrega AL cliente por su pago de suscripción, distinto
// del voucher que el cliente subió. Se guarda y el tenant lo descarga desde ese mismo pago.
func (h *PaymentHandler) UploadFiscalDocAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}

	detail, err := h.svc.GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "pago no encontrado"})
	}
	if detail.Status != database.SaasPayApproved {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "solo se puede adjuntar el comprobante a un pago aprobado",
		})
	}

	file, err := c.FormFile("document")
	if err != nil || file == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "adjunte el comprobante"})
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	// Solo PDF: es el documento que el cliente descarga e imprime.
	if ext != ".pdf" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "el comprobante debe ser un PDF"})
	}
	if file.Size > uploadlimits.MaxFileBytes {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "el comprobante no debe superar 10 MB"})
	}

	ruc, rucErr := tenantstorage.TenantRUCFromID(detail.TenantID)
	if rucErr != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": rucErr.Error()})
	}
	filename := fmt.Sprintf("%s_%d%s", uuid.New().String(), time.Now().Unix(), ext)
	dir := tenantstorage.TenantUploadDir(ruc, "fiscal_docs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo crear la carpeta"})
	}
	if err := c.SaveFile(file, filepath.Join(dir, filename)); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error guardando el comprobante"})
	}

	url := tenantstorage.TenantUploadPublicURL(ruc, "fiscal_docs", filename)
	if err := h.svc.SetFiscalDoc(uint(id), url); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "fiscal_doc_url": url})
}

// CollectionAlertsAPI GET /api/superadmin/payments/alerts — campana de cobranza del panel.
func (h *PaymentHandler) CollectionAlertsAPI(c fiber.Ctx) error {
	alerts, err := saas.GetCollectionAlerts()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(alerts)
}
