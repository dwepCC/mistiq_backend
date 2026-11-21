package service

import (
	"errors"
	"strings"
	"time"

	"tukifac/config"
	reststaff "tukifac/internal/restaurant/staff"
	usersvc "tukifac/internal/users/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// Este archivo es el ÚNICO lugar del backend de tenant que arma claims, fija vigencias y firma JWT
// (access y refresh). Un test guardián (issuer_guard_test.go) falla si aparece SignedString( o
// jwt.NewWithClaims( en cualquier otro archivo de producción (el superadmin tiene su propio JWT).
//
// Dos operaciones:
//   - Issue: primer token de una sesión (login, PIN, acceso maestro, refresh). Lee BD y arma TODO.
//   - Reissue: cambio de sucursal de una sesión ya abierta. Vuelve a leer rol, permisos, módulos, plan
//     y estado desde la BD (no copia a ciegas los del token viejo), cambia solo la sucursal activa y
//     la versión de sesión, conserva Kind/Impersonated/MasterActor* y NUNCA extiende exp.

// Perfiles de sesión (Profile.Kind coincide con claims.AuthMethod).
const (
	KindPwd          = "pwd"
	KindPin          = "pin"
	KindMasterAccess = middleware.AuthMethodMasterAccess
)

// RefreshTokenTTL vida del refresh token (30 días): mientras el usuario siga activo su sesión se renueva
// sin pedir contraseña. Sin rotación ni almacenamiento (ver docs/aislamiento-sucursal/DEUDA_TECNICA.md §2.3).
const RefreshTokenTTL = 30 * 24 * time.Hour

// Profile describe un tipo de sesión. Es la ÚNICA fuente de vigencias.
type Profile struct {
	Kind string
	// TTL vigencia del access token emitido por Issue.
	TTL time.Duration
	// WithPerms: el JWT lleva Permissions (false en PIN: el POS resuelve permisos de Tukichef aparte).
	WithPerms bool
	// IssueRefresh: Issue puede emitir refresh token (solo pwd).
	IssueRefresh bool
	// SyncHomeBranch: normaliza/persiste la sucursal base del usuario al abrir sesión (el PIN no escribe).
	SyncHomeBranch bool
}

// Reissue no tiene bandera de "extender": para TODOS los perfiles conserva el exp del token anterior
// (nunca más que ahora+TTL). Así encadenar cambios de sucursal no prolonga ninguna sesión; quien renueva
// la sesión pwd es el refresh (que además vuelve a validar usuario activo y relee todo desde la BD).
var profiles = map[string]Profile{
	KindPwd:          {Kind: KindPwd, TTL: 24 * time.Hour, WithPerms: true, IssueRefresh: true, SyncHomeBranch: true},
	KindPin:          {Kind: KindPin, TTL: 12 * time.Hour, WithPerms: false, IssueRefresh: false, SyncHomeBranch: false},
	KindMasterAccess: {Kind: KindMasterAccess, TTL: 24 * time.Hour, WithPerms: true, IssueRefresh: false, SyncHomeBranch: true},
}

// ProfileFor devuelve el perfil de un auth_method ("" equivale a pwd).
func ProfileFor(kind string) (Profile, bool) {
	if strings.TrimSpace(kind) == "" {
		kind = KindPwd
	}
	p, ok := profiles[kind]
	return p, ok
}

var (
	ErrUnknownProfile = errors.New("tipo de sesión desconocido")
	ErrUserInactive   = errors.New("usuario inactivo")
	// ErrSessionNoLongerValid la sesión (p. ej. PIN) ya no cumple sus condiciones: módulo restaurante o personal inactivo.
	ErrSessionNoLongerValid = errors.New("la sesión ya no es válida")
)

// IssueOpts opciones de Issue.
type IssueOpts struct {
	Kind string // pwd | pin | master_access ("" = pwd)
	// MasterActor* identifican al superadmin que abre el acceso maestro; solo con Kind master_access.
	MasterActorID    uint
	MasterActorEmail string
	// WithRefresh pide además un refresh token (solo se emite si el perfil lo permite).
	WithRefresh bool
	// Legacy: el tenant aún no tiene el esquema multi-sucursal (valor devuelto por LoadTenantUserForBranch*).
	Legacy bool
}

// Change cambios admitidos en Reissue.
type Change struct {
	ActiveBranchID uint
}

// Session resultado de Issue/Reissue.
type Session struct {
	Token        string
	RefreshToken string // vacío salvo Issue con WithRefresh y perfil con refresh
	Claims       *middleware.TenantClaims
	Role         database.TenantRole
	Subscription saas.TenantSubscriptionView
}

// Issuer emite tokens tenant. Now es inyectable para pruebas.
type Issuer struct {
	Now func() time.Time
}

// DefaultIssuer emisor de producción. El secreto se lee de config.AppConfig en cada firma.
func DefaultIssuer() *Issuer { return &Issuer{Now: time.Now} }

func (i *Issuer) now() time.Time {
	if i != nil && i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

// sign única función de firma de tokens tenant (HS256, secreto común a access y refresh).
func (i *Issuer) sign(claims jwt.Claims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.AppConfig.JWTSecret))
}

