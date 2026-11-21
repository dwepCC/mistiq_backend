package handler

import (
	"errors"
	"strings"

	authservice "tukifac/internal/auth/service"
	"tukifac/internal/restaurant/staff"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// GET /api/restaurant/auth/config — sin JWT; solo tenant + flags (ligero).
func (h *RestaurantHandler) AuthConfig(c fiber.Ctx) error {
	tenant, _ := c.Locals("tenant").(*database.Tenant)
	if tenant == nil {
		return c.Status(400).JSON(fiber.Map{"error": "empresa no encontrada"})
	}
	tenantDB, _ := c.Locals("tenantDB").(*gorm.DB)
	pinEnabled := false
	if tenantDB != nil {
		var n int64
		tenantDB.Model(&database.TenantRestaurantStaff{}).Where("is_active = ? AND pin_hash != ''", true).Count(&n)
		pinEnabled = n > 0
	}
	return c.JSON(fiber.Map{
		"tenant_name":       tenant.Name,
		"tenant_slug":       tenant.Slug,
		"pin_login_enabled": pinEnabled,
	})
}

// POST /api/restaurant/auth/pin — JWT liviano sin permissions[].
func (h *RestaurantHandler) PinLogin(c fiber.Ctx) error {
	var body struct {
		Pin     string `json:"pin"`
		Station string `json:"station"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
	}
	tenant, _ := c.Locals("tenant").(*database.Tenant)
	tenantDB, _ := c.Locals("tenantDB").(*gorm.DB)
	if tenant == nil || tenantDB == nil {
		return c.Status(400).JSON(fiber.Map{"error": "contexto de empresa requerido"})
	}
	view, err := saas.GetTenantView(tenant.ID)
	if err != nil || !view.CanOperate {
		return c.Status(402).JSON(fiber.Map{"error": "operación suspendida por suscripción"})
	}

	staffSvc := staff.New(tenantDB)
	userID, staffID, employeeType, err := staffSvc.AuthenticatePIN(body.Pin, strings.TrimSpace(body.Station))
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"error": err.Error()})
	}

	var user database.TenantUser
	if err := tenantDB.First(&user, userID).Error; err != nil || !user.Active {
		return c.Status(401).JSON(fiber.Map{"error": "usuario inactivo"})
	}
	var role database.TenantRole
	tenantDB.First(&role, user.RoleID)

	// Un único emisor arma claims (sin Permissions: perfil "pin"), fija 12 h y firma. Exige módulo restaurante y personal activo.
	sess, err := authservice.DefaultIssuer().Issue(tenant, tenantDB, &user, authservice.IssueOpts{Kind: authservice.KindPin})
	if err != nil {
		if errors.Is(err, authservice.ErrSessionNoLongerValid) {
			return c.Status(403).JSON(fiber.Map{"error": "módulo restaurante no habilitado"})
		}
		return c.Status(500).JSON(fiber.Map{"error": "error generando sesión"})
	}
	claims := sess.Claims
	enabledModules := claims.Modules
	permVer := claims.PermVer
	activeBranchID := claims.ActiveBranchID
	canSwitch := branch.UserCanSwitchBranchAuth(tenantDB, role.Name, &user, "pin")
	allowedBranches, _ := branch.ListUserBranchBriefs(tenantDB, user.ID)
	tokenString := sess.Token
	activeBrief, _ := branch.GetBranchBrief(tenantDB, activeBranchID)
	keys, _ := staffSvc.ResolvePermissionKeys(tenant.Slug, tenant.ID, userID, permVer)

	return c.JSON(fiber.Map{
		"token": tokenString,
		"user": fiber.Map{
			"id":                user.ID,
			"name":              user.Name,
			"email":             user.Email,
			"role":              role.Name,
			"employee_type":     employeeType,
			"auth_method":       "pin",
			"staff_id":          staffID,
			"home_branch_id":    activeBranchID,
			"can_switch_branch": canSwitch,
		},
		"active_branch":          activeBrief,
		"can_switch_branch":      canSwitch,
		"allowed_branches":       allowedBranches,
		"modules":                enabledModules,
		"restaurant_permissions": keys,
	})
}

// GET /api/restaurant/session/permissions — permisos efectivos (cache).
func (h *RestaurantHandler) SessionPermissions(c fiber.Ctx) error {
	claims, ok := c.Locals("tenant_claims").(*middleware.TenantClaims)
	if !ok || claims == nil {
		return c.Status(403).JSON(fiber.Map{"error": "sin sesión"})
	}
	tenantDB, _ := c.Locals("tenantDB").(*gorm.DB)
	if tenantDB == nil {
		return c.Status(500).JSON(fiber.Map{"error": "sin base de datos"})
	}
	staffSvc := staff.New(tenantDB)
	keys, err := staffSvc.ResolvePermissionKeys(claims.TenantSlug, claims.TenantID, claims.UserID, claims.PermVer)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"permissions":     keys,
		"employee_type":   claims.EmployeeType,
		"auth_method":     claims.AuthMethod,
		"staff_id":        claims.StaffID,
	})
}

func moduleKeysForTenant(tenantID uint) []string {
	out := make([]string, 0)
	var tms []database.TenantModule
	database.CentralDB.Where("tenant_id = ? AND enabled = ?", tenantID, true).Find(&tms)
	for _, m := range tms {
		out = append(out, m.ModuleKey)
	}
	return out
}
