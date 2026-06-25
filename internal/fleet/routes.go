package fleet

import (
	"tukifac/internal/fleet/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewFleetHandler()
	mod := middleware.RequireModule("billing")

	api.Get("/fleet/defaults", mod, h.DefaultsAPI)

	api.Get("/fleet/carriers", mod, h.ListCarriersAPI)
	api.Get("/fleet/carriers/:id", mod, h.GetCarrierAPI)
	api.Post("/fleet/carriers", mod, h.CreateCarrierAPI)
	api.Put("/fleet/carriers/:id", mod, h.UpdateCarrierAPI)
	api.Patch("/fleet/carriers/:id/toggle", mod, h.ToggleCarrierAPI)

	api.Get("/fleet/drivers", mod, h.ListDriversAPI)
	api.Get("/fleet/drivers/:id", mod, h.GetDriverAPI)
	api.Post("/fleet/drivers", mod, h.CreateDriverAPI)
	api.Put("/fleet/drivers/:id", mod, h.UpdateDriverAPI)
	api.Patch("/fleet/drivers/:id/toggle", mod, h.ToggleDriverAPI)

	api.Get("/fleet/vehicles", mod, h.ListVehiclesAPI)
	api.Get("/fleet/vehicles/:id", mod, h.GetVehicleAPI)
	api.Post("/fleet/vehicles", mod, h.CreateVehicleAPI)
	api.Put("/fleet/vehicles/:id", mod, h.UpdateVehicleAPI)
	api.Patch("/fleet/vehicles/:id/toggle", mod, h.ToggleVehicleAPI)
}