// sessionData todo lo que se lee de la BD para armar claims; lo comparten Issue y Reissue.
type sessionData struct {
	role          database.TenantRole
	permissions   []string
	modules       []string
	sub           saas.TenantSubscriptionView
	hasRestaurant bool
	employeeType  string
	staffID       uint
	permVer       uint
}

func loadSessionData(tenant *database.Tenant, tdb *gorm.DB, user *database.TenantUser, p Profile) (*sessionData, error) {
	d := &sessionData{}
	if err := tdb.First(&d.role, user.RoleID).Error; err != nil {
		return nil, err
	}
	if p.WithPerms {
		keys, _ := usersvc.NewRoleService(tdb).GetRolePermissionKeys(user.RoleID)
		if keys == nil {
			keys = []string{}
		}
		d.permissions = keys
	}

	d.modules = make([]string, 0)
	var tms []database.TenantModule
	database.CentralDB.Where("tenant_id = ? AND enabled = ?", tenant.ID, true).Find(&tms)
	for _, m := range tms {
		d.modules = append(d.modules, m.ModuleKey)
		if m.ModuleKey == "restaurant" {
			d.hasRestaurant = true
		}
	}

	d.sub, _ = saas.GetTenantView(tenant.ID)

	if d.hasRestaurant {
		staffSvc := reststaff.New(tdb)
		d.permVer, _ = staffSvc.GetPermCacheVersion()
		if st, err := staffSvc.GetStaffByUserID(user.ID); err == nil && st.IsActive {
			d.employeeType = st.EmployeeType
			d.staffID = st.ID
		}
	}
	return d, nil
}

// buildClaims ÚNICO constructor de claims. Issue y Reissue lo usan con los mismos argumentos de lectura;
// solo difieren en quién aporta sucursal, exp y la identidad de la sesión.
func buildClaims(tenant *database.Tenant, tdb *gorm.DB, user *database.TenantUser, d *sessionData,
	kind string, impersonated bool, actorID uint, actorEmail string, activeBranchID uint) *middleware.TenantClaims {
	sessionVersion := uint(0)
	if database.TenantBranchSessionVersionReady(tdb) {
		sessionVersion = user.BranchSessionVersion
	}
	return &middleware.TenantClaims{
		UserID:               user.ID,
		Email:                user.Email,
		RoleID:               user.RoleID,
		RoleName:             d.role.Name,
		TenantSlug:           tenant.Slug,
		TenantDB:             tenant.DBName,
		TenantID:             tenant.ID,
		TenantVersion:        middleware.CurrentTenantJWTVersion(),
		PlanID:               d.sub.PlanID,
		Modules:              d.modules,
		Permissions:          d.permissions,
		EmployeeType:         d.employeeType,
		AuthMethod:           kind,
		Impersonated:         impersonated,
		MasterActorID:        actorID,
		MasterActorEmail:     actorEmail,
		PermVer:              d.permVer,
		StaffID:              d.staffID,
		Status:               tenant.Status,
		Type:                 "tenant",
		ActiveBranchID:       activeBranchID,
		BranchSessionVersion: sessionVersion,
	}
}

