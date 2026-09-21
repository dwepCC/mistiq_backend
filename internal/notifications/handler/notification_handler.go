package handler

import (
	"bufio"
	"fmt"
	"strconv"
	"time"

	"tukifac/internal/notifications/service"
	"tukifac/pkg/database"
	"tukifac/pkg/notificationevents"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type NotificationHandler struct{}

func NewNotificationHandler() *NotificationHandler { return &NotificationHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

func currentUserID(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}

func currentPermissions(c fiber.Ctx) []string {
	v, _ := c.Locals("permissions").([]string)
	return v
}

// ListAPI GET /api/notifications?limit=&before_id= — paginación simple "cargar más" (mismo
// idioma que ListOrdersAPI de ecommerce: sin total, un límite acotado).
func (h *NotificationHandler) ListAPI(c fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit"))
	beforeID, _ := strconv.ParseUint(c.Query("before_id"), 10, 32)
	rows, err := service.NewNotificationService(db(c)).List(service.ListParams{
		UserID:      currentUserID(c),
		Permissions: currentPermissions(c),
		Limit:       limit,
		BeforeID:    uint(beforeID),
	})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// UnreadCountAPI GET /api/notifications/unread-count — fuente de verdad del badge.
func (h *NotificationHandler) UnreadCountAPI(c fiber.Ctx) error {
	count, err := service.NewNotificationService(db(c)).UnreadCount(currentUserID(c), currentPermissions(c))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"unread_count": count})
}

// MarkReadAPI POST /api/notifications/:id/read
func (h *NotificationHandler) MarkReadAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewNotificationService(db(c))
	if err := svc.MarkRead(uint(id), currentUserID(c), currentPermissions(c)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// MarkAllReadAPI POST /api/notifications/read-all
func (h *NotificationHandler) MarkAllReadAPI(c fiber.Ctx) error {
	svc := service.NewNotificationService(db(c))
	if err := svc.MarkAllRead(currentUserID(c), currentPermissions(c)); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// EventsSSE GET /api/notifications/events — señal mínima "algo cambió" (Fase 6): el payload nunca
// lleva contenido de negocio, ver comentario de notificationevents.EventChanged. Mismo patrón
// exacto que internal/billing/handler/sse_handler.go (hub independiente, ver pkg/notificationevents).
func (h *NotificationHandler) EventsSSE(c fiber.Ctx) error {
	tenant, ok := c.Locals("tenant").(*database.Tenant)
	if !ok || tenant == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant requerido"})
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache, no-transform")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	ch, unsub := notificationevents.Subscribe(tenant.ID)
	defer unsub()

	return c.SendStreamWriter(func(w *bufio.Writer) {
		ctx := c.Context()
		_, _ = fmt.Fprintf(w, "retry: 3000\n\n")
		_ = w.Flush()

		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-ch:
				if !ok {
					return
				}
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", notificationevents.EventChanged, data)
				if err := w.Flush(); err != nil {
					return
				}
			case <-ping.C:
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	})
}
