package service

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

const issuerTestSecret = "issuer-test-secret-0123456789"

type issuerFx struct {
	tdb    *gorm.DB
	tenant database.Tenant
	user   database.TenantUser
	role   database.TenantRole
	perms  map[string]uint // "module.action" -> permission id
}

// newIssuerFx arma una BD central y una BD de tenant reales en SQLite (sin mocks): rol con permisos, usuario asignado a
// las sucursales 1 y 2, módulo restaurante habilitado y personal de restaurante activo (para el perfil PIN).
func newIssuerFx(t *testing.T) *issuerFx {
	t.Helper()
	open := func(name string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%s?mode=memory&cache=shared", t.Name(), name)), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	central := open("central")
	if err := central.AutoMigrate(&database.Tenant{}, &database.TenantModule{}); err != nil {
		t.Fatal(err)
	}
	prevCentral := database.CentralDB
	database.CentralDB = central
	t.Cleanup(func() { database.CentralDB = prevCentral })
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{AppEnv: "development", JWTSecret: issuerTestSecret}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	fx := &issuerFx{tdb: open("tenant"), perms: map[string]uint{}}
	if err := database.ApplyBaselineSchema(fx.tdb); err != nil {
		t.Fatal(err)
	}
	if err := fx.tdb.AutoMigrate(&database.TenantUserBranch{}); err != nil {
		t.Fatal(err)
	}

	fx.tenant = database.Tenant{Name: "Demo", Slug: "demo", DBName: "demo_db", Status: "active", RUC: "20000000001"}
	if err := central.Create(&fx.tenant).Error; err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"sales", "restaurant"} {
		if err := central.Create(&database.TenantModule{TenantID: fx.tenant.ID, ModuleKey: m, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}

	for i := 1; i <= 3; i++ {
		if err := fx.tdb.Create(&database.TenantBranch{Name: fmt.Sprintf("S%d", i), Active: true, IsMain: i == 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range [][2]string{{"sales", "view"}, {"sales", "create"}, {"branches", "switch"}, {"branches", "view_all"}} {
		p := database.TenantPermission{Module: k[0], Action: k[1]}
		if err := fx.tdb.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		fx.perms[k[0]+"."+k[1]] = p.ID
	}
	fx.role = database.TenantRole{Name: "Vendedor"}
	if err := fx.tdb.Create(&fx.role).Error; err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sales.view", "sales.create", "branches.switch"} {
		if err := fx.tdb.Create(&database.TenantRolePermission{RoleID: fx.role.ID, PermissionID: fx.perms[k]}).Error; err != nil {
			t.Fatal(err)
		}
	}
	home := uint(1)
	fx.user = database.TenantUser{Name: "U", Email: "u@x.com", RoleID: fx.role.ID, HomeBranchID: &home, BranchID: &home, BranchSessionVersion: 3}
	if err := fx.tdb.Create(&fx.user).Error; err != nil {
		t.Fatal(err)
	}
	for _, b := range []uint{1, 2} {
		if err := fx.tdb.Create(&database.TenantUserBranch{UserID: fx.user.ID, BranchID: b}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := fx.tdb.Create(&database.TenantRestaurantStaff{UserID: fx.user.ID, EmployeeType: "cashier", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	return fx
}

// reloadUser vuelve a leer el usuario como lo hacen los handlers.
func (fx *issuerFx) reloadUser(t *testing.T) *database.TenantUser {
	t.Helper()
	u, _, err := database.LoadTenantUserForBranch(fx.tdb, fx.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func parseTenantToken(t *testing.T, s string) *middleware.TenantClaims {
	t.Helper()
	c := &middleware.TenantClaims{}
	tok, err := jwt.ParseWithClaims(s, c, func(*jwt.Token) (interface{}, error) { return []byte(issuerTestSecret), nil },
		jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !tok.Valid {
		t.Fatalf("token inválido: %v", err)
	}
	return c
}

func payloadKeys(t *testing.T, s string) map[string]bool {
	t.Helper()
	m := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(s, m); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func issuerAt(t time.Time) *Issuer { return &Issuer{Now: func() time.Time { return t }} }

func (fx *issuerFx) issue(t *testing.T, kind string, at time.Time) *Session {
	t.Helper()
	opts := IssueOpts{Kind: kind}
	if kind == KindMasterAccess {
		opts.MasterActorID, opts.MasterActorEmail = 77, "soporte@tukifac.test"
	}
	s, err := issuerAt(at).Issue(&fx.tenant, fx.tdb, fx.reloadUser(t), opts)
	if err != nil {
		t.Fatalf("Issue(%s): %v", kind, err)
	}
	return s
}

// Por perfil: vigencia correcta en Issue, y Reissue produce el MISMO conjunto de claims (por reflexión y por las llaves
// del payload), conserva exp/Kind/Impersonated/MasterActor* y solo cambia sucursal.
func TestIssuerProfiles_IssueVersusReissue(t *testing.T) {
	cases := []struct {
		kind      string
		ttl       time.Duration
		wantPerms bool
		imperson  bool
	}{
		{KindPwd, 24 * time.Hour, true, false},
		{KindPin, 12 * time.Hour, false, false},
		{KindMasterAccess, 24 * time.Hour, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			fx := newIssuerFx(t)
			t0 := time.Now().Truncate(time.Second)
			first := fx.issue(t, tc.kind, t0)
			c1 := parseTenantToken(t, first.Token)

			if got := c1.ExpiresAt.Sub(c1.IssuedAt.Time); got != tc.ttl {
				t.Fatalf("Issue: vigencia %v, esperada %v", got, tc.ttl)
			}
			if c1.ActiveBranchID != 1 || c1.BranchSessionVersion != 3 {
				t.Fatalf("Issue: sucursal/versión inesperadas: %d/%d", c1.ActiveBranchID, c1.BranchSessionVersion)
			}
			if tc.wantPerms != (len(c1.Permissions) > 0) {
				t.Fatalf("Issue: permisos en JWT = %v, esperado con permisos = %v", c1.Permissions, tc.wantPerms)
			}
			if c1.Impersonated != tc.imperson || c1.AuthMethod != tc.kind {
				t.Fatalf("Issue: Impersonated=%v AuthMethod=%q", c1.Impersonated, c1.AuthMethod)
			}

			re, err := issuerAt(t0.Add(time.Hour)).Reissue(fx.tdb, c1, fx.reloadUser(t), Change{ActiveBranchID: 2})
			if err != nil {
				t.Fatal(err)
			}
			c2 := parseTenantToken(t, re.Token)

			// Mismo conjunto de claims: llaves del payload...
			k1, k2 := payloadKeys(t, first.Token), payloadKeys(t, re.Token)
			if !reflect.DeepEqual(k1, k2) {
				t.Fatalf("llaves de payload distintas\nIssue:   %v\nReissue: %v", k1, k2)
			}
			// ... y valores campo por campo (solo debe cambiar la sucursal y la fecha de emisión).
			exempt := map[string]bool{"ActiveBranchID": true, "RegisteredClaims": true}
			v1, v2 := reflect.ValueOf(*c1), reflect.ValueOf(*c2)
			for i := 0; i < v1.NumField(); i++ {
				name := v1.Type().Field(i).Name
				if exempt[name] {
					continue
				}
				if !reflect.DeepEqual(v1.Field(i).Interface(), v2.Field(i).Interface()) {
					t.Errorf("campo %s difiere: Issue=%v Reissue=%v", name, v1.Field(i).Interface(), v2.Field(i).Interface())
				}
			}
			if c2.ActiveBranchID != 2 {
				t.Fatalf("Reissue no cambió la sucursal: %d", c2.ActiveBranchID)
			}
			if !c2.ExpiresAt.Time.Equal(c1.ExpiresAt.Time) {
				t.Fatalf("Reissue extendió/alteró exp: %v -> %v", c1.ExpiresAt, c2.ExpiresAt)
			}
			if tc.imperson && (c2.MasterActorID != 77 || c2.MasterActorEmail != "soporte@tukifac.test") {
				t.Fatalf("Reissue perdió MasterActor*: %+v", c2)
			}
		})
	}
}

// Encadenar N cambios de sucursal (cada uno horas después) no prolonga NINGÚN perfil más allá del exp original.
func TestReissue_ChainingNeverExtendsSession(t *testing.T) {
	for _, kind := range []string{KindPwd, KindPin, KindMasterAccess} {
		t.Run(kind, func(t *testing.T) {
			fx := newIssuerFx(t)
			t0 := time.Now().Truncate(time.Second)
			first := fx.issue(t, kind, t0)
			orig := parseTenantToken(t, first.Token)
			p, _ := ProfileFor(kind)
			wantExp := t0.Add(p.TTL)
			if !orig.ExpiresAt.Time.Equal(wantExp) {
				t.Fatalf("exp inicial %v != %v", orig.ExpiresAt.Time, wantExp)
			}

			cur := orig
			for n := 1; n <= 6; n++ {
				// cada cambio ocurre 1 h más tarde que el anterior (el token sigue vigente al parsearlo)
				at := t0.Add(time.Duration(n) * time.Hour)
				branchID := uint(1 + n%2) // alterna 2,1,2,...
				re, err := issuerAt(at).Reissue(fx.tdb, cur, fx.reloadUser(t), Change{ActiveBranchID: branchID})
				if err != nil {
					t.Fatalf("cambio %d: %v", n, err)
				}
				cur = parseTenantToken(t, re.Token)
				if !cur.ExpiresAt.Time.Equal(wantExp) {
					t.Fatalf("cambio %d: exp %v, debía seguir en %v", n, cur.ExpiresAt.Time, wantExp)
				}
			}
			// El máximo absoluto es el TTL del perfil desde el primer token (PIN: ≤ 12 h; pwd/master: ≤ 24 h).
			if cur.ExpiresAt.Time.Sub(t0) > p.TTL {
				t.Fatalf("la sesión se prolongó a %v desde el inicio (máx %v)", cur.ExpiresAt.Time.Sub(t0), p.TTL)
			}
		})
	}
}

// Un token cuyo exp es mayor que el TTL del perfil (p. ej. emitido con otra política) se acota a ahora+TTL.
func TestReissue_CapsExpToProfileTTL(t *testing.T) {
	fx := newIssuerFx(t)
	now := time.Now().Truncate(time.Second)
	old := parseTenantToken(t, fx.issue(t, KindPin, now).Token)
	old.ExpiresAt = jwt.NewNumericDate(now.Add(48 * time.Hour))
	re, err := issuerAt(now).Reissue(fx.tdb, old, fx.reloadUser(t), Change{ActiveBranchID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := parseTenantToken(t, re.Token).ExpiresAt.Time; !got.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("exp = %v, esperado ahora+12h", got)
	}
}

// Reissue relee permisos, módulos y estado de la BD: lo quitado/cambiado tras el login NO viaja al token nuevo.
func TestReissue_RereadsRolePermissionsModulesAndStatus(t *testing.T) {
	fx := newIssuerFx(t)
	t0 := time.Now().Truncate(time.Second)
	first := parseTenantToken(t, fx.issue(t, KindPwd, t0).Token)
	if !contains(first.Permissions, "sales.create") || !contains(first.Modules, "restaurant") || first.Status != "active" {
		t.Fatalf("precondición: %+v", first)
	}

	// 1) se le quita sales.create al rol
	if err := fx.tdb.Where("role_id = ? AND permission_id = ?", fx.role.ID, fx.perms["sales.create"]).
		Delete(&database.TenantRolePermission{}).Error; err != nil {
		t.Fatal(err)
	}
	// 2) se deshabilita el módulo restaurant y 3) el tenant queda suspendido
	if err := database.CentralDB.Model(&database.TenantModule{}).Where("module_key = ?", "restaurant").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.CentralDB.Model(&database.Tenant{}).Where("id = ?", fx.tenant.ID).Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}

	re, err := issuerAt(t0.Add(time.Minute)).Reissue(fx.tdb, first, fx.reloadUser(t), Change{ActiveBranchID: 2})
	if err != nil {
		t.Fatal(err)
	}
	c := parseTenantToken(t, re.Token)
	if contains(c.Permissions, "sales.create") {
		t.Fatalf("el token nuevo conserva un permiso revocado: %v", c.Permissions)
	}
	if !contains(c.Permissions, "sales.view") {
		t.Fatalf("el token nuevo perdió permisos que sí tiene: %v", c.Permissions)
	}
	if contains(c.Modules, "restaurant") {
		t.Fatalf("conserva un módulo deshabilitado: %v", c.Modules)
	}
	if c.Status != "suspended" {
		t.Fatalf("status = %q, esperado suspended", c.Status)
	}

	// 4) cambio de rol: el nombre/ID del token nuevo salen de la BD, no del token viejo
	other := database.TenantRole{Name: "Cajero"}
	if err := fx.tdb.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.tdb.Model(&database.TenantUser{}).Where("id = ?", fx.user.ID).Update("role_id", other.ID).Error; err != nil {
		t.Fatal(err)
	}
	re2, err := issuerAt(t0.Add(2*time.Minute)).Reissue(fx.tdb, first, fx.reloadUser(t), Change{ActiveBranchID: 1})
	if err != nil {
		t.Fatal(err)
	}
	c2 := parseTenantToken(t, re2.Token)
	if c2.RoleID != other.ID || c2.RoleName != "Cajero" || len(c2.Permissions) != 0 {
		t.Fatalf("rol/permisos no releídos: %d %q %v", c2.RoleID, c2.RoleName, c2.Permissions)
	}
}

// Usuario inactivo: Issue y Reissue no emiten nada.
func TestIssuer_RejectsInactiveUser(t *testing.T) {
	fx := newIssuerFx(t)
	t0 := time.Now()
	first := parseTenantToken(t, fx.issue(t, KindPwd, t0).Token)
	if err := fx.tdb.Model(&database.TenantUser{}).Where("id = ?", fx.user.ID).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}
	u := fx.reloadUser(t)
	if _, err := issuerAt(t0).Reissue(fx.tdb, first, u, Change{ActiveBranchID: 2}); err != ErrUserInactive {
		t.Fatalf("Reissue: %v, esperado ErrUserInactive", err)
	}
	if _, err := issuerAt(t0).Issue(&fx.tenant, fx.tdb, u, IssueOpts{Kind: KindPwd}); err != ErrUserInactive {
		t.Fatalf("Issue: %v, esperado ErrUserInactive", err)
	}
}

// PIN: si el personal de restaurante queda inactivo, el cambio de sucursal no renueva el token.
func TestReissue_PinRejectedWhenStaffInactive(t *testing.T) {
	fx := newIssuerFx(t)
	t0 := time.Now()
	first := parseTenantToken(t, fx.issue(t, KindPin, t0).Token)
	if err := fx.tdb.Model(&database.TenantRestaurantStaff{}).Where("user_id = ?", fx.user.ID).Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := issuerAt(t0).Reissue(fx.tdb, first, fx.reloadUser(t), Change{ActiveBranchID: 2}); err != ErrSessionNoLongerValid {
		t.Fatalf("esperado ErrSessionNoLongerValid, obtuvo %v", err)
	}
}

// El refresh solo se emite en el perfil pwd, vive 30 d y lo acepta ParseTenantRefreshToken (que valida TenantVersion).
func TestIssuer_RefreshTokenOnlyForPwd(t *testing.T) {
	fx := newIssuerFx(t)
	now := time.Now().Truncate(time.Second)
	for _, kind := range []string{KindPwd, KindPin, KindMasterAccess} {
		s, err := issuerAt(now).Issue(&fx.tenant, fx.tdb, fx.reloadUser(t), IssueOpts{Kind: kind, WithRefresh: true})
		if err != nil {
			t.Fatal(err)
		}
		if (kind == KindPwd) != (s.RefreshToken != "") {
			t.Fatalf("%s: refresh emitido = %v", kind, s.RefreshToken != "")
		}
		if kind == KindPwd {
			rc, err := middleware.ParseTenantRefreshToken(s.RefreshToken)
			if err != nil {
				t.Fatal(err)
			}
			if !rc.ExpiresAt.Time.Equal(now.Add(30 * 24 * time.Hour)) {
				t.Fatalf("exp refresh %v", rc.ExpiresAt)
			}
			if rc.TenantVersion != middleware.CurrentTenantJWTVersion() || rc.UserID != fx.user.ID {
				t.Fatalf("claims de refresh inesperados: %+v", rc)
			}
		}
	}
	// un refresh con TenantVersion inferior a la mínima se rechaza en el parseo
	bad := issuerAt(now)
	tok, _ := bad.sign(&middleware.TenantRefreshClaims{
		UserID: 1, TenantSlug: "demo", TenantDB: "d", TenantID: 1, TenantVersion: 0, Type: middleware.TenantRefreshType,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))},
	})
	if middleware.MinTenantJWTVersion > 0 {
		if _, err := middleware.ParseTenantRefreshToken(tok); err == nil {
			t.Fatal("refresh con TenantVersion antigua debía rechazarse")
		}
	}
}

// El parseo solo admite HS256: un token HS512 firmado con el mismo secreto se rechaza.
func TestParseTenantRefreshToken_RejectsOtherHMAC(t *testing.T) {
	_ = newIssuerFx(t)
	claims := &middleware.TenantRefreshClaims{
		UserID: 1, TenantSlug: "demo", TenantDB: "d", TenantID: 1, TenantVersion: middleware.CurrentTenantJWTVersion(),
		Type:             middleware.TenantRefreshType,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString([]byte(issuerTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := middleware.ParseTenantRefreshToken(s); err == nil {
		t.Fatal("HS512 debía rechazarse")
	}
}

func TestProfileFor(t *testing.T) {
	if p, ok := ProfileFor(""); !ok || p.Kind != KindPwd {
		t.Fatal("\"\" debe resolver a pwd")
	}
	if _, ok := ProfileFor("otro"); ok {
		t.Fatal("perfil desconocido debe rechazarse")
	}
	if profiles[KindPin].WithPerms || profiles[KindPin].IssueRefresh || profiles[KindPin].TTL != 12*time.Hour {
		t.Fatal("perfil pin mal definido")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
