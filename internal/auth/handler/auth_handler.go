package handler

import (
	"errors"
	"strings"

	"tukifac/config"
	authservice "tukifac/internal/auth/service"
	reststaff "tukifac/internal/restaurant/staff"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/mastercode"
	"tukifac/pkg/middleware"
	"tukifac/pkg/tenantctx"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type AuthHandler struct{}

func NewAuthHandler() *AuthHandler {
	return &AuthHandler{}
}

func (h *AuthHandler) LoginPage(c fiber.Ctx) error {
	tenant, _ := c.Locals("tenant").(*database.Tenant)

	// Sin tenant en desarrollo → mostrar selector de empresas
	// Sin tenant en producción → redirigir a superadmin
	if tenant == nil {
		if config.AppConfig.IsDev() {
			return c.Redirect().To("/")
		}
		return c.Redirect().To("/superadmin/login")
	}

	return c.Render("auth/login", fiber.Map{
		"Title":      "Iniciar sesión",
		"TenantName": tenant.Name,
	})
}

func (h *AuthHandler) Logout(c fiber.Ctx) error {
	c.ClearCookie("token")
	return c.Redirect().To("/login")
}

func (h *AuthHandler) LoginAPI(c fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}

	tenantDB, _ := c.Locals("tenantDB").(*gorm.DB)
	tenant, _ := c.Locals("tenant").(*database.Tenant)
	if tenantDB == nil || tenant == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Contexto de empresa no encontrado"})
	}

	// Suspendidos/bloqueados pueden iniciar sesión para ver deuda y contactar soporte.
	subscriptionBlocked := tenant.Status != "active"

	user, legacyBranch, err := database.LoadTenantUserForBranchByEmail(tenantDB, req.Email)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Credenciales inválidas"})
	}

	if !user.CheckPassword(req.Password) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Credenciales inválidas"})
	}

	// Un único emisor arma claims, vigencias y firma (ver internal/auth/service/issuer.go).
	sess, err := authservice.DefaultIssuer().Issue(tenant, tenantDB, user, authservice.IssueOpts{
		Kind:        authservice.KindPwd,
		WithRefresh: true,
		Legacy:      legacyBranch,
	})
	if err != nil {
		if errors.Is(err, authservice.ErrUserInactive) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Credenciales inválidas"})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	claims := sess.Claims
	role := sess.Role
	subView := sess.Subscription
	enabledModules := claims.Modules
	permissionKeys := claims.Permissions
	employeeType := claims.EmployeeType
	staffID := claims.StaffID
	permVer := claims.PermVer
	activeBranchID := claims.ActiveBranchID

	subscriptionInfo := fiber.Map{
		"plan_name":             subView.PlanName,
		"status":                subView.Status,
		"tenant_status":         subView.TenantStatus,
		"end_date":              subView.EndDate,
		"start_date":            subView.StartDate,
		"days_until_expiry":     subView.DaysUntilExpiry,
		"can_operate":           subView.CanOperate,
		"is_blocked":            subView.IsBlocked,
		"strike_count":          subView.StrikeCount,
		"can_submit_payment":    subView.CanSubmitPayment,
		"provisional_until":     subView.ProvisionalUntil,
		"support_message":       subView.SupportMessage,
		"show_renewal_banner":   subView.ShowRenewalBanner,
		"show_suspended_banner": subView.ShowSuspendedBanner,
		"pending_amount":        subView.PendingAmount,
		"reconnection_fee":      subView.ReconnectionFee,
		"portal_url":            subView.PortalURL,
	}

	canSwitch := branch.UserCanSwitchBranch(tenantDB, role.Name, user)
	activeBrief, _ := branch.GetBranchBrief(tenantDB, activeBranchID)
	allowedBranches, _ := branch.ListUserBranchBriefs(tenantDB, user.ID)
	if branch.RoleViewsAll(tenantDB, user.RoleID, role.Name, "") {
		var all []database.TenantBranch
		tenantDB.Where("active = ?", true).Order("is_main DESC, name ASC").Find(&all)
		allowedBranches = make([]branch.BranchBrief, 0, len(all))
		for _, b := range all {
			allowedBranches = append(allowedBranches, branch.BranchBrief{ID: b.ID, Name: b.Name, IsMain: b.IsMain})
		}
	}

	restPerms := []string(nil)
	if employeeType != "" {
		restPerms, _ = reststaff.New(tenantDB).ResolvePermissionKeys(tenant.Slug, tenant.ID, user.ID, permVer)
	}

	return c.JSON(fiber.Map{
		"token":         sess.Token,
		"refresh_token": sess.RefreshToken,
		"user": fiber.Map{
			"id":                user.ID,
			"name":              user.Name,
			"email":             user.Email,
			"role":              role.Name,
			"employee_type":     employeeType,
			"staff_id":          staffID,
			"home_branch_id":    activeBranchID,
			"can_switch_branch": canSwitch,
		},
		"active_branch":          activeBrief,
		"can_switch_branch":      canSwitch,
		"can_view_all":           branch.RoleViewsAll(tenantDB, user.RoleID, role.Name, ""),
		"allowed_branches":       allowedBranches,
		"modules":                enabledModules,
		"permissions":            permissionKeys,
		"restaurant_permissions": restPerms,
		"subscription":           subscriptionInfo,
		"subscription_blocked":   subscriptionBlocked,
	})
}

