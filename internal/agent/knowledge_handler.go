package agent

import (
	"context"
	"strconv"
	"strings"

	"tukifac/pkg/agent/knowledge"
	knowledgemysql "tukifac/pkg/agent/knowledge/mysql"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

type knowledgeItemDTO struct {
	ID         uint   `json:"id"`
	SourceType string `json:"source_type"`
	Title      string `json:"title"`
	Content    string `json:"content"`
}

// handleKnowledgeList: GET /assistant/knowledge
func handleKnowledgeList(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	var rows []database.AssistantKnowledgeChunk
	if err := database.CentralDB.
		Where("assistant_id = ? AND kb_id = ?", eng.platformAssistantID, eng.platformAssistantID).
		Order("title, id").Find(&rows).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo listar el conocimiento"})
	}
	out := make([]knowledgeItemDTO, len(rows))
	for i, r := range rows {
		out[i] = knowledgeItemDTO{ID: r.ID, SourceType: r.SourceType, Title: r.Title, Content: r.Content}
	}
	return c.JSON(fiber.Map{"data": out})
}

type createKnowledgeRequest struct {
	SourceType string `json:"source_type"`
	Title      string `json:"title"`
	Content    string `json:"content"`
}

// handleKnowledgeCreate: POST /assistant/knowledge — trocea, embebe y
// guarda (reemplaza cualquier fragmento previo con el mismo título, ver
// knowledge.Indexer.Upsert). Requiere que el asistente tenga un proveedor
// de embeddings configurado (mismo o de respaldo).
func handleKnowledgeCreate(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	var body createKnowledgeRequest
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	body.Title = strings.TrimSpace(body.Title)
	body.Content = strings.TrimSpace(body.Content)
	if body.Title == "" || body.Content == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "title y content son requeridos"})
	}
	if body.SourceType == "" {
		body.SourceType = "manual"
	}

	store, err := knowledgeStoreFor(c.Context(), eng.platformAssistantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	scope := knowledgeScope()
	chunks, err := store.Upsert(c.Context(), scope, body.SourceType, body.Title, body.Content)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"chunks": chunks})
}

// handleKnowledgeDelete: DELETE /assistant/knowledge/:id — borra UN
// fragmento (el panel borra todos los de un "documento" llamando esto una
// vez por cada id del grupo, igual que el original).
func handleKnowledgeDelete(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	if err := database.CentralDB.Delete(&database.AssistantKnowledgeChunk{}, uint(id)).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo borrar"})
	}
	return c.SendStatus(fiber.StatusOK)
}

