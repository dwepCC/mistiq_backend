package handler

import (
	"strings"
	"time"

	"tukifac/internal/accounting/service"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Handler endpoints del módulo Contabilidad. Todas las rutas exigen el módulo «accounting» y un
// permiso propio (ver routes.go).
type Handler struct {
	// Sire cliente HTTP de SUNAT; se reemplaza en las pruebas.
	Sire *service.SireClient
}

func New() *Handler { return &Handler{Sire: service.NewSireClient()} }

func svc(c fiber.Ctx) *service.Service {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return service.New(v)
}

func fail(c fiber.Ctx, err error) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
}

func ok(c fiber.Ctx, data any) error { return c.JSON(fiber.Map{"success": true, "data": data}) }

func month(c fiber.Ctx) string {
	m := strings.TrimSpace(c.Query("month"))
	if m == "" {
		m = time.Now().Format("2006-01")
	}
	return m
}

// SalesRegister GET /accounting/registers/sales?month=YYYY-MM&with_status=1
func (h *Handler) SalesRegister(c fiber.Ctx) error {
	t, err := svc(c).SalesRegister(month(c), c.Query("with_status") == "1" || c.Query("with_status") == "true")
	if err != nil {
		return fail(c, err)
	}
	return ok(c, t)
}

// PurchasesRegister GET /accounting/registers/purchases?month=YYYY-MM
func (h *Handler) PurchasesRegister(c fiber.Ctx) error {
	t, err := svc(c).PurchasesRegister(month(c))
	if err != nil {
		return fail(c, err)
	}
	return ok(c, t)
}

// Summary GET /accounting/summary?from=YYYY-MM-DD&to=YYYY-MM-DD
func (h *Handler) Summary(c fiber.Ctx) error {
	from, err1 := time.Parse("2006-01-02", c.Query("from"))
	to, err2 := time.Parse("2006-01-02", c.Query("to"))
	if err1 != nil || err2 != nil {
		return fail(c, errBadDates)
	}
	if to.Before(from) {
		return fail(c, errBadRange)
	}
	r, err := svc(c).SummaryReport(from, to)
	if err != nil {
		return fail(c, err)
	}
	return ok(c, r)
}

// Formats GET /accounting/formats
func (h *Handler) Formats(c fiber.Ctx) error {
	type f struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	out := make([]f, 0, len(service.Formats))
	for _, x := range service.Formats {
		out = append(out, f{x.Key, x.Name})
	}
	return ok(c, out)
}

// ExportFormat GET /accounting/formats/export?format=concar&month=YYYY-MM
func (h *Handler) ExportFormat(c fiber.Ctx) error {
	ex, err := svc(c).ExportFormat(strings.TrimSpace(c.Query("format")), month(c))
	if err != nil {
		return fail(c, err)
	}
	return ok(c, ex)
}

// Ledger GET /accounting/ledger?month=YYYY-MM
func (h *Handler) Ledger(c fiber.Ctx) error {
	l, err := svc(c).Ledger(month(c))
	if err != nil {
		return fail(c, err)
	}
	return ok(c, l)
}

// GetSettings GET /accounting/settings
func (h *Handler) GetSettings(c fiber.Ctx) error {
	s := svc(c)
	st, err := s.Settings()
	if err != nil {
		return fail(c, err)
	}
	def := service.DefaultAccounts
	return ok(c, fiber.Map{"settings": st, "defaults": def})
}

// SaveSettings PUT /accounting/settings
func (h *Handler) SaveSettings(c fiber.Ctx) error {
	var in database.TenantAccountingSettings
	if err := c.Bind().JSON(&in); err != nil {
		return fail(c, errBadJSON)
	}
	st, err := svc(c).SaveSettings(in)
	if err != nil {
		return fail(c, err)
	}
	return ok(c, st)
}

// GetSireSettings GET /accounting/sire/settings — nunca devuelve los secretos.
func (h *Handler) GetSireSettings(c fiber.Ctx) error {
	v, err := svc(c).SireSettingsView()
	if err != nil {
		return fail(c, err)
	}
	return ok(c, v)
}

// SaveSireSettings PUT /accounting/sire/settings — un secreto vacío conserva el guardado.
func (h *Handler) SaveSireSettings(c fiber.Ctx) error {
	var in struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		SolUsername  string `json:"sol_username"`
		SolPassword  string `json:"sol_password"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return fail(c, errBadJSON)
	}
	s := svc(c)
	if err := s.SaveSireSettings(in.ClientID, in.ClientSecret, in.SolUsername, in.SolPassword); err != nil {
		return fail(c, err)
	}
	v, err := s.SireSettingsView()
	if err != nil {
		return fail(c, err)
	}
	return ok(c, v)
}

func book(c fiber.Ctx) string {
	if c.Params("book") == service.SireBookPurchase {
		return service.SireBookPurchase
	}
	return service.SireBookSale
}

// SirePeriods GET /accounting/sire/:book/periods
func (h *Handler) SirePeriods(c fiber.Ctx) error {
	p, err := svc(c).SirePeriods(h.Sire, book(c))
	if err != nil {
		return fail(c, err)
	}
	return ok(c, p)
}

// SireTickets GET /accounting/sire/:book/tickets
func (h *Handler) SireTickets(c fiber.Ctx) error {
	t, err := svc(c).SireTickets(book(c), 30)
	if err != nil {
		return fail(c, err)
	}
	return ok(c, t)
}

// SireRequestTicket POST /accounting/sire/:book/tickets {period}
func (h *Handler) SireRequestTicket(c fiber.Ctx) error {
	var in struct {
		Period string `json:"period"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return fail(c, errBadJSON)
	}
	uid, _ := c.Locals("user_id").(uint)
	t, err := svc(c).SireRequestTicket(h.Sire, book(c), in.Period, uid)
	if err != nil {
		return fail(c, err)
	}
	return ok(c, t)
}

// SireQuery POST /accounting/sire/:book/query {period, ticket}
func (h *Handler) SireQuery(c fiber.Ctx) error {
	var in struct {
		Period string `json:"period"`
		Ticket string `json:"ticket"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return fail(c, errBadJSON)
	}
	r, err := svc(c).SireQueryTicket(h.Sire, book(c), in.Period, in.Ticket)
	if err != nil {
		return fail(c, err)
	}
	return ok(c, r)
}

// SireAccept POST /accounting/sire/sale/accept {period, confirm:true} — irreversible en SUNAT.
func (h *Handler) SireAccept(c fiber.Ctx) error {
	var in struct {
		Period  string `json:"period"`
		Confirm bool   `json:"confirm"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return fail(c, errBadJSON)
	}
	if !in.Confirm {
		return fail(c, errConfirm)
	}
	if err := svc(c).SireAccept(h.Sire, in.Period); err != nil {
		return fail(c, err)
	}
	return ok(c, fiber.Map{"accepted": true, "period": in.Period})
}
