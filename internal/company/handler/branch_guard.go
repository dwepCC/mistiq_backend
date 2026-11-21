package handler

import (
	"errors"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Aislamiento por sucursal en Empresa (Fase 3A): series de comprobante y mutaciones de sucursales.
// Son configuración, no operación: quien administra el tenant (ve todas las sucursales) puede configurar cualquiera;
// el resto, solo las sucursales a las que tiene acceso. Nadie escribe en modo "todas las sucursales" (sin activa).

func guardNotFound(c fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
}

func guardForbidden(c fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": msg, "code": branch.CodeBranchForbidden})
}

// guardConfigBranch: la sucursal debe ser accesible (404 si no) y debe haber sucursal activa para escribir (403).
func guardConfigBranch(c fiber.Ctx, branchID uint, notFoundMsg string) (bool, error) {
	if !branch.CanAccessBranch(c, branchID) {
		return false, guardNotFound(c, notFoundMsg)
	}
	if branch.ActiveBranchID(c) == 0 {
		return false, guardForbidden(c, "Seleccione una sucursal activa para modificar la configuración")
	}
	return true, nil
}

// guardSeriesByID: la serie debe pertenecer a una sucursal accesible.
func guardSeriesByID(c fiber.Ctx, seriesID uint) (bool, error) {
	var s database.TenantDocumentSeries
	if err := db(c).Select("id", "branch_id").First(&s, seriesID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, guardNotFound(c, "serie no encontrada")
		}
		return false, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return guardConfigBranch(c, s.BranchID, "serie no encontrada")
}

// guardAdminOnly: operaciones que afectan a todo el tenant (crear/eliminar sucursales).
func guardAdminOnly(c fiber.Ctx) (bool, error) {
	if branch.IsBranchAdmin(c) {
		return true, nil
	}
	if claims, _ := c.Locals("tenant_claims").(*middleware.TenantClaims); claims != nil && branch.HasPermission(claims.Permissions, branch.PermManage) {
		return true, nil
	}
	return false, guardForbidden(c, "Solo quien administra todas las sucursales puede realizar esta acción")
}