// defaultKnowledgeSeed: corpus real sobre el funcionamiento de Mistiq —
// qué hace cada módulo, cómo funciona la facturación electrónica, cómo se
// empieza. Deliberadamente SIN precios/planes exactos acá: esos se
// consultan en vivo con la acción commercial_lookup_plans (siempre
// actualizados contra la BD real), duplicarlos acá los dejaría
// desactualizados en cuanto cambie un precio. El equipo comercial puede
// ampliar/editar cualquiera de estos documentos desde este mismo panel.
var defaultKnowledgeSeed = []struct{ Title, Content string }{
	{
		Title: "Qué es Mistiq",
		Content: "Mistiq es un sistema de gestión (ERP + punto de venta) para negocios peruanos, en la nube: " +
			"no se instala nada, se usa desde el navegador (y hay una app de punto de venta para " +
			"Android/escritorio para el mostrador). Cubre ventas y punto de venta, facturación electrónica " +
			"ante SUNAT, inventario, compras, contactos/clientes, caja y bancos, cuentas por cobrar, un " +
			"módulo especializado para restaurantes, y una tienda online (catálogo digital / ecommerce). " +
			"Cada negocio elige qué módulos activar según su plan — no todos los planes traen todos los " +
			"módulos.",
	},
	{
		Title: "Punto de venta y ventas",
		Content: "El punto de venta permite cobrar rápido desde el navegador o la app (Android/escritorio), " +
			"con caja y turnos (apertura/cierre de caja, arqueo, ingresos y egresos), múltiples medios de " +
			"pago (efectivo, tarjeta, Yape/Plin, transferencia), y emisión del comprobante electrónico en " +
			"el mismo momento de la venta. Funciona con o sin conexión estable — las ventas quedan " +
			"registradas y se sincronizan.",
	},
	{
		Title: "Facturación electrónica SUNAT",
		Content: "Mistiq emite boletas de venta electrónicas, facturas electrónicas, notas de crédito y " +
			"notas de débito, y guías de remisión electrónica (GRE) — todo enviado directo a SUNAT desde el " +
			"sistema, sin pasos manuales. Los comprobantes quedan validados y disponibles para descargar " +
			"(PDF/XML/CDR) y reenviar al cliente por correo. Si SUNAT rechaza un comprobante o hay un " +
			"problema de conexión con SUNAT, el sistema reintenta y avisa — el negocio no se queda sin " +
			"poder facturar por una caída temporal del servicio de SUNAT.",
	},
	{
		Title: "Inventario y compras",
		Content: "El módulo de inventario controla stock por producto (y por variantes/presentaciones), " +
			"alerta cuando un producto está por agotarse, y se actualiza automáticamente con cada venta y " +
			"cada compra registrada. El módulo de compras registra las compras a proveedores, actualiza el " +
			"costo del producto y suma el stock comprado.",
	},
	{
		Title: "Módulo de restaurantes",
		Content: "Para negocios de comida, Mistiq tiene un módulo especializado: mapa de mesas, comandas " +
			"que van directo a cocina, control de delivery, y todo integrado con la caja y la facturación " +
			"electrónica — la cuenta de una mesa se cobra y factura sin pasos extra. Este módulo es " +
			"adicional al punto de venta general, pensado específicamente para restaurantes/food service.",
	},
	{
		Title: "Cuentas por cobrar y caja/bancos",
		Content: "Cuentas por cobrar lleva el control de ventas al crédito: cuánto debe cada cliente, " +
			"vencimientos, y registro de pagos parciales. Caja y bancos centraliza el efectivo y las cuentas " +
			"bancarias del negocio, con el detalle de ingresos y egresos — útil para saber cuánto dinero hay " +
			"realmente disponible, más allá de lo vendido.",
	},
	{
		Title: "Tienda online (catálogo digital)",
		Content: "El módulo de ecommerce genera una tienda online conectada al mismo inventario y precios " +
			"del sistema — un pedido hecho por internet descuenta stock igual que una venta del mostrador, " +
			"sin llevar dos inventarios por separado.",
	},
	{
		Title: "Cómo empezar",
		Content: "Para empezar a usar Mistiq no se necesita instalar nada: se crea la cuenta con el RUC del " +
			"negocio (los datos de la empresa se validan automáticamente contra SUNAT), se elige un plan, y " +
			"en minutos queda lista para emitir boletas/facturas y vender. El equipo comercial ayuda con la " +
			"configuración inicial (productos, series de comprobantes, usuarios) durante el primer contacto.",
	},
	{
		Title: "Soporte",
		Content: "El soporte de Mistiq se da por WhatsApp y correo. Cualquier duda técnica o de facturación " +
			"electrónica que este asistente no pueda resolver con confianza se deriva a un asesor humano — " +
			"mejor derivar que arriesgarse a dar una respuesta incorrecta sobre algo tan sensible como " +
			"facturación electrónica.",
	},
}

// handleKnowledgeSeedDefaults: POST /assistant/knowledge/seed-defaults —
// idempotente (Upsert reemplaza por título), pensado para el botón
// "Cargar contenido por defecto" del panel en una instalación nueva.
func handleKnowledgeSeedDefaults(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	store, err := knowledgeStoreFor(c.Context(), eng.platformAssistantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	scope := knowledgeScope()
	totalChunks := 0
	for _, doc := range defaultKnowledgeSeed {
		n, err := store.Upsert(c.Context(), scope, "faq", doc.Title, doc.Content)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		totalChunks += n
	}
	return c.JSON(fiber.Map{"docs": len(defaultKnowledgeSeed), "chunks": totalChunks})
}

func knowledgeScope() knowledge.KBScope {
	return knowledge.ScopeFor(eng.platformAssistantID)
}

// knowledgeStoreFor resuelve el proveedor de embeddings vigente y arma un
// Store fresco — mismo criterio que newKnowledgeRetriever en wiring.go
// (barato, sin caché propia: el resolver ya cachea la Config).
func knowledgeStoreFor(ctx context.Context, assistantID uint) (*knowledgemysql.Store, error) {
	cfg, err := eng.resolver.Resolve(ctx, assistantID)
	if err != nil {
		return nil, err
	}
	return knowledgemysql.New(database.CentralDB, buildEmbedder(cfg)), nil
}
