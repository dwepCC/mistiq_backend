package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/saas"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/uploadlimits"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type Handler struct{}

func New() *Handler { return &Handler{} }

func mapUsageToHub(u docusage.DocumentUsageView) saas.DocumentUsageHubView {
	return saas.DocumentUsageHubView{
		IsUnlimited: u.IsUnlimited, PlanLimit: u.PlanLimit, PlanUsed: u.PlanUsed,
		PlanRemaining: u.PlanRemaining, PackageBonus: u.PackageBonus, PackageUsed: u.PackageUsed,
		PackageRemaining: u.PackageRemaining, TotalAvailable: u.TotalAvailable,
		TotalConsumed: u.TotalConsumed, UsagePercent: u.UsagePercent, WarningLevel: u.WarningLevel,
		WarningMessage: u.WarningMessage, CanEmit: u.CanEmit, BillingCycleEnd: u.BillingCycleEnd,
		QuotaPeriodEnd: u.QuotaPeriodEnd, QuotaPeriodIndex: u.QuotaPeriodIndex,
		QuotaPeriodTotal: u.QuotaPeriodTotal,
	}
}

func mapCatalogToHub(rows []docusage.CatalogPackageView) []saas.CatalogPackageHubView {
	out := make([]saas.CatalogPackageHubView, 0, len(rows))
	for _, r := range rows {
		out = append(out, saas.CatalogPackageHubView{
			ID: r.ID, Name: r.Name, Description: r.Description,
			DocumentsQty: r.DocumentsQty, Price: r.Price, Currency: r.Currency,
		})
	}
	return out
}

func tenantID(c fiber.Ctx) uint {
	t, _ := c.Locals("tenant").(*database.Tenant)
	if t != nil {
		return t.ID
	}
	return 0
}

func userID(c fiber.Ctx) *uint {
	if id, ok := c.Locals("user_id").(uint); ok && id > 0 {
		return &id
	}
	return nil
}

// Summary GET /api/subscription/summary — hub único de cobro SaaS.
func (h *Handler) Summary(c fiber.Ctx) error {
	hub, err := saas.GetBillingHub(tenantID(c))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if usage, err := docusage.GetUsageView(tenantID(c)); err == nil {
		hub.Documents = mapUsageToHub(usage)
	}
	if catalog, err := docusage.ListActiveCatalogForHub(); err == nil {
		hub.DocumentPackages = mapCatalogToHub(catalog)
	}
	return c.JSON(hub)
}

// ListInvoices GET /api/subscription/invoices (retrocompat)
func (h *Handler) ListInvoices(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"invoices": saas.ListInvoicesView(tenantID(c))})
}

// ListPayments GET /api/subscription/payments (retrocompat)
func (h *Handler) ListPayments(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"payments": saas.ListPaymentsView(tenantID(c))})
}

// ListEvents GET /api/subscription/events
func (h *Handler) ListEvents(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"events": saas.ListTimelineEvents(tenantID(c))})
}

// SubmitPayment POST /api/subscription/payments (multipart)
func (h *Handler) SubmitPayment(c fiber.Ctx) error {
	tid := tenantID(c)
	billingCycleID, _ := strconv.ParseUint(c.FormValue("billing_cycle_id"), 10, 32)
	amount, _ := strconv.ParseFloat(c.FormValue("amount"), 64)
	method := strings.TrimSpace(c.FormValue("payment_method"))
	reference := strings.TrimSpace(c.FormValue("reference"))
	notes := c.FormValue("notes")

	var paymentDate *time.Time
	if pd := strings.TrimSpace(c.FormValue("payment_date")); pd != "" {
		if t, err := time.ParseInLocation("2006-01-02", pd, saas.LimaLocation()); err == nil {
			paymentDate = &t
		}
	}

	receiptURL := ""
	file, err := c.FormFile("receipt")
	if err == nil && file != nil {
		ext := strings.ToLower(filepath.Ext(file.Filename))
		allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".pdf": true, ".webp": true}
		if !allowed[ext] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "formato no permitido"})
		}
		if file.Size > uploadlimits.MaxFileBytes {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "archivo máximo 10 MB"})
		}
		dir := filepath.Join("storage", "saas", "receipts", fmt.Sprintf("tenant_%d", tid))
		_ = os.MkdirAll(dir, 0755)
		filename := fmt.Sprintf("%s_%d%s", uuid.New().String(), time.Now().Unix(), ext)
		savePath := filepath.Join(dir, filename)
		if err := c.SaveFile(file, savePath); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error guardando comprobante"})
		}
		receiptURL = "/" + strings.ReplaceAll(savePath, "\\", "/")
	} else {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "comprobante requerido"})
	}

	payment, err := saas.SubmitPayment(saas.SubmitPaymentInput{
		TenantID:       tid,
		BillingCycleID: uint(billingCycleID),
		Amount:         amount,
		PaymentMethod:  method,
		PaymentDate:    paymentDate,
		Reference:      reference,
		Notes:          notes,
		ReceiptURL:     receiptURL,
		SubmittedBy:    userID(c),
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	hub, _ := saas.GetBillingHub(tid)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"success": true,
		"message": "Pago enviado; pendiente de validación",
		"payment": payment,
		"hub":     hub,
	})
}

// ListDocumentPackages GET catálogo activo.
func (h *Handler) ListDocumentPackages(c fiber.Ctx) error {
	rows, err := docusage.ListActiveCatalog()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	usage, _ := docusage.GetUsageView(tenantID(c))
	return c.JSON(fiber.Map{"packages": rows, "usage": usage})
}

// PurchaseDocumentPackage POST compra paquete con comprobante.
func (h *Handler) PurchaseDocumentPackage(c fiber.Ctx) error {
	tid := tenantID(c)
	packageID, _ := strconv.ParseUint(c.FormValue("package_id"), 10, 32)
	amount, _ := strconv.ParseFloat(c.FormValue("amount"), 64)
	reference := strings.TrimSpace(c.FormValue("reference"))

	receiptURL := ""
	file, err := c.FormFile("receipt")
	if err == nil && file != nil {
		ext := strings.ToLower(filepath.Ext(file.Filename))
		allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".pdf": true, ".webp": true}
		if !allowed[ext] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "formato no permitido"})
		}
		if file.Size > uploadlimits.MaxFileBytes {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "archivo máximo 10 MB"})
		}
		dir := filepath.Join("storage", "saas", "doc_packages", fmt.Sprintf("tenant_%d", tid))
		_ = os.MkdirAll(dir, 0755)
		filename := fmt.Sprintf("%s_%d%s", uuid.New().String(), time.Now().Unix(), ext)
		savePath := filepath.Join(dir, filename)
		if err := c.SaveFile(file, savePath); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error guardando comprobante"})
		}
		receiptURL = "/" + strings.ReplaceAll(savePath, "\\", "/")
	} else {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "comprobante requerido"})
	}

	row, err := docusage.PurchaseDocumentPackage(docusage.PurchasePackageInput{
		TenantID: tid, PackageID: uint(packageID), Amount: amount,
		Reference: reference, ReceiptURL: receiptURL, SubmittedBy: userID(c),
	})
	if err != nil {
		code := fiber.StatusBadRequest
		if err == docusage.ErrUnlimitedCannotBuyPkg {
			code = fiber.StatusConflict
		}
		return c.Status(code).JSON(fiber.Map{"error": err.Error()})
	}
	usage, _ := docusage.GetUsageView(tid)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "purchase": row, "usage": usage})
}
