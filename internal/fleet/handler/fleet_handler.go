package handler

import (
	"strconv"

	"tukifac/internal/fleet/service"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type FleetHandler struct{}

func NewFleetHandler() *FleetHandler { return &FleetHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

func parseID(c fiber.Ctx) (uint, error) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}

func parseCarrierIDQuery(c fiber.Ctx) *uint {
	raw := c.Query("carrier_id")
	if raw == "" {
		return nil
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 {
		return nil
	}
	id := uint(n)
	return &id
}

func activeOnly(c fiber.Ctx) bool {
	return c.Query("active_only") == "1" || c.Query("active_only") == "true"
}

func (h *FleetHandler) ListCarriersAPI(c fiber.Ctx) error {
	list, err := service.NewFleetService(db(c)).ListCarriers(c.Query("q"), activeOnly(c))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

func (h *FleetHandler) GetCarrierAPI(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	row, err := service.NewFleetService(db(c)).GetCarrier(id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No encontrado"})
	}
	return c.JSON(fiber.Map{"data": row})
}

func (h *FleetHandler) CreateCarrierAPI(c fiber.Ctx) error {
	var body service.CarrierInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	row, err := service.NewFleetService(db(c)).CreateCarrier(body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": row})
}

func (h *FleetHandler) UpdateCarrierAPI(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.CarrierInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	row, err := service.NewFleetService(db(c)).UpdateCarrier(id, body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

func (h *FleetHandler) ToggleCarrierAPI(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewFleetService(db(c)).ToggleCarrier(id); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *FleetHandler) ListDriversAPI(c fiber.Ctx) error {
	list, err := service.NewFleetService(db(c)).ListDrivers(c.Query("q"), activeOnly(c), parseCarrierIDQuery(c))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

func (h *FleetHandler) GetDriverAPI(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	row, err := service.NewFleetService(db(c)).GetDriver(id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No encontrado"})
	}
	return c.JSON(fiber.Map{"data": row})
}

func (h *FleetHandler) CreateDriverAPI(c fiber.Ctx) error {
	var body service.DriverInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	row, err := service.NewFleetService(db(c)).CreateDriver(body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": row})
}

func (h *FleetHandler) UpdateDriverAPI(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.DriverInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	row, err := service.NewFleetService(db(c)).UpdateDriver(id, body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

func (h *FleetHandler) ToggleDriverAPI(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewFleetService(db(c)).ToggleDriver(id); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *FleetHandler) ListVehiclesAPI(c fiber.Ctx) error {
	list, err := service.NewFleetService(db(c)).ListVehicles(c.Query("q"), activeOnly(c), parseCarrierIDQuery(c))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

func (h *FleetHandler) GetVehicleAPI(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	row, err := service.NewFleetService(db(c)).GetVehicle(id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No encontrado"})
	}
	return c.JSON(fiber.Map{"data": row})
}

func (h *FleetHandler) CreateVehicleAPI(c fiber.Ctx) error {
	var body service.VehicleInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	row, err := service.NewFleetService(db(c)).CreateVehicle(body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": row})
}

func (h *FleetHandler) UpdateVehicleAPI(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.VehicleInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	row, err := service.NewFleetService(db(c)).UpdateVehicle(id, body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

func (h *FleetHandler) ToggleVehicleAPI(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewFleetService(db(c)).ToggleVehicle(id); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *FleetHandler) DefaultsAPI(c fiber.Ctx) error {
	def := service.NewFleetService(db(c)).GetDefaults()
	return c.JSON(fiber.Map{"data": def})
}
