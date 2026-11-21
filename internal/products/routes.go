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
	// Costo para el botón «Costo» del POS: permiso propio de esa vista (pos.view_cost), sin depender
	// de products.view_cost. Va aparte de la lista porque la lista omite el costo a quien no tiene
	// products.view_cost (StripProductCost).
	api.Get("/products/:id/cost", middleware.RequireModule("products"), middleware.RequirePermission(middleware.PermPOSViewCost), h.CostAPI)
	api.Get("/products/:id",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.GetAPI,
	)
	api.Post("/products", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), middleware.RequireGlobalCatalog(), h.CreateAPI)
	api.Post("/products/bulk-import/restaurant", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), middleware.RequireGlobalCatalog(), h.BulkImportRestaurantAPI)
	api.Post("/products/bulk-import/catalog", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), middleware.RequireGlobalCatalog(), h.BulkImportCatalogAPI)
	api.Post("/products/bulk-import/advanced", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), middleware.RequireGlobalCatalog(), h.BulkImportAdvancedAPI)
	api.Post("/products/bulk-import/restaurant-advanced", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), middleware.RequireGlobalCatalog(), h.BulkImportRestaurantAdvancedAPI)
	api.Post("/products/bulk-delete/restaurant",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequirePermission("products.delete"),
		middleware.RequireGlobalCatalog(),
		h.BulkDeleteRestaurantAPI,
	)
	api.Post("/products/bulk-delete/catalog",
		middleware.RequireModule("products"),
		middleware.RequirePermission("products.delete"),
		middleware.RequireGlobalCatalog(),
		h.BulkDeleteCatalogAPI,
	)
	api.Patch("/products/bulk-toggle",
		middleware.RequireModule("products"),
		middleware.RequirePermission("products.edit"),
		middleware.RequireGlobalCatalog(),
		h.BulkToggleCatalogAPI,
	)
	api.Patch("/products/bulk-update",
		middleware.RequireModule("products"),
		middleware.RequirePermission("products.edit"),
		middleware.RequireGlobalCatalog(),
		h.BulkUpdateCatalogAPI,
	)
	api.Patch("/products/bulk-branches",
		middleware.RequireModule("products"),
		middleware.RequirePermission("products.edit"),
		h.BulkBranchesAPI,
	)
	api.Patch("/products/bulk-update-prices",
		middleware.RequireModule("products"),
		middleware.RequirePermission("products.edit"),
		h.BulkUpdatePricesAPI,
	)
	api.Put("/products/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.UpdateAPI)
	api.Patch("/products/:id/toggle", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.ToggleAPI)
	api.Delete("/products/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), middleware.RequireGlobalCatalog(), h.DeleteAPI)
	api.Post("/products/:id/image", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.UploadImageAPI)
	// Galería de imágenes adicionales (Catálogo Digital) e imagen de categoría.
	api.Get("/products/:id/gallery", middleware.RequireModule("products"), middleware.RequirePermission("products.view"), h.GalleryListAPI)
	api.Post("/products/:id/gallery", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.GalleryUploadAPI)
	api.Delete("/products/:id/gallery/:imageId", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.GalleryDeleteAPI)
	api.Post("/products/:id/gallery/:imageId/main", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.GalleryPromoteAPI)
	api.Post("/categories/:id/image", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.CategoryUploadImageAPI)
	api.Delete("/categories/:id/image", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.CategoryDeleteImageAPI)
	// Unidades de venta (TenantProductSaleUnit): exclusivas de Tukifac, mismo permiso que el resto
	// de la escritura de catálogo — sin puente restaurantperm (Tukichef no las administra).
	api.Get("/products/:id/sale-units", middleware.RequireModule("products"), middleware.RequirePermission("products.view"), h.SaleUnitListAPI)
	api.Get("/products/:id/sale-units/:suid", middleware.RequireModule("products"), middleware.RequirePermission("products.view"), h.SaleUnitGetAPI)
	api.Post("/products/:id/sale-units", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), middleware.RequireGlobalCatalog(), h.SaleUnitCreateAPI)
	api.Put("/products/:id/sale-units/:suid", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.SaleUnitUpdateAPI)
	api.Delete("/products/:id/sale-units/:suid", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), middleware.RequireGlobalCatalog(), h.SaleUnitDeleteAPI)
	// Precios por sucursal de unidades de venta (Fase 4): mismo permiso/patrón que sale-units.
	api.Get("/products/:id/sale-units/:suid/branch-prices", middleware.RequireModule("products"), middleware.RequirePermission("products.view"), h.SaleUnitBranchPriceListAPI)
	api.Get("/products/:id/sale-units/:suid/branch-prices/:branchId", middleware.RequireModule("products"), middleware.RequirePermission("products.view"), h.SaleUnitBranchPriceGetAPI)
	api.Post("/products/:id/sale-units/:suid/branch-prices/:branchId", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), h.SaleUnitBranchPriceCreateAPI)
	api.Put("/products/:id/sale-units/:suid/branch-prices/:branchId", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), h.SaleUnitBranchPriceUpdateAPI)
	api.Delete("/products/:id/sale-units/:suid/branch-prices/:branchId", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), h.SaleUnitBranchPriceDeleteAPI)
	// Precio por sucursal de un producto "normal" (sin unidades de venta): mismo permiso/patrón
	// que sale-units/branch-prices, pero cuelga directo del producto (sin unidad de venta de por
	// medio) para no activar el selector de "elegir unidad" del POS.
	api.Get("/products/:id/branch-prices", middleware.RequireModule("products"), middleware.RequirePermission("products.view"), h.ProductBranchPriceListAPI)
	api.Get("/products/:id/branch-prices/:branchId", middleware.RequireModule("products"), middleware.RequirePermission("products.view"), h.ProductBranchPriceGetAPI)
	api.Post("/products/:id/branch-prices/:branchId", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), h.ProductBranchPriceCreateAPI)
	api.Put("/products/:id/branch-prices/:branchId", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), h.ProductBranchPriceUpdateAPI)
	api.Delete("/products/:id/branch-prices/:branchId", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), h.ProductBranchPriceDeleteAPI)
	// Atributos descriptivos de producto (Fase 5): mismo permiso/patrón que sale-units.
	api.Get("/products/:id/attributes", middleware.RequireModule("products"), middleware.RequirePermission("products.view"), h.ProductAttributeListAPI)
	api.Get("/products/:id/attributes/:attrId", middleware.RequireModule("products"), middleware.RequirePermission("products.view"), h.ProductAttributeGetAPI)
	api.Post("/products/:id/attributes", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), middleware.RequireGlobalCatalog(), h.ProductAttributeCreateAPI)
	api.Put("/products/:id/attributes/:attrId", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.ProductAttributeUpdateAPI)
	api.Delete("/products/:id/attributes/:attrId", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), middleware.RequireGlobalCatalog(), h.ProductAttributeDeleteAPI)
	api.Get("/categories",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.CategoryListAPI,
	)
	// Sin RequireGlobalCatalog: quien no administra el catálogo global igual gestiona las categorías de SU sucursal. El handler
	// (requireCategoryBranchAccess) permite las de su sucursal activa y rechaza las de «todas» o de otra sucursal.
	api.Post("/categories", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), h.CategoryCreateAPI)
	api.Put("/categories/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), h.CategoryUpdateAPI)
	api.Delete("/categories/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), h.CategoryDeleteAPI)
	api.Get("/brands",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.BrandListAPI,
	)
	api.Post("/brands", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), middleware.RequireGlobalCatalog(), h.BrandCreateAPI)
	api.Put("/brands/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.BrandUpdateAPI)
	api.Delete("/brands/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), middleware.RequireGlobalCatalog(), h.BrandDeleteAPI)
	api.Get("/units",
		middleware.RequireModule("products"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireProductsViewOrRestaurantCatalog(),
		h.UnitListAPI,
	)
	api.Post("/units", middleware.RequireModule("products"), middleware.RequirePermission("products.create"), middleware.RequireGlobalCatalog(), h.UnitCreateAPI)
	api.Put("/units/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.edit"), middleware.RequireGlobalCatalog(), h.UnitUpdateAPI)
	api.Delete("/units/:id", middleware.RequireModule("products"), middleware.RequirePermission("products.delete"), middleware.RequireGlobalCatalog(), h.UnitDeleteAPI)
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
