package service

import (
	reststaff "tukifac/internal/restaurant/staff"
	usersvc "tukifac/internal/users/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas"

	"gorm.io/gorm"
)

// TenantSessionOpts opciones al emitir JWT de sesión tenant.
type TenantSessionOpts struct {
	AuthMethod   string // pwd | master_access | pin
	Impersonated bool
	// MasterActor* identifican al superadmin que abre el acceso maestro; solo se
	// llenan en ese flujo y viajan en el JWT para poder auditar a su nombre.
	MasterActorID    uint
	MasterActorEmail string
}

// TenantSessionResult token firmado y claims generados.
type TenantSessionResult struct {
	Token  string
	Claims *middleware.TenantClaims
}

// BuildTenantSession genera JWT de sesión tenant reutilizando la lógica del login normal.
func BuildTenantSession(
	tenant *database.Tenant,
	tenantDB *gorm.DB,
	user *database.TenantUser,
	legacyBranch bool,
	opts TenantSessionOpts,
) (*TenantSessionResult, error) {
	var role database.TenantRole
	if err := tenantDB.First(&role, user.RoleID).Error; err != nil {
		return nil, err
	}

	roleSvc := usersvc.NewRoleService(tenantDB)
	permissionKeys, _ := roleSvc.GetRolePermissionKeys(user.RoleID)
	if permissionKeys == nil {
		permissionKeys = []string{}
	}

	enabledModules := make([]string, 0)
	var tms []database.TenantModule
	database.CentralDB.
		Where("tenant_id = ? AND enabled = ?", tenant.ID, true).
		Find(&tms)
	for _, m := range tms {
		enabledModules = append(enabledModules, m.ModuleKey)
	}

	subView, _ := saas.GetTenantView(tenant.ID)
	planID := subView.PlanID

	hasRestaurant := false
	for _, m := range enabledModules {
		if m == "restaurant" {
			hasRestaurant = true
			break
		}
	}
	employeeType := ""
	staffID := uint(0)
	permVer := uint(0)
	if hasRestaurant {
		staffSvc := reststaff.New(tenantDB)
		permVer, _ = staffSvc.GetPermCacheVersion()
		if st, err := staffSvc.GetStaffByUserID(user.ID); err == nil && st.IsActive {
			employeeType = st.EmployeeType
			staffID = st.ID
		}
	}

	if !legacyBranch {
		branch.SyncUserBranchFields(user, role.Name)
		if user.HomeBranchID == nil || (user.HomeBranchID != nil && *user.HomeBranchID == 0) {
			if hid, err := branch.ResolveHomeBranchID(tenantDB, user); err == nil {
				hb := hid
				user.HomeBranchID = &hb
				user.BranchID = &hb
				_ = database.PersistUserBranchFieldsOnLogin(tenantDB, user.ID, hb, branch.CanSwitchBranch(role.Name, user))
			}
		}
	}

	activeBranchID, err := branch.ResolveUserSessionBranchID(tenantDB, user)
	if err != nil {
		return nil, err
	}

	sessionVersion := uint(0)
	if !legacyBranch {
		sessionVersion = user.BranchSessionVersion
	}

	authMethod := opts.AuthMethod
	if authMethod == "" {
		authMethod = "pwd"
	}

	claims := &middleware.TenantClaims{
		UserID:               user.ID,
		Email:                user.Email,
		RoleID:               user.RoleID,
		RoleName:             role.Name,
		TenantSlug:           tenant.Slug,
		TenantDB:             tenant.DBName,
		TenantID:             tenant.ID,
		TenantVersion:        middleware.CurrentTenantJWTVersion(),
		PlanID:               planID,
		Modules:              enabledModules,
		Permissions:          permissionKeys,
		EmployeeType:         employeeType,
		AuthMethod:           authMethod,
		Impersonated:         opts.Impersonated,
		MasterActorID:        opts.MasterActorID,
		MasterActorEmail:     opts.MasterActorEmail,
		PermVer:              permVer,
		StaffID:              staffID,
		Status:               tenant.Status,
		Type:                 "tenant",
		ActiveBranchID:       activeBranchID,
		BranchSessionVersion: sessionVersion,
	}

	tokenString, err := middleware.BuildTenantToken(claims, middleware.PasswordSessionTTL)
	if err != nil {
		return nil, err
	}

	return &TenantSessionResult{
		Token:  tokenString,
		Claims: claims,
	}, nil
}