// Issue emite el primer access token de una sesión (y opcionalmente su refresh).
func (i *Issuer) Issue(tenant *database.Tenant, tdb *gorm.DB, user *database.TenantUser, opts IssueOpts) (*Session, error) {
	kind := opts.Kind
	if strings.TrimSpace(kind) == "" {
		kind = KindPwd
	}
	p, ok := ProfileFor(kind)
	if !ok {
		return nil, ErrUnknownProfile
	}
	if !user.Active {
		return nil, ErrUserInactive
	}

	d, err := loadSessionData(tenant, tdb, user, p)
	if err != nil {
		return nil, err
	}
	if kind == KindPin && (!d.hasRestaurant || d.employeeType == "") {
		return nil, ErrSessionNoLongerValid
	}

	if p.SyncHomeBranch && !opts.Legacy {
		branch.SyncUserBranchFields(user, d.role.Name)
		if user.HomeBranchID == nil || *user.HomeBranchID == 0 {
			if hid, err := branch.ResolveHomeBranchID(tdb, user); err == nil {
				hb := hid
				user.HomeBranchID = &hb
				user.BranchID = &hb
				_ = database.PersistUserBranchFieldsOnLogin(tdb, user.ID, hb, branch.CanSwitchBranch(d.role.Name, user))
			}
		}
	}

	activeBranchID, err := branch.ResolveUserSessionBranchID(tdb, user)
	if err != nil {
		return nil, err
	}

	claims := buildClaims(tenant, tdb, user, d, kind, kind == KindMasterAccess, opts.MasterActorID, opts.MasterActorEmail, activeBranchID)
	now := i.now()
	claims.RegisteredClaims = jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(p.TTL)),
	}
	token, err := i.sign(claims)
	if err != nil {
		return nil, err
	}
	sess := &Session{Token: token, Claims: claims, Role: d.role, Subscription: d.sub}
	if opts.WithRefresh && p.IssueRefresh {
		sess.RefreshToken, _ = i.signRefresh(tenant, user.ID, kind)
	}
	return sess, nil
}

// Reissue emite un access token nuevo para el cambio de sucursal de una sesión abierta. `user` es el usuario
// recién cargado de la BD. Relee todo lo demás; rechaza usuarios inactivos. exp = exp del token anterior
// (acotado a ahora+TTL del perfil): ninguna sesión se alarga encadenando cambios de sucursal.
func (i *Issuer) Reissue(tdb *gorm.DB, old *middleware.TenantClaims, user *database.TenantUser, change Change) (*Session, error) {
	if old == nil || user == nil {
		return nil, errors.New("sesión inválida")
	}
	if !user.Active {
		return nil, ErrUserInactive
	}
	if change.ActiveBranchID == 0 {
		return nil, errors.New("sucursal requerida")
	}
	kind := old.AuthMethod
	if strings.TrimSpace(kind) == "" {
		kind = KindPwd
	}
	p, ok := ProfileFor(kind)
	if !ok {
		return nil, ErrUnknownProfile
	}

	var tenant database.Tenant
	if err := database.CentralDB.First(&tenant, old.TenantID).Error; err != nil {
		return nil, err
	}
	if tenant.Slug != old.TenantSlug || user.ID != old.UserID {
		return nil, errors.New("el token no corresponde a la empresa o al usuario")
	}

	d, err := loadSessionData(&tenant, tdb, user, p)
	if err != nil {
		return nil, err
	}
	if kind == KindPin && (!d.hasRestaurant || d.employeeType == "") {
		return nil, ErrSessionNoLongerValid
	}

	claims := buildClaims(&tenant, tdb, user, d, kind, old.Impersonated, old.MasterActorID, old.MasterActorEmail, change.ActiveBranchID)
	now := i.now()
	exp := now.Add(p.TTL)
	if old.ExpiresAt != nil && old.ExpiresAt.Time.Before(exp) {
		exp = old.ExpiresAt.Time
	}
	claims.RegisteredClaims = jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}
	token, err := i.sign(claims)
	if err != nil {
		return nil, err
	}
	return &Session{Token: token, Claims: claims, Role: d.role, Subscription: d.sub}, nil
}

func (i *Issuer) signRefresh(tenant *database.Tenant, userID uint, authMethod string) (string, error) {
	now := i.now()
	if strings.TrimSpace(authMethod) == "" {
		authMethod = KindPwd
	}
	return i.sign(&middleware.TenantRefreshClaims{
		UserID:        userID,
		TenantSlug:    tenant.Slug,
		TenantDB:      tenant.DBName,
		TenantID:      tenant.ID,
		TenantVersion: middleware.CurrentTenantJWTVersion(),
		AuthMethod:    authMethod,
		Type:          middleware.TenantRefreshType,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	})
}
