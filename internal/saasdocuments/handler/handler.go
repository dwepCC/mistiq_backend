package handler

import (
	"fmt"
	"strconv"

	"tukifac/pkg/database"
	"tukifac/pkg/saas/docusage"

	"github.com/gofiber/fiber/v3"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) ListCatalogAPI(c fiber.Ctx) error {
	rows, err := docusage.ListCatalogAdmin()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"packages": rows})
}

func (h *Handler) UpsertCatalogAPI(c fiber.Ctx) error {
	var in docusage.UpsertPackageInput
	if err := c.Bind().Body(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	if id, err := strconv.ParseUint(c.Params("id"), 10, 32); err == nil && id > 0 {
		in.ID = uint(id)
	}
	creating := in.ID == 0
	row, err := docusage.UpsertCatalogPackage(in)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	saUserID, _ := c.Locals("sa_user_id").(uint)
	action := "document_package_catalog_updated"
	if creating {
		action = "document_package_catalog_created"
	}
	database.WriteAuditLog(&database.AuditLog{
		UserID:    saUserID,
		Action:    action,
		Entity:    "saas_document_package",
		EntityID:  row.ID,
		Payload:   fmt.Sprintf(`{"name":%q,"documents_qty":%d,"price":%v}`, row.Name, row.DocumentsQty, row.Price),
		IPAddress: c.IP(),
	})

	return c.JSON(row)
}

// DeleteCatalogAPI desactiva un paquete del catálogo (is_active=false) — no es un borrado físico,
// no hay ninguna fila destructiva que eliminar en este flujo.
func (h *Handler) DeleteCatalogAPI(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 32)
	if err := database.CentralDB.Model(&database.SaasDocumentPackage{}).Where("id = ?", id).
		Update("is_active", false).Error; err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		UserID:    saUserID,
		Action:    "document_package_catalog_deactivated",
		Entity:    "saas_document_package",
		EntityID:  uint(id),
		IPAddress: c.IP(),
	})

	return c.JSON(fiber.Map{"success": true})
}

func (h *Handler) ListPendingAPI(c fiber.Ctx) error {
	rows, err := docusage.ListPendingPackages()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	type rowView struct {
		database.SaasTenantDocumentPackage
		TenantName  string `json:"tenant_name"`
		PackageName string `json:"package_name"`
	}
	out := make([]rowView, 0, len(rows))
	for _, r := range rows {
		v := rowView{SaasTenantDocumentPackage: r}
		var t database.Tenant
		if database.CentralDB.First(&t, r.TenantID).Error == nil {
			v.TenantName = t.Name
		}
		var p database.SaasDocumentPackage
		if database.CentralDB.First(&p, r.PackageID).Error == nil {
			v.PackageName = p.Name
		}
		out = append(out, v)
	}
	return c.JSON(fiber.Map{"requests": out})
}

// ApproveAPI aprueba una solicitud de compra de paquete de documentos — efecto financiero real:
// acredita los documentos comprados (ver docusage.ApproveTenantPackage). La validación de negocio
// existente (la solicitud debe seguir en pending_review) sigue intacta, RBAC no la reemplaza.
func (h *Handler) ApproveAPI(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 32)
	// Corregido: leía "superadmin_id", una clave que SuperAdminAuthAPI nunca setea (el middleware
	// deja "sa_user_id") — el actor auditado quedaba siempre en 0. Mismo patrón que el resto del
	// panel central.
	saUserID, _ := c.Locals("sa_user_id").(uint)
	var body struct {
		Notes string `json:"notes"`
	}
	_ = c.Bind().Body(&body)
	if err := docusage.ApproveTenantPackage(uint(id), saUserID, body.Notes); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	database.WriteAuditLog(&database.AuditLog{
		UserID:    saUserID,
		Action:    "document_package_purchase_approved",
		Entity:    "saas_tenant_document_package",
		EntityID:  uint(id),
		Payload:   fmt.Sprintf(`{"to":%q}`, database.SaasDocPkgApproved),
		IPAddress: c.IP(),
	})

	return c.JSON(fiber.Map{"success": true})
}

// RejectAPI rechaza una solicitud de compra — requiere documentos.approve_purchase (decisión
// confirmada con el usuario: aprobar/rechazar son las dos caras de la misma revisión; el catálogo
// no tiene un permiso de rechazo separado).
func (h *Handler) RejectAPI(c fiber.Ctx) error {
	id, _ := strconv.ParseUint(c.Params("id"), 10, 32)
	saUserID, _ := c.Locals("sa_user_id").(uint)
	var body struct {
		Reason string `json:"reason"`
	}
	if err := c.Bind().Body(&body); err != nil || body.Reason == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "motivo requerido"})
	}
	if err := docusage.RejectTenantPackage(uint(id), saUserID, body.Reason); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	database.WriteAuditLog(&database.AuditLog{
		UserID:    saUserID,
		Action:    "document_package_purchase_rejected",
		Entity:    "saas_tenant_document_package",
		EntityID:  uint(id),
		Payload:   fmt.Sprintf(`{"to":%q,"reason":%q}`, database.SaasDocPkgRejected, body.Reason),
		IPAddress: c.IP(),
	})

	return c.JSON(fiber.Map{"success": true})
}
