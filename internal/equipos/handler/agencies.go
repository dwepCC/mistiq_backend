package handler

import (
	"tukifac/internal/equipos/service"

	"github.com/gofiber/fiber/v3"
)

// ── Importación del catálogo ───────────────────────────────────────────────

type catalogImportBody struct {
	Rows   []service.CatalogImportRow `json:"rows"`
	Commit bool                       `json:"commit"`
}

// ImportCatalog carga masiva del catálogo desde la plantilla de Excel (sin commit = vista previa).
func (h *Handler) ImportCatalog(c fiber.Ctx) error {
	var in catalogImportBody
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	res, err := svc().ImportCatalog(in.Rows, in.Commit)
	if err != nil {
		return fail(c, err)
	}
	if in.Commit {
		audit(c, "equip_catalog_imported", "equip_product", 0, fiber.Map{"total": res.Total, "created": res.Created, "updated": res.Updated, "unchanged": res.Unchanged, "errors": res.Errors})
	}
	return c.JSON(fiber.Map{"data": res})
}

// ── Agencias de los transportistas ─────────────────────────────────────────

func (h *Handler) ListAgencies(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	rows, err := svc().ListAgencies(id, c.Query("include_inactive") == "1")
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *Handler) CreateAgency(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var in service.AgencyInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	a, err := svc().CreateAgency(id, in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_agency_created", "equip_carrier_agency", a.ID, in)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": a})
}

func (h *Handler) UpdateAgency(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var in service.AgencyInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	a, err := svc().UpdateAgency(id, in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_agency_updated", "equip_carrier_agency", a.ID, in)
	return c.JSON(fiber.Map{"data": a})
}

type agencyImportBody struct {
	Rows   []service.AgencyImportRow `json:"rows"`
	Commit bool                      `json:"commit"`
}

// ImportAgencies recibe las filas ya leídas del Excel; sin `commit` solo devuelve la vista previa.
func (h *Handler) ImportAgencies(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var in agencyImportBody
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	res, err := svc().ImportAgencies(id, in.Rows, in.Commit)
	if err != nil {
		return fail(c, err)
	}
	if in.Commit {
		audit(c, "equip_agencies_imported", "equip_carrier", id, fiber.Map{"total": res.Total, "created": res.Created, "updated": res.Updated})
	}
	return c.JSON(fiber.Map{"data": res})
}
