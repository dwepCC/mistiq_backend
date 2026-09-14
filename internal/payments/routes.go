package payments

import (
	"tukifac/internal/payments/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(saAPI fiber.Router) {
	h := handler.NewPaymentHandler()

	// Fase 5 (etapa 1 — solo lectura): ver comentario en internal/superadmin/routes.go.
	saAPI.Get("/payments", middleware.RequireSAPermission("pagos.view"), h.ListAPI)
	saAPI.Get("/payments/alerts", middleware.RequireSAPermission("pagos.view"), h.CollectionAlertsAPI)
	saAPI.Get("/payments/:id", middleware.RequireSAPermission("pagos.view"), h.GetAPI)
	// Fase 5 (etapa 3, Grupo 2 — pagos).
	//
	// CreateAPI (POST /payments) NO es un registro operativo neutro: internamente llama
	// saas.SubmitPayment + saas.ApprovePayment en el mismo paso (ver PaymentService.Create) — un
	// pago creado por un admin queda aprobado de inmediato (extiende suscripción, marca el ciclo
	// pagado, sincroniza módulos). Mismo efecto real que approve, así que exige el mismo permiso
	// — confirmado con el usuario antes de asignarlo, no es una inferencia automática.
	saAPI.Post("/payments", middleware.RequireSAPermission("pagos.approve"), h.CreateAPI)
	saAPI.Patch("/payments/:id/approve", middleware.RequireSAPermission("pagos.approve"), h.ApproveAPI)
	// reject NO se deriva de approve — permiso independiente, tal como exige el catálogo.
	saAPI.Patch("/payments/:id/reject", middleware.RequireSAPermission("pagos.reject"), h.RejectAPI)
	// revert/refund: revierte una aprobación existente — el más sensible de los 4, permiso propio,
	// no se deriva de approve ni de view.
	saAPI.Patch("/payments/:id/revert", middleware.RequireSAPermission("pagos.refund"), h.RevertAPI)
	// UploadFiscalDocAPI solo opera sobre pagos ya aprobados (el propio handler lo valida) —
	// continuación del flujo de aprobación, no un permiso nuevo (confirmado con el usuario).
	saAPI.Post("/payments/:id/fiscal-document", middleware.RequireSAPermission("pagos.approve"), h.UploadFiscalDocAPI)
}
