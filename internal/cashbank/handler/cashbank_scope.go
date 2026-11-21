package handler

import (
	"strings"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

// canManageAnyCashSession admin tenant, cashbank.manage o supervisor restaurante (s.m).
func canManageAnyCashSession(c fiber.Ctx) bool {
	if claims, ok := c.Locals("tenant_claims").(*middleware.TenantClaims); ok && claims != nil {
		if claims.RoleName == "Administrador" {
			return true
		}
		for _, p := range claims.Permissions {
			if p == "cashbank.manage" {
				return true
			}
		}
	}
	return middleware.HasRestaurantPerm(c, restaurantperm.SettingsManage)
}

func canAccessCashSession(c fiber.Ctx, sess *database.TenantCashSession) bool {
	if sess == nil {
		return false
	}
	if canManageAnyCashSession(c) {
		return true
	}
	return sess.OpenedBy == userID(c)
}

// sessionReadableInBranch: la sesión es de una sucursal que el usuario puede ver (sin sucursal = del tenant).
// Si no, el handler responde 404 como si no existiera (no revela que existe en otra sucursal).
func sessionReadableInBranch(c fiber.Ctx, sess *database.TenantCashSession) bool {
	return sess != nil && branch.RecordReadable(c, sess.BranchID)
}

// sessionWritableInBranch: para operar (movimientos, cierre, arqueo, borrar) la sesión debe ser de la
// sucursal ACTIVA; un administrador que ve todas debe cambiar de sucursal para operar otra.
func sessionWritableInBranch(c fiber.Ctx, sess *database.TenantCashSession) bool {
	return sess != nil && branch.RecordWritable(c, sess.BranchID)
}

// sessionWriteDenied centraliza el guard de escritura sobre una sesión: no visible (o inexistente) -> 404;
// visible pero de otra sucursal que la activa (admin viendo todas) -> 403 BRANCH_FORBIDDEN.
func sessionWriteDenied(c fiber.Ctx, sess *database.TenantCashSession) (bool, error) {
	if !sessionReadableInBranch(c, sess) {
		return true, c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Sesión no encontrada"})
	}
	if !sessionWritableInBranch(c, sess) {
		return true, c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Solo puede operar cajas de su sucursal activa; cambie de sucursal", "code": branch.CodeBranchForbidden,
		})
	}
	return false, nil
}

func callerUserIDOrZero(c fiber.Ctx) uint {
	if canManageAnyCashSession(c) {
		return 0
	}
	return userID(c)
}

// canViewBankAccountBalances solo el administrador del restaurante ve saldos de cuentas/billeteras.
func canViewBankAccountBalances(c fiber.Ctx) bool {
	if et, ok := c.Locals("employee_type").(string); ok && strings.EqualFold(strings.TrimSpace(et), "admin") {
		return true
	}
	if claims, ok := c.Locals("tenant_claims").(*middleware.TenantClaims); ok && claims != nil {
		return claims.RoleName == "Administrador"
	}
	return false
}

func maskBankAccountBalances(c fiber.Ctx, accounts []database.TenantBankAccount) []database.TenantBankAccount {
	if canViewBankAccountBalances(c) {
		return accounts
	}
	out := make([]database.TenantBankAccount, len(accounts))
	copy(out, accounts)
	for i := range out {
		out[i].Balance = 0
	}
	return out
}

func maskBankAccountBalance(c fiber.Ctx, acc *database.TenantBankAccount) *database.TenantBankAccount {
	if acc == nil || canViewBankAccountBalances(c) {
		return acc
	}
	copy := *acc
	copy.Balance = 0
	return &copy
}