// RefreshAPI renueva el access token a partir de un refresh token válido. Reconstruye los claims
// desde la BD (rol, permisos, módulos, sucursal), reflejando cambios y validando que el usuario
// siga activo. No rota el refresh token: el cliente conserva el mismo.
func (h *AuthHandler) RefreshAPI(c fiber.Ctx) error {
	var req struct {
		RefreshToken string `json:"refresh_token"`
		// ActiveBranchID: sucursal con la que el cliente venía operando. Se respeta solo si el usuario puede
		// acceder a ella; si no, se vuelve a la base. Sin esto el refresh devolvía siempre a la sucursal base.
		ActiveBranchID uint `json:"active_branch_id"`
	}
	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}

	claims, err := middleware.ParseTenantRefreshToken(req.RefreshToken)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Refresh token inválido o expirado", "code": "REFRESH_INVALID"})
	}

	tenant, _ := c.Locals("tenant").(*database.Tenant)
	tenantDB, _ := c.Locals("tenantDB").(*gorm.DB)
	if tenant == nil || tenantDB == nil {
		t, terr := middleware.LookupTenantBySlug(claims.TenantSlug)
		if terr != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Empresa no encontrada", "code": "REFRESH_INVALID"})
		}
		tdb, derr := database.GetTenantDB(t.DBName)
		if derr != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Servicio temporalmente no disponible"})
		}
		tenant = t
		tenantDB = tdb
	}

	if tenant.ID != claims.TenantID || tenant.Slug != claims.TenantSlug {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Refresh token no corresponde a la empresa", "code": "REFRESH_INVALID"})
	}

	user, legacyBranch, err := database.LoadTenantUserForBranch(tenantDB, claims.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Usuario no válido", "code": "REFRESH_INVALID"})
		}
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Servicio temporalmente no disponible"})
	}
	if !user.Active {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Usuario desactivado", "code": "REFRESH_INVALID"})
	}

	// El refresh token lleva auth_method; el perfil (vigencia, permisos, Impersonated) sale de él en el emisor.
	// Hoy solo el login normal emite refresh (pwd).
	sess, err := authservice.DefaultIssuer().Issue(tenant, tenantDB, user, authservice.IssueOpts{
		Kind:   claims.AuthMethod,
		Legacy: legacyBranch,
	})
	if err != nil {
		if errors.Is(err, authservice.ErrUserInactive) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Usuario desactivado", "code": "REFRESH_INVALID"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "No se pudo renovar la sesión"})
	}

	token := sess.Token
	if want := req.ActiveBranchID; want > 0 && want != sess.Claims.ActiveBranchID && !legacyBranch {
		if allowedBranchForRefresh(tenantDB, user, sess.Claims.RoleName, want) {
			if re, rerr := authservice.DefaultIssuer().Reissue(tenantDB, sess.Claims, user, authservice.Change{ActiveBranchID: want}); rerr == nil {
				token = re.Token
			}
		}
	}

	return c.JSON(fiber.Map{
		"token":       token,
		"modules":     sess.Claims.Modules,
		"permissions": sess.Claims.Permissions,
	})
}

// allowedBranchForRefresh indica si el usuario puede conservar la sucursal wantID al renovar el token: debe estar
// activa y ser asignada (o el usuario ve todas las sucursales). Nunca confía en lo que pida el cliente.
func allowedBranchForRefresh(db *gorm.DB, user *database.TenantUser, roleName string, wantID uint) bool {
	var b database.TenantBranch
	if err := db.Where("id = ? AND active = ?", wantID, true).First(&b).Error; err != nil {
		return false
	}
	if branch.RoleViewsAll(db, user.RoleID, roleName, "") {
		return true
	}
	ok, err := branch.UserHasBranchAccess(db, user.ID, wantID)
	return err == nil && ok
}

// MasterExchangeAPI (POST /api/auth/master-exchange) canjea el código de UN SOLO USO del acceso maestro por el JWT del tenant.
// Es público (no hay sesión todavía) pero exige contexto de empresa por host y que el código se haya emitido para ESE tenant.
func (h *AuthHandler) MasterExchangeAPI(c fiber.Ctx) error {
	tenant, ok := tenantctx.Tenant(c)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Acceso no permitido sin contexto de empresa"})
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := c.Bind().Body(&body); err != nil || strings.TrimSpace(body.Code) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Código requerido"})
	}
	token, redeemed := mastercode.Redeem(c.Context(), tenant.Slug, strings.TrimSpace(body.Code))
	if !redeemed {
		database.WriteAuditLog(&database.AuditLog{TenantID: tenant.ID, Action: "master_access_exchange_failed", Entity: "tenant", EntityID: tenant.ID, IPAddress: c.IP()})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Código inválido o vencido", "code": "MASTER_CODE_INVALID"})
	}
	database.WriteAuditLog(&database.AuditLog{TenantID: tenant.ID, Action: "master_access_exchange", Entity: "tenant", EntityID: tenant.ID, IPAddress: c.IP()})
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"token": token})
}
