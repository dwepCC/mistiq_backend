package users

import (
	"tukifac/internal/users/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	userH := handler.NewUserHandler()
	roleH := handler.NewRoleHandler()

	// Perfil del usuario autenticado (sin permiso de administración)
	api.Get("/profile/me", userH.GetMyProfileAPI)
	api.Put("/profile/me", userH.UpdateMyProfileAPI)
	api.Post("/profile/me/password", userH.ChangeMyPasswordAPI)

	api.Get("/users", middleware.RequirePermission("users.view"), userH.ListAPI)
	api.Get("/users/:id", middleware.RequirePermission("users.view"), userH.GetAPI)
	api.Post("/users", middleware.RequirePermission("users.create"), userH.CreateAPI)
	api.Post("/users/:id/restore", middleware.RequirePermission("users.create"), userH.RestoreAPI)
	api.Put("/users/:id", middleware.RequirePermission("users.edit"), userH.UpdateAPI)
	api.Delete("/users/:id", middleware.RequirePermission("users.delete"), userH.DeleteAPI)
	api.Patch("/users/:id/toggle", middleware.RequirePermission("users.edit"), userH.ToggleAPI)
	api.Get("/roles", middleware.RequirePermission("roles.view"), roleH.ListAPI)
	api.Get("/roles/:id", middleware.RequirePermission("roles.view"), roleH.GetAPI)
	api.Post("/roles", middleware.RequirePermission("roles.manage"), roleH.CreateAPI)
	api.Put("/roles/:id", middleware.RequirePermission("roles.manage"), roleH.UpdateAPI)
	api.Delete("/roles/:id", middleware.RequirePermission("roles.manage"), roleH.DeleteAPI)
	api.Get("/permissions", middleware.RequirePermission("roles.view"), roleH.ListPermissionsAPI)
}
