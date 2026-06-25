package handler

import (
	"strconv"

	"tukifac/internal/superadmin/service"

	"github.com/gofiber/fiber/v3"
)

type MigrationHandler struct {
	svc *service.MigrationFleetService
}

func NewMigrationHandler() *MigrationHandler {
	return &MigrationHandler{svc: service.NewMigrationFleetService()}
}

// GET /api/superadmin/migrations
func (h *MigrationHandler) ListAPI(c fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	perPage, _ := strconv.Atoi(c.Query("per_page", "25"))
	curV, _ := strconv.Atoi(c.Query("current_version", "0"))
	tgtV, _ := strconv.Atoi(c.Query("target_version", "0"))

	params := service.MigrationListParams{
		Page:             page,
		PerPage:          perPage,
		Status:           c.Query("status"),
		CurrentVersion:   curV,
		TargetVersion:    tgtV,
		Outdated:         c.Query("outdated") == "true" || c.Query("outdated") == "1",
		Failed:           c.Query("failed") == "true" || c.Query("failed") == "1",
		Pending:          c.Query("pending") == "true" || c.Query("pending") == "1",
		Drifted:          c.Query("drifted") == "true" || c.Query("drifted") == "1",
		TenantSlug:       c.Query("tenant_slug"),
		TenantName:       c.Query("tenant_name"),
		LastMigratedFrom: c.Query("last_migrated_from"),
		LastMigratedTo:   c.Query("last_migrated_to"),
	}
	items, total, err := h.svc.List(params)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"data":     items,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

// GET /api/superadmin/migrations/summary
func (h *MigrationHandler) SummaryAPI(c fiber.Ctx) error {
	sum, err := h.svc.Summary()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(sum)
}

// GET /api/superadmin/migrations/jobs
func (h *MigrationHandler) ListJobsAPI(c fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	jobs, err := h.svc.ListJobs(limit)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": jobs})
}

// GET /api/superadmin/migrations/jobs/:jobId
func (h *MigrationHandler) GetJobAPI(c fiber.Ctx) error {
	jobID, err := strconv.ParseUint(c.Params("jobId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	job, err := h.svc.GetJob(uint(jobID))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(job)
}

type driftScanBody struct {
	TenantID uint `json:"tenant_id"`
	Limit    int  `json:"limit"`
	Async    bool `json:"async"`
}

// POST /api/superadmin/migrations/drift-scan
func (h *MigrationHandler) DriftScanAPI(c fiber.Ctx) error {
	var body driftScanBody
	_ = c.Bind().Body(&body)
	if body.Limit <= 0 {
		body.Limit = 100
	}
	if body.Async && body.TenantID == 0 {
		job, err := h.svc.StartDriftScanJob(body.Limit, saUserID(c), c.IP())
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"job": job})
	}
	reports, err := h.svc.DriftScan(service.DriftScanParams{
		TenantID: body.TenantID,
		Limit:    body.Limit,
		DryRun:   true,
	})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": reports, "count": len(reports)})
}

type bulkRepairBody struct {
	TenantIDs []uint `json:"tenant_ids"`
	Limit     int    `json:"limit"`
}

// POST /api/superadmin/migrations/bulk/repair
func (h *MigrationHandler) BulkRepairAPI(c fiber.Ctx) error {
	var body bulkRepairBody
	_ = c.Bind().Body(&body)
	job, err := h.svc.StartBulkRepairSelected(service.BulkRepairParams{
		TenantIDs: body.TenantIDs,
		Limit:     body.Limit,
	}, saUserID(c), c.IP())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"job": job})
}

// POST /api/superadmin/migrations/bulk/repair-drifted
func (h *MigrationHandler) BulkRepairDriftedAPI(c fiber.Ctx) error {
	var body bulkRepairBody
	_ = c.Bind().Body(&body)
	limit := body.Limit
	if limit <= 0 {
		limit = 50
	}
	job, err := h.svc.StartBulkRepairDrifted(limit, saUserID(c), c.IP())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"job": job})
}

// POST /api/superadmin/migrations/bulk/retry-failed
func (h *MigrationHandler) BulkRetryFailedAPI(c fiber.Ctx) error {
	var body bulkRepairBody
	_ = c.Bind().Body(&body)
	limit := body.Limit
	if limit <= 0 {
		limit = 50
	}
	job, err := h.svc.StartBulkRetryFailed(limit, saUserID(c), c.IP())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"job": job})
}

func (h *MigrationHandler) tenantID(c fiber.Ctx) (uint, error) {
	id, err := strconv.ParseUint(c.Params("tenantId"), 10, 32)
	return uint(id), err
}

func saUserID(c fiber.Ctx) uint {
	v, _ := c.Locals("sa_user_id").(uint)
	return v
}

// GET /api/superadmin/migrations/:tenantId/history
func (h *MigrationHandler) HistoryAPI(c fiber.Ctx) error {
	tid, err := h.tenantID(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	limit, _ := strconv.Atoi(c.Query("limit", "200"))
	items, err := h.svc.TenantMigrationHistory(tid, limit)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": items})
}

// GET /api/superadmin/migrations/:tenantId/drift
func (h *MigrationHandler) DriftAPI(c fiber.Ctx) error {
	tid, err := h.tenantID(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	reports, err := h.svc.DriftScan(service.DriftScanParams{TenantID: tid, DryRun: true})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if len(reports) == 0 {
		return c.JSON(fiber.Map{"drift_detected": false})
	}
	return c.JSON(reports[0])
}

type repairBody struct {
	DryRun        bool `json:"dry_run"`
	ReconcileOnly bool `json:"reconcile_only"`
}

// POST /api/superadmin/migrations/:tenantId/repair
func (h *MigrationHandler) RepairAPI(c fiber.Ctx) error {
	tid, err := h.tenantID(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body repairBody
	_ = c.Bind().Body(&body)
	result, err := h.svc.RepairTenant(service.RepairParams{
		TenantID: tid, DryRun: body.DryRun, ReconcileOnly: body.ReconcileOnly,
	}, saUserID(c), c.IP())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(result)
}

// POST /api/superadmin/migrations/:tenantId/retry
func (h *MigrationHandler) RetryAPI(c fiber.Ctx) error {
	tid, err := h.tenantID(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := h.svc.Retry(tid, saUserID(c), c.IP()); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/superadmin/migrations/:tenantId/migrate
func (h *MigrationHandler) MigrateAPI(c fiber.Ctx) error {
	tid, err := h.tenantID(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := h.svc.MigrateOne(tid, saUserID(c), c.IP()); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/superadmin/migrations/:tenantId/pause
func (h *MigrationHandler) PauseAPI(c fiber.Ctx) error {
	tid, err := h.tenantID(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := h.svc.Pause(tid, saUserID(c), c.IP()); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/superadmin/migrations/resume-fleet
func (h *MigrationHandler) ResumeFleetAPI(c fiber.Ctx) error {
	if err := h.svc.ResumeFleet(saUserID(c), c.IP()); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/superadmin/migrations/:tenantId/resume
func (h *MigrationHandler) ResumeAPI(c fiber.Ctx) error {
	tid, err := h.tenantID(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := h.svc.Resume(tid, saUserID(c), c.IP()); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}
