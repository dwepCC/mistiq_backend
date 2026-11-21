package handler

import (
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// Anti-escalada de privilegios de sucursal: quien administra usuarios o roles solo puede conceder acceso a sucursales
// que él mismo tiene, y solo quien ve todas las sucursales (branches.view_all) o administra sucursales
// (branches.manage) puede conceder acceso global.

func actorCanGrantGlobalBranchAccess(c fiber.Ctx) bool {
	if branch.IsBranchAdmin(c) {
		return true
	}
	claims, _ := c.Locals("tenant_claims").(*middleware.TenantClaims)
	return claims != nil && branch.HasPermission(claims.Permissions, branch.PermManage)
}

// denyBranchEscalationForUser valida el rol y las sucursales que se asignan a un usuario. Devuelve true si ya respondió.
func denyBranchEscalationForUser(c fiber.Ctx, roleID uint, branchID *uint, branchIDs []uint) bool {
	if actorCanGrantGlobalBranchAccess(c) {
		return false
	}
	db := tenantDB(c)
	if branch.RoleHasBranchPermission(db, roleID, "view_all") || branch.RoleHasBranchPermission(db, roleID, "manage") {
		_ = c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"code":  branch.CodeBranchForbidden,
			"error": "No puede asignar un rol con acceso a todas las sucursales",
		})
		return true
	}
	ids := append([]uint(nil), branchIDs...)
	if branchID != nil && *branchID > 0 {
		ids = append(ids, *branchID)
	}
	for _, id := range ids {
		if id > 0 && !branch.CanAccessBranch(c, id) {
			_ = c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":  branch.CodeBranchForbidden,
				"error": "No puede asignar una sucursal a la que usted no tiene acceso",
			})
			return true
		}
	}
	return false
}

// denyBranchEscalationForRole impide que quien no administra sucursales otorgue branches.view_all o branches.manage a un rol.
func denyBranchEscalationForRole(c fiber.Ctx, permissionIDs []uint) bool {
	if len(permissionIDs) == 0 || actorCanGrantGlobalBranchAccess(c) {
		return false
	}
	var n int64
	tenantDB(c).Model(&database.TenantPermission{}).
		Where("id IN ? AND module = ? AND action IN ?", permissionIDs, "branches", []string{"view_all", "manage"}).
		Count(&n)
	if n == 0 {
		return false
	}
	_ = c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"code":  branch.CodeBranchForbidden,
		"error": "No puede otorgar acceso a todas las sucursales o su administración",
	})
	return true
}
