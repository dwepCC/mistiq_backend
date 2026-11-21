package billing

import (
	"tukifac/internal/billing/handler"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registra las rutas de facturación electrónica.
//
// Antes solo exigían el módulo del plan, luego se corrigió a un único permiso "billing.send" para
// TODO (enviar, anular con nota de crédito, nota de débito, guías, retenciones, percepciones,
// reversiones). Ahora cada tipo de documento tiene su propio permiso — billing.manage lo sigue
// concediendo todo (implica el resto vía "{modulo}.manage") — para poder, por ejemplo, dar acceso
// a Guías de remisión sin dar acceso a anular comprobantes con nota de crédito.
//
// send/resend/status/invoice/void-con-nota-de-crédito usan RequireBillingAccess (puente a staff de
// restaurante, o.ch/o.cx) porque Tukichef los usa para cobrar una mesa
// (frontend_restaurant_tenant/src/services/billing.service.ts) — regresión real detectada en
// producción con un permiso plano sin puente. debit_note/despatch/advanced_docs quedan sin puente:
// confirmado que Tukichef no los usa.
// /reissue sigue reservada a soporte (RequireMasterAccess), fuera del catálogo de roles del tenant.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewBillingHandler()
	mod := middleware.RequireModule("billing")
	loadRest := middleware.LoadRestaurantPermissions()
	send := middleware.RequireBillingAccess("billing.send", "send")
	// Solo lectura (lista, estado, descargas): billing.view, o billing.send que ya lo implicaba. Permite dar
	// "ver comprobantes" a un rol sin darle la capacidad de enviar/reenviar a SUNAT.
	view := middleware.RequireBillingAccess("billing.view", "send", "billing.send")
	creditNote := middleware.RequireBillingAccess("billing.credit_note", "credit_note")
	debitNote := middleware.RequirePermission("billing.debit_note")
	anyNote := middleware.RequireAnyPermission("billing.credit_note", "billing.debit_note")
	despatch := middleware.RequirePermission("billing.despatch")
	despatchMod := middleware.RequireModule(database.ModuleDespatch)
	advancedMod := middleware.RequireModule(database.ModuleAdvancedDocs)
	advancedDocs := middleware.RequirePermission("billing.advanced_docs")

	api.Post("/billing/send/:saleId", mod, loadRest, send, h.SendToSUNAT)
	api.Get("/billing/status/:saleId", mod, loadRest, view, h.GetBillingStatus)
	api.Get("/billing/events", mod, loadRest, view, h.BillingEventsSSE)
	api.Get("/billing/job/:saleId", mod, loadRest, view, h.GetBillingJobStatus)
	api.Post("/billing/resend/:saleId", mod, loadRest, send, h.ResendToSUNAT)
	// Corrección fiscal: reenvía con otra fecha de emisión aunque el comprobante
	// ya tenga aceptación. Reservada a soporte por acceso maestro (auditable).
	api.Post("/billing/reissue/:saleId",
		mod,
		middleware.RequireMasterAccess(),
		h.ReissueToSUNAT,
	)
	api.Post("/billing/void-with-credit-note/:saleId", mod, loadRest, creditNote, h.VoidWithCreditNoteAPI)
	api.Post("/billing/debit-notes/:saleId", mod, debitNote, h.CreateDebitNoteAPI)
	// Nota de crédito/débito independiente (Fase 3): sin venta local, documento afectado a mano.
	// Un solo endpoint sirve ambos tipos según el body — exige cualquiera de los dos permisos.
	api.Post("/billing/notes/independent", mod, anyNote, h.CreateIndependentNoteAPI)
	api.Get("/billing/invoice/:saleId", mod, loadRest, view, h.GetInvoiceAPI)
	api.Get("/billing/invoice/:saleId/document/:kind", mod, loadRest, view, h.GetInvoiceDocumentAPI)
	// Resúmenes diarios y comunicaciones de baja
	api.Get("/billing/summaries", mod, view, h.ListSummariesAPI)
	api.Post("/billing/summaries", mod, send, h.CreateSummaryAPI)
	api.Get("/billing/summaries/:id/status", mod, view, h.GetSummaryStatusAPI)
	api.Get("/billing/voided", mod, view, h.ListVoidedAPI)
	api.Post("/billing/voided", mod, send, h.CreateVoidedAPI)
	api.Get("/billing/voided/:id/status", mod, view, h.GetVoidedStatusAPI)
	api.Get("/billing/notification-counts", mod, view, h.NotificationCountsAPI)
	api.Get("/billing/invoice-status", mod, send, h.ConsultInvoiceStatusAPI)
	// Guías de remisión
	api.Get("/billing/despatches", mod, despatchMod, despatch, h.ListDespatchesAPI)
	api.Get("/billing/despatches/last-transport", mod, despatchMod, despatch, h.LastDespatchTransportAPI)
	api.Get("/billing/despatches/lookup", mod, despatchMod, despatch, h.LookupDespatchAPI)
	api.Post("/billing/despatches", mod, despatchMod, despatch, h.CreateDespatchAPI)
	api.Get("/billing/despatches/:id/status", mod, despatchMod, despatch, h.GetDespatchStatusAPI)
	// Documentos avanzados: retención, percepción, reversión
	api.Get("/billing/retentions", mod, advancedMod, advancedDocs, h.ListRetentionsAPI)
	api.Post("/billing/retentions", mod, advancedMod, advancedDocs, h.CreateRetentionAPI)
	api.Get("/billing/retentions/:id/status", mod, advancedMod, advancedDocs, h.GetRetentionStatusAPI)
	api.Get("/billing/retentions/:id/print", mod, advancedMod, advancedDocs, h.GetRetentionPrintAPI)
	api.Get("/billing/perceptions", mod, advancedMod, advancedDocs, h.ListPerceptionsAPI)
	api.Post("/billing/perceptions", mod, advancedMod, advancedDocs, h.CreatePerceptionAPI)
	api.Get("/billing/perceptions/:id/status", mod, advancedMod, advancedDocs, h.GetPerceptionStatusAPI)
	api.Get("/billing/perceptions/:id/print", mod, advancedMod, advancedDocs, h.GetPerceptionPrintAPI)
	api.Get("/billing/reversions", mod, advancedMod, advancedDocs, h.ListReversionsAPI)
	api.Post("/billing/reversions", mod, advancedMod, advancedDocs, h.CreateReversionAPI)
	api.Get("/billing/reversions/:id/status", mod, advancedMod, advancedDocs, h.GetReversionStatusAPI)
}
