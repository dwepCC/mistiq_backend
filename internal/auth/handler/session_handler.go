package handler

import (
	"time"

	"tukifac/config"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

type SessionHandler struct{}

func NewSessionHandler() *SessionHandler {
	return &SessionHandler{}
}

func sessionTenantDB(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

// GET /api/session/capabilities — feature flags según schema del tenant.
func (h *SessionHandler) GetCapabilities(c fiber.Ctx) error {
	tdb := sessionTenantDB(c)
	if tdb == nil {
		return c.Status(400).JSON(fiber.Map{"error": "Sin contexto"})
	}
	ver := database.CurrentSchemaVersion(tdb)
	return c.JSON(fiber.Map{
		"schema_version": ver,
		"features":       database.TenantFeatureFlags(tdb),
	})
}

// GET /api/session/modules — catálogo de módulos IMPLEMENTADOS con enabled por tenant + uso/
// límites del plan. Lo consumen tukifac y tukichef para mostrar módulos bloqueados con upsell
// (los NO implementados se ocultan) y avisar cuando el tenant se acerca a un tope.
func (h *SessionHandler) GetModules(c fiber.Ctx) error {
	tdb := sessionTenantDB(c)
	claims, _ := c.Locals("tenant_claims").(*middleware.TenantClaims)
	if tdb == nil || claims == nil {
		return c.Status(400).JSON(fiber.Map{"error": "Sin contexto"})
	}

	// Solo módulos implementados y activos (los no implementados no se muestran al tenant).
	var catalog []database.SaasModule
	database.CentralDB.Where("implemented = ? AND active = ?", true, true).
		Order("sort_order asc, id asc").Find(&catalog)

	// Habilitados del tenant: fuente actual en central (refleja reconciliaciones al instante).
	enabled := map[string]bool{}
	var tms []database.TenantModule
	database.CentralDB.Where("tenant_id = ? AND enabled = ?", claims.TenantID, true).Find(&tms)
	for _, tm := range tms {
		enabled[tm.ModuleKey] = true
	}

	type moduleView struct {
		Key         string `json:"key"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
		Category    string `json:"category"`
		Enabled     bool   `json:"enabled"`
	}
	modules := make([]moduleView, 0, len(catalog))
	for _, m := range catalog {
		modules = append(modules, moduleView{
			Key: m.Key, Name: m.Name, Description: m.Description, Icon: m.Icon,
			Category: m.Category, Enabled: enabled[m.Key],
		})
	}

	// Uso actual en la BD del tenant (seats activos / productos vivos).
	var usersUsed, branchesUsed, productsUsed int64
	tdb.Model(&database.TenantUser{}).Where("active = ?", true).Count(&usersUsed)
	tdb.Model(&database.TenantBranch{}).Where("active = ?", true).Count(&branchesUsed)
	tdb.Model(&database.TenantProduct{}).Count(&productsUsed)

	limits := saas.TenantPlanLimits(claims.TenantID)
	type limitView struct {
		Used      int64 `json:"used"`
		Max       int   `json:"max"`
		Unlimited bool  `json:"unlimited"`
	}
	return c.JSON(fiber.Map{
		"modules": modules,
		"limits": fiber.Map{
			"users":     limitView{Used: usersUsed, Max: limits.MaxUsers, Unlimited: limits.MaxUsers <= 0},
			"branches":  limitView{Used: branchesUsed, Max: limits.MaxBranches, Unlimited: limits.MaxBranches <= 0},
			"products":  limitView{Used: productsUsed, Max: limits.MaxProducts, Unlimited: limits.MaxProducts <= 0},
			"documents": limitView{Max: limits.MaxDocuments, Unlimited: limits.UnlimitedDocuments},
		},
	})
}

// GET /api/session/context
func (h *SessionHandler) GetContext(c fiber.Ctx) error {
	tdb := sessionTenantDB(c)
	claims, _ := c.Locals("tenant_claims").(*middleware.TenantClaims)
	if tdb == nil || claims == nil {
		return c.Status(400).JSON(fiber.Map{"error": "Sin contexto"})
	}
	activeID := branch.ActiveBranchID(c)
	if activeID == 0 {
		activeID = claims.ActiveBranchID
	}
	brief, _ := branch.GetBranchBrief(tdb, activeID)
	user, _, _ := database.LoadTenantUserForBranch(tdb, claims.UserID)
	canSwitch := false
	sessionVer := uint(0)
	allowedBranches := []branch.BranchBrief(nil)
	if user != nil {
		canSwitch = branch.UserCanSwitchBranch(tdb, claims.RoleName, user)
		if database.TenantBranchSessionVersionReady(tdb) {
			sessionVer = user.BranchSessionVersion
		}
		if branch.IsTenantAdmin(claims.RoleName) {
			var all []database.TenantBranch
			tdb.Where("active = ?", true).Order("is_main DESC, name ASC").Find(&all)
			for _, b := range all {
				allowedBranches = append(allowedBranches, branch.BranchBrief{ID: b.ID, Name: b.Name, IsMain: b.IsMain})
			}
		} else {
			allowedBranches, _ = branch.ListUserBranchBriefs(tdb, user.ID)
		}
	}
	return c.JSON(fiber.Map{
		"active_branch":          brief,
		"can_switch_branch":      canSwitch,
		"allowed_branches":       allowedBranches,
		"branch_session_version": sessionVer,
	})
}

// POST /api/session/switch-branch
func (h *SessionHandler) SwitchBranch(c fiber.Ctx) error {
	tdb := sessionTenantDB(c)
	claims, _ := c.Locals("tenant_claims").(*middleware.TenantClaims)
	if tdb == nil || claims == nil {
		return c.Status(400).JSON(fiber.Map{"error": "Sin contexto"})
	}
	var body struct {
		BranchID uint `json:"branch_id"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.BranchID == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "branch_id requerido"})
	}

	user, legacy, err := database.LoadTenantUserForBranch(tdb, claims.UserID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Usuario no encontrado"})
	}
	if legacy || !database.TenantBranchMultiSchemaReady(tdb) {
		return c.Status(503).JSON(fiber.Map{
			"error":   "Migración de sucursales pendiente",
			"message": "Ejecute migrate en el tenant antes de cambiar sucursal",
		})
	}
	if !branch.UserCanSwitchBranch(tdb, claims.RoleName, user) {
		return c.Status(403).JSON(fiber.Map{"error": "No tiene permiso para cambiar de sucursal"})
	}

	var b database.TenantBranch
	if err := tdb.Where("id = ? AND active = ?", body.BranchID, true).First(&b).Error; err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Sucursal no válida"})
	}
	if !branch.IsTenantAdmin(claims.RoleName) {
		ok, err := branch.UserHasBranchAccess(tdb, user.ID, body.BranchID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		if !ok {
			return c.Status(403).JSON(fiber.Map{"error": "No tiene acceso a esta sucursal"})
		}
	}

	tokenString, brief, err := issueTokenWithBranch(tdb, claims, user, b.ID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	allowed, _ := branch.ListUserBranchBriefs(tdb, user.ID)
	if branch.IsTenantAdmin(claims.RoleName) {
		var all []database.TenantBranch
		tdb.Where("active = ?", true).Order("is_main DESC, name ASC").Find(&all)
		allowed = make([]branch.BranchBrief, 0, len(all))
		for _, br := range all {
			allowed = append(allowed, branch.BranchBrief{ID: br.ID, Name: br.Name, IsMain: br.IsMain})
		}
	}
	return c.JSON(fiber.Map{
		"token":             tokenString,
		"active_branch":     brief,
		"can_switch_branch": branch.UserCanSwitchBranch(tdb, claims.RoleName, user),
		"allowed_branches":  allowed,
	})
}

func branchSessionVersionForToken(tdb *gorm.DB, user *database.TenantUser) uint {
	if user == nil || !database.TenantBranchSessionVersionReady(tdb) {
		return 0
	}
	return user.BranchSessionVersion
}

func issueTokenWithBranch(tdb *gorm.DB, old *middleware.TenantClaims, user *database.TenantUser, activeBranchID uint) (string, *branch.BranchBrief, error) {
	brief, err := branch.GetBranchBrief(tdb, activeBranchID)
	if err != nil {
		return "", nil, err
	}
	claims := &middleware.TenantClaims{
		UserID:               old.UserID,
		Email:                old.Email,
		RoleID:               old.RoleID,
		RoleName:             old.RoleName,
		TenantSlug:           old.TenantSlug,
		TenantDB:             old.TenantDB,
		TenantID:             old.TenantID,
		TenantVersion:        old.TenantVersion,
		PlanID:               old.PlanID,
		Modules:              old.Modules,
		Permissions:          old.Permissions,
		EmployeeType:         old.EmployeeType,
		AuthMethod:           old.AuthMethod,
		Impersonated:         old.Impersonated,
		MasterActorID:        old.MasterActorID,
		MasterActorEmail:     old.MasterActorEmail,
		PermVer:              old.PermVer,
		StaffID:              old.StaffID,
		Status:               old.Status,
		Type:                 "tenant",
		ActiveBranchID:       activeBranchID,
		BranchSessionVersion: branchSessionVersionForToken(tdb, user),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(config.AppConfig.JWTSecret))
	return tokenString, brief, err
}
