package fleet

import (
	"tukifac/internal/fleet/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registra las rutas de flota (transportistas/conductores/vehículos para GRE).
// Antes solo exigían el módulo del plan ("billing"), nunca el rol del usuario. El catálogo define
// fleet.{view,manage} (ver internal/users/service/role_service.go); se agrega RequirePermission.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewFleetHandler()
	mod := middleware.RequireModule("billing")
	view := middleware.RequirePermission("fleet.view")
	manage := middleware.RequirePermission("fleet.manage")

	api.Get("/fleet/defaults", mod, view, h.DefaultsAPI)

	api.Get("/fleet/carriers", mod, view, h.ListCarriersAPI)
	api.Get("/fleet/carriers/:id", mod, view, h.GetCarrierAPI)
	api.Post("/fleet/carriers", mod, manage, h.CreateCarrierAPI)
	api.Put("/fleet/carriers/:id", mod, manage, h.UpdateCarrierAPI)
	api.Patch("/fleet/carriers/:id/toggle", mod, manage, h.ToggleCarrierAPI)

	api.Get("/fleet/drivers", mod, view, h.ListDriversAPI)
	api.Get("/fleet/drivers/:id", mod, view, h.GetDriverAPI)
	api.Post("/fleet/drivers", mod, manage, h.CreateDriverAPI)
	api.Put("/fleet/drivers/:id", mod, manage, h.UpdateDriverAPI)
	api.Patch("/fleet/drivers/:id/toggle", mod, manage, h.ToggleDriverAPI)

	api.Get("/fleet/vehicles", mod, view, h.ListVehiclesAPI)
	api.Get("/fleet/vehicles/:id", mod, view, h.GetVehicleAPI)
	api.Post("/fleet/vehicles", mod, manage, h.CreateVehicleAPI)
	api.Put("/fleet/vehicles/:id", mod, manage, h.UpdateVehicleAPI)
	api.Patch("/fleet/vehicles/:id/toggle", mod, manage, h.ToggleVehicleAPI)
}
