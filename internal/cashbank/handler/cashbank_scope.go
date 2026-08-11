package handler

import (
	"strings"

	"tukifac/internal/cashbank/service"
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

func filterSessionsForCaller(c fiber.Ctx, items []service.CashSessionListItem) []service.CashSessionListItem {
	if canManageAnyCashSession(c) {
		return items
	}
	uid := userID(c)
	if uid == 0 {
		return nil
	}
	out := make([]service.CashSessionListItem, 0, len(items))
	for _, it := range items {
		if it.OpenedBy == uid {
			out = append(out, it)
		}
	}
	return out
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
