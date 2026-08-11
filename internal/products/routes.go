package products

import (
	"tukifac/internal/products/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewProductHandler()
	api.Get("/products",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.SearchAPI,
	)
	api.Get("/products/next-code",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.NextCodeAPI,
	)
	api.Get("/products/lookup-by-code",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.LookupByCodeAPI,
	)
	api.Get("/products/:id/serials",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.ProductSerialsAPI,
	)
	api.Get("/products/:id",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.GetAPI,
	)
	api.Post("/products", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), h.CreateAPI)
	api.Post("/products/bulk-import/restaurant", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), h.BulkImportRestaurantAPI)
	api.Post("/products/bulk-import/catalog", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), h.BulkImportCatalogAPI)
	api.Post("/products/bulk-delete/restaurant",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequirePermission("products.delete"),
		h.BulkDeleteRestaurantAPI,
	)
	api.Post("/products/bulk-delete/catalog",
		middleware.RequireModule("products"),
		middleware.RequirePermission("products.delete"),
		h.BulkDeleteCatalogAPI,
	)
	api.Patch("/products/bulk-toggle",
		middleware.RequireModule("products"),
		middleware.RequirePermission("products.edit"),
		h.BulkToggleCatalogAPI,
	)
	api.Patch("/products/bulk-update",
		middleware.RequireModule("products"),
		middleware.RequirePermission("products.edit"),
		h.BulkUpdateCatalogAPI,
	)
	api.Put("/products/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), h.UpdateAPI)
	api.Patch("/products/:id/toggle", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), h.ToggleAPI)
	api.Delete("/products/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), h.DeleteAPI)
	api.Post("/products/:id/image", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), h.UploadImageAPI)
	api.Get("/categories",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.CategoryListAPI,
	)
	api.Post("/categories", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), h.CategoryCreateAPI)
	api.Put("/categories/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), h.CategoryUpdateAPI)
	api.Delete("/categories/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), h.CategoryDeleteAPI)
	api.Get("/preparation-areas",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.PreparationAreaListAPI,
	)
	api.Post("/preparation-areas", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), h.PreparationAreaCreateAPI)
	api.Put("/preparation-areas/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), h.PreparationAreaUpdateAPI)
	api.Delete("/preparation-areas/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), h.PreparationAreaDeleteAPI)
	api.Get("/modifier-groups",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.ModifierGroupsAPI,
	)
	api.Post("/modifier-groups",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsManageOrTenantWrite(),
		h.ModifierGroupCreateAPI,
	)
	api.Put("/modifier-groups/:id",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsManageOrTenantWrite(),
		h.ModifierGroupUpdateAPI,
	)
	api.Delete("/modifier-groups/:id",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsManageOrTenantWrite(),
		h.ModifierGroupDeleteAPI,
	)
}
