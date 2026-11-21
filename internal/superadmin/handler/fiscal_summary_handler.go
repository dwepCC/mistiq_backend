package handler

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"tukifac/internal/fiscal/summary"
	"tukifac/pkg/database"
)

// FiscalSummaryHandler expone el resumen fiscal por tenant que vive en la BD CENTRAL
// (tenant_fiscal_daily / tenant_fiscal_health). Las lecturas nunca tocan las BD de los tenants.
// Ver docs/PLAN-RESUMEN-FISCAL-POR-TENANT.md.
type FiscalSummaryHandler struct {
	mu          sync.Mutex
	lastRefresh map[uint]time.Time
	lastResend  map[uint]time.Time
}

func NewFiscalSummaryHandler() *FiscalSummaryHandler {
	return &FiscalSummaryHandler{lastRefresh: map[uint]time.Time{}, lastResend: map[uint]time.Time{}}
}

// Mínimo entre dos "Verificar ahora" del mismo tenant, para no cargar su BD.
const fiscalSummaryRefreshCooldown = 20 * time.Second

var validSummaryDocTypes = map[string]bool{"01": true, "03": true, "07": true, "08": true, "09": true, "31": true}

func validDay(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// GET /api/superadmin/fiscal/tenant-summary
// Query: from, to (YYYY-MM-DD, día de emisión), doc_type (coma o repetido: 01,03,07,08,09,31),
// ruc, q, only_pending, sort (to_send|emitted|accepted|sent|rejected|name|ruc|oldest|last_issue|scanned),
// dir (asc|desc; vacío = el natural de la columna), page, per_page.
func (h *FiscalSummaryHandler) ListAPI(c fiber.Ctx) error {
	from, to := strings.TrimSpace(c.Query("from")), strings.TrimSpace(c.Query("to"))
	if !validDay(from) || !validDay(to) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "from/to deben tener formato YYYY-MM-DD"})
	}
	if from != "" && to != "" && from > to {
		from, to = to, from
	}

	var docTypes []string
	for _, raw := range strings.Split(c.Query("doc_type"), ",") {
		code := strings.TrimSpace(raw)
		if code == "" {
			continue
		}
		if !validSummaryDocTypes[code] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "doc_type inválido: " + code})
		}
		docTypes = append(docTypes, code)
	}

	// Las tablas las crea migrate-central en el deploy; si aún no existen, se avisa en vez de un 500 opaco.
	m := database.CentralDB.Migrator()
	if !m.HasTable(&database.TenantFiscalDaily{}) || !m.HasTable(&database.TenantFiscalHealth{}) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "El resumen fiscal aún no está disponible: falta aplicar migrate-central.",
		})
	}

	page, _ := strconv.Atoi(c.Query("page"))
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	only, _ := strconv.ParseBool(c.Query("only_pending"))
	stale, _ := strconv.ParseBool(c.Query("stale_only"))

	res, err := summary.Summarize(database.CentralDB, summary.Filter{
		From: from, To: to, DocTypes: docTypes,
		RUC: c.Query("ruc"), Q: c.Query("q"), OnlyPending: only, StaleOnly: stale,
		Sort: c.Query("sort"), Dir: c.Query("dir"), Page: page, PerPage: perPage,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "No se pudo leer el resumen fiscal"})
	}
	return c.JSON(res)
}

// POST /api/superadmin/fiscal/tenant-summary/:id/refresh — "Verificar ahora": reescanea UN tenant
// (solo lectura en su BD) sin esperar al job periódico.
func (h *FiscalSummaryHandler) RefreshAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	var tenant database.Tenant
	if err := database.CentralDB.First(&tenant, uint(id)).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant no encontrado"})
	}

	h.mu.Lock()
	if last, ok := h.lastRefresh[tenant.ID]; ok && time.Since(last) < fiscalSummaryRefreshCooldown {
		h.mu.Unlock()
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "Este tenant se verificó hace instantes; espera unos segundos.",
		})
	}
	h.lastRefresh[tenant.ID] = time.Now()
	h.mu.Unlock()

	if err := summary.NewScanner().ScanTenant(tenant, false); err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "No se pudo leer la base del tenant: " + err.Error(),
		})
	}
	return c.JSON(fiber.Map{"ok": true})
}
