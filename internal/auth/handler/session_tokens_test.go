package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"tukifac/config"
	authservice "tukifac/internal/auth/service"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

const handlerTestSecret = "handler-test-secret-0123456789"

type fx struct {
	tdb    *gorm.DB
	tenant database.Tenant
	user   database.TenantUser
	app    *fiber.App
}

// Fixture real (SQLite): BD central + BD de tenant, usuario con contraseña, sucursales 1 y 2, restaurante habilitado.
// Se montan los handlers reales; el middleware de prueba solo deja en Locals lo que dejarían TenantAuthAPI y el
// resolvedor de tenant.
func newFx(t *testing.T) *fx {
	t.Helper()
	open := func(n string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%s?mode=memory&cache=shared", t.Name(), n)), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	central := open("c")
	if err := central.AutoMigrate(&database.Tenant{}, &database.TenantModule{}); err != nil {
		t.Fatal(err)
	}
	prev := database.CentralDB
	database.CentralDB = central
	t.Cleanup(func() { database.CentralDB = prev })
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{AppEnv: "development", JWTSecret: handlerTestSecret}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	f := &fx{tdb: open("t")}
	if err := database.ApplyBaselineSchema(f.tdb); err != nil {
		t.Fatal(err)
	}
	if err := f.tdb.AutoMigrate(&database.TenantUserBranch{}); err != nil {
		t.Fatal(err)
	}
	f.tenant = database.Tenant{Name: "Demo", Slug: "demo", DBName: "demo_db", Status: "active", RUC: "20000000002"}
	central.Create(&f.tenant)
	for _, m := range []string{"sales", "restaurant"} {
		central.Create(&database.TenantModule{TenantID: f.tenant.ID, ModuleKey: m, Enabled: true})
	}
	for i := 1; i <= 3; i++ {
		f.tdb.Create(&database.TenantBranch{Name: fmt.Sprintf("S%d", i), Active: true, IsMain: i == 1})
	}
	perm := map[string]uint{}
	for _, k := range [][2]string{{"sales", "view"}, {"branches", "switch"}, {"branches", "view_all"}} {
		p := database.TenantPermission{Module: k[0], Action: k[1]}
		f.tdb.Create(&p)
		perm[k[0]+"."+k[1]] = p.ID
	}
	role := database.TenantRole{Name: "Vendedor"}
	f.tdb.Create(&role)
	for _, k := range []string{"sales.view", "branches.switch"} {
		f.tdb.Create(&database.TenantRolePermission{RoleID: role.ID, PermissionID: perm[k]})
	}
	home := uint(1)
	f.user = database.TenantUser{Name: "U", Email: "u@x.com", RoleID: role.ID, HomeBranchID: &home, BranchID: &home, BranchSessionVersion: 2}
	if err := f.user.SetPassword("secreto123"); err != nil {
		t.Fatal(err)
	}
	f.tdb.Create(&f.user)
	for _, b := range []uint{1, 2} {
		f.tdb.Create(&database.TenantUserBranch{UserID: f.user.ID, BranchID: b})
	}
	f.tdb.Create(&database.TenantRestaurantStaff{UserID: f.user.ID, EmployeeType: "cashier", IsActive: true})

	f.app = fiber.New()
	f.app.Use(func(c fiber.Ctx) error {
		c.Locals("tenant", &f.tenant)
		c.Locals("tenantDB", f.tdb)
		if h := c.Get("Authorization"); len(h) > 7 { // emula TenantAuthAPI: valida el token y deja los claims
			claims := &middleware.TenantClaims{}
			tok, err := jwt.ParseWithClaims(h[7:], claims, func(*jwt.Token) (interface{}, error) { return []byte(handlerTestSecret), nil },
				jwt.WithValidMethods([]string{"HS256"}))
			if err != nil || !tok.Valid {
				return c.Status(401).JSON(fiber.Map{"error": "token inválido"})
			}
			c.Locals("tenant_claims", claims)
		}
		return c.Next()
	})
	a := NewAuthHandler()
	s := NewSessionHandler()
	f.app.Post("/api/login", a.LoginAPI)
	f.app.Post("/api/session/refresh", a.RefreshAPI)
	f.app.Post("/api/session/switch-branch", s.SwitchBranch)
	return f
}

func (f *fx) post(t *testing.T, path, bearer string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := f.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]interface{}{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func parseClaims(t *testing.T, s string) *middleware.TenantClaims {
	t.Helper()
	c := &middleware.TenantClaims{}
	tok, err := jwt.ParseWithClaims(s, c, func(*jwt.Token) (interface{}, error) { return []byte(handlerTestSecret), nil },
		jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !tok.Valid {
		t.Fatalf("token inválido: %v", err)
	}
	return c
}

func (f *fx) issue(t *testing.T, kind string) string {
	t.Helper()
	u, _, _ := database.LoadTenantUserForBranch(f.tdb, f.user.ID)
	opts := authservice.IssueOpts{Kind: kind}
	if kind == authservice.KindMasterAccess {
		opts.MasterActorID, opts.MasterActorEmail = 9, "sa@x.com"
	}
	s, err := authservice.DefaultIssuer().Issue(&f.tenant, f.tdb, u, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s.Token
}

// El contrato JSON de /api/login no cambia y el token emitido es pwd de 24 h con refresh.
func TestLoginAPI_ContractAndToken(t *testing.T) {
	f := newFx(t)
	code, body := f.post(t, "/api/login", "", map[string]string{"email": "u@x.com", "password": "secreto123"})
	if code != 200 {
		t.Fatalf("login: %d %v", code, body)
	}
	for _, k := range []string{"token", "refresh_token", "user", "active_branch", "can_switch_branch", "can_view_all",
		"allowed_branches", "modules", "permissions", "restaurant_permissions", "subscription", "subscription_blocked"} {
		if _, ok := body[k]; !ok {
			t.Errorf("falta la clave %q en la respuesta de login", k)
		}
	}
	c := parseClaims(t, body["token"].(string))
	if c.AuthMethod != "pwd" || c.ExpiresAt.Sub(c.IssuedAt.Time) != 24*time.Hour || c.ActiveBranchID != 1 || c.BranchSessionVersion != 2 {
		t.Fatalf("claims de login inesperados: %+v", c)
	}
	if len(c.Permissions) == 0 || c.EmployeeType != "cashier" {
		t.Fatalf("permisos/empleado: %v %q", c.Permissions, c.EmployeeType)
	}
	if rt, _ := body["refresh_token"].(string); rt == "" {
		t.Fatal("login sin refresh_token")
	}
	if code, _ := f.post(t, "/api/login", "", map[string]string{"email": "u@x.com", "password": "mala"}); code != 401 {
		t.Fatalf("contraseña mala: %d", code)
	}
}

// PIN: login (perfil pin, 12 h, sin permisos) -> switch-branch conserva exp y sigue en 12 h como máximo.
func TestSwitchBranch_PinKeepsExp(t *testing.T) {
	f := newFx(t)
	pin := f.issue(t, authservice.KindPin)
	c1 := parseClaims(t, pin)
	if c1.ExpiresAt.Sub(c1.IssuedAt.Time) != 12*time.Hour || len(c1.Permissions) != 0 {
		t.Fatalf("token PIN: ttl=%v perms=%v", c1.ExpiresAt.Sub(c1.IssuedAt.Time), c1.Permissions)
	}
	time.Sleep(1100 * time.Millisecond) // "ahora" debe diferir del login: antes el cambio fijaba 24 h desde ahora
	code, body := f.post(t, "/api/session/switch-branch", pin, map[string]uint{"branch_id": 2})
	if code != 200 {
		t.Fatalf("switch-branch PIN: %d %v", code, body)
	}
	c2 := parseClaims(t, body["token"].(string))
	if c2.ActiveBranchID != 2 || c2.AuthMethod != "pin" {
		t.Fatalf("claims: %+v", c2)
	}
	if !c2.ExpiresAt.Time.Equal(c1.ExpiresAt.Time) {
		t.Fatalf("exp cambió: %v -> %v", c1.ExpiresAt, c2.ExpiresAt)
	}
	if c2.ExpiresAt.Sub(c1.IssuedAt.Time) > 12*time.Hour {
		t.Fatal("la sesión PIN superó 12 h desde el login")
	}
}

// Acceso maestro: switch-branch conserva exp, Impersonated y MasterActor*.
func TestSwitchBranch_MasterAccessDoesNotExtend(t *testing.T) {
	f := newFx(t)
	tok := f.issue(t, authservice.KindMasterAccess)
	c1 := parseClaims(t, tok)
	time.Sleep(1100 * time.Millisecond)
	code, body := f.post(t, "/api/session/switch-branch", tok, map[string]uint{"branch_id": 2})
	if code != 200 {
		t.Fatalf("switch-branch: %d %v", code, body)
	}
	c2 := parseClaims(t, body["token"].(string))
	if !c2.ExpiresAt.Time.Equal(c1.ExpiresAt.Time) || !c2.Impersonated || c2.MasterActorID != 9 || c2.AuthMethod != "master_access" {
		t.Fatalf("c1=%+v c2=%+v", c1, c2)
	}
}

// pwd: encadenar varios switch-branch reales no mueve el exp del primer token (el refresh es quien renueva).
func TestSwitchBranch_PwdChainDoesNotExtend(t *testing.T) {
	f := newFx(t)
	tok := f.issue(t, authservice.KindPwd)
	first := parseClaims(t, tok)
	for i := 0; i < 4; i++ {
		time.Sleep(1100 * time.Millisecond)
		code, body := f.post(t, "/api/session/switch-branch", tok, map[string]uint{"branch_id": uint(1 + (i+1)%2)})
		if code != 200 {
			t.Fatalf("cambio %d: %d %v", i, code, body)
		}
		tok = body["token"].(string)
		if c := parseClaims(t, tok); !c.ExpiresAt.Time.Equal(first.ExpiresAt.Time) {
			t.Fatalf("cambio %d: exp %v != %v", i, c.ExpiresAt, first.ExpiresAt)
		}
	}
}

// switch-branch relee permisos: tras quitar un permiso al rol el token nuevo ya no lo trae.
func TestSwitchBranch_RereadsPermissions(t *testing.T) {
	f := newFx(t)
	tok := f.issue(t, authservice.KindPwd)
	if !has(parseClaims(t, tok).Permissions, "sales.view") {
		t.Fatal("precondición")
	}
	var p database.TenantPermission
	f.tdb.Where("module = ? AND action = ?", "sales", "view").First(&p)
	f.tdb.Where("role_id = ? AND permission_id = ?", f.user.RoleID, p.ID).Delete(&database.TenantRolePermission{})
	code, body := f.post(t, "/api/session/switch-branch", tok, map[string]uint{"branch_id": 2})
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	if has(parseClaims(t, body["token"].(string)).Permissions, "sales.view") {
		t.Fatal("el token nuevo conserva un permiso revocado")
	}
}

// Usuario desactivado: ni switch-branch ni refresh emiten nada.
func TestInactiveUser_RejectedBySwitchBranchAndRefresh(t *testing.T) {
	f := newFx(t)
	code, login := f.post(t, "/api/login", "", map[string]string{"email": "u@x.com", "password": "secreto123"})
	if code != 200 {
		t.Fatal(code)
	}
	tok, refresh := login["token"].(string), login["refresh_token"].(string)

	f.tdb.Model(&database.TenantUser{}).Where("id = ?", f.user.ID).Update("active", false)

	if code, body := f.post(t, "/api/session/switch-branch", tok, map[string]uint{"branch_id": 2}); code != 401 || body["token"] != nil {
		t.Fatalf("switch-branch con usuario inactivo: %d %v", code, body)
	}
	if code, body := f.post(t, "/api/session/refresh", "", map[string]interface{}{"refresh_token": refresh}); code != 401 || body["token"] != nil {
		t.Fatalf("refresh con usuario inactivo: %d %v", code, body)
	}
}

// Refresh: reconstruye desde la BD (permiso quitado no vuelve), emite 24 h nuevas y respeta la sucursal pedida si es válida.
func TestRefreshAPI_RebuildsFromDBAndKeepsAllowedBranch(t *testing.T) {
	f := newFx(t)
	_, login := f.post(t, "/api/login", "", map[string]string{"email": "u@x.com", "password": "secreto123"})
	refresh := login["refresh_token"].(string)

	var p database.TenantPermission
	f.tdb.Where("module = ? AND action = ?", "sales", "view").First(&p)
	f.tdb.Where("role_id = ? AND permission_id = ?", f.user.RoleID, p.ID).Delete(&database.TenantRolePermission{})

	code, body := f.post(t, "/api/session/refresh", "", map[string]interface{}{"refresh_token": refresh, "active_branch_id": 2})
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	c := parseClaims(t, body["token"].(string))
	if c.ActiveBranchID != 2 || has(c.Permissions, "sales.view") || c.ExpiresAt.Sub(c.IssuedAt.Time) != 24*time.Hour {
		t.Fatalf("claims tras refresh: branch=%d perms=%v ttl=%v", c.ActiveBranchID, c.Permissions, c.ExpiresAt.Sub(c.IssuedAt.Time))
	}
	// una sucursal no asignada (3) se ignora: se queda en la base
	_, body = f.post(t, "/api/session/refresh", "", map[string]interface{}{"refresh_token": refresh, "active_branch_id": 3})
	if c := parseClaims(t, body["token"].(string)); c.ActiveBranchID != 1 {
		t.Fatalf("aceptó una sucursal no asignada: %d", c.ActiveBranchID)
	}
}

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
