package saasdocuments

import (
	"tukifac/internal/saasdocuments/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(saAPI fiber.Router) {
	h := handler.NewHandler()
	g := saAPI.Group("/document-packages")
	g.Get("/", middleware.RequireSAPermission("documentos.view"), h.ListCatalogAPI)
	// Catálogo (crear/editar/desactivar paquetes): documentos.manage — nunca implica
	// approve_purchase (allowlist ya vigente, saManageImpliedActions solo concede "view" para
	// este módulo, ver pkg/middleware/sa_permissions.go). DeleteCatalogAPI solo desactiva
	// (is_active=false), no hay borrado físico que requiera un permiso más específico.
	g.Post("/", middleware.RequireSAPermission("documentos.manage"), h.UpsertCatalogAPI)
	g.Put("/:id", middleware.RequireSAPermission("documentos.manage"), h.UpsertCatalogAPI)
	g.Delete("/:id", middleware.RequireSAPermission("documentos.manage"), h.DeleteCatalogAPI)
	g.Get("/purchases/pending", middleware.RequireSAPermission("documentos.view"), h.ListPendingAPI)
	// approve/reject de una compra: documentos.approve_purchase — confirmado con el usuario que
	// reject reutiliza el mismo permiso (el catálogo no distingue rechazo de aprobación), y que
	// NUNCA se otorga solo por tener documentos.manage (efecto financiero real: acredita
	// documentos pagados).
	g.Patch("/purchases/:id/approve", middleware.RequireSAPermission("documentos.approve_purchase"), h.ApproveAPI)
	g.Patch("/purchases/:id/reject", middleware.RequireSAPermission("documentos.approve_purchase"), h.RejectAPI)
}
