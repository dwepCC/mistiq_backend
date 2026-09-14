package handler

// AuthSAHandler expone la administración de usuarios del panel central (SuperAdminUser).
//
// Fase 5, etapa 3, Grupo 7, Paso E: la superficie de escritura quedó separada en 7 endpoints
// dedicados, cada uno con su propio permiso — ninguno genérico puede usarse como puerta trasera
// de otro (ver comentario en cada handler y sa_permissions_test.go de wiring):
//
//	POST   /users              usuarios_central.create   → CreateUserAPI (SIEMPRE Role="admin")
//	PUT    /users/:id          usuarios_central.update*  → UpdateUserAPI (SOLO name/email)
//	PUT    /users/:id/role     usuarios_central.change_role → ChangeUserRoleAPI (SOLO RoleID)
//	PUT    /users/:id/system-role  RequireSuperAdminOnly() → ChangeUserSystemRoleAPI (SOLO Role)
//	PATCH  /users/:id/status   usuarios_central.change_status → ChangeUserStatusAPI (SOLO Active)
//	POST   /users/:id/password usuarios_central.reset_password → ResetUserPasswordAPI
//	DELETE /users/:id          usuarios_central.destroy  → DestroyUserAPI (soft-delete)
//
// * UpdateUserAPI conserva el comportamiento previo a este grupo: un usuario editando su PROPIO
// nombre/email nunca necesitó ningún permiso (ver Paso B, aprobado) — el permiso granular solo se
// exige para editar a alguien más. Ver el propio handler.
//
// Toda la lógica de negocio (techo de delegación, cuenta protegida, último superadmin,
// transacciones) vive en service.SAUserService — este archivo solo arma requests, llama al
// servicio y traduce sus errores centinela a HTTP (mismo patrón que SARoleHandler, Paso C).
import (
	"errors"
	"strconv"
	"strings"
	"time"

	"tukifac/config"
	"tukifac/internal/superadmin/service"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// saPermissionsForUser resuelve los permisos efectivos a incluir en el JWT al momento del login:
//   - superadmin real → ["*"] (bypass total, ver middleware.SuperAdminClaims).
//   - usuario con RoleID asignado → permisos del rol (formato "module.action").
//   - usuario sin RoleID (todavía no migrado, o rol eliminado) → SIN permisos. Nunca se
//     interpreta "sin rol" como acceso total; fail-closed explícito, ver Fase 1.
func saPermissionsForUser(user *database.SuperAdminUser) []string {
	if strings.EqualFold(strings.TrimSpace(user.Role), "superadmin") {
		return []string{"*"}
	}
	if user.RoleID == nil {
		return []string{}
	}
	keys, err := service.NewSARoleService(database.CentralDB).GetRolePermissionKeys(*user.RoleID)
	if err != nil {
		// Rol inconsistente (p. ej. RoleID apunta a una fila borrada) → fail-closed, no 500: el
		// login no debe romperse por esto, simplemente el usuario queda sin permisos.
		return []string{}
	}
	return keys
}

type AuthSAHandler struct {
	userSvc *service.SAUserService
	roleSvc *service.SARoleService
}

func NewAuthSAHandler() *AuthSAHandler {
	return &AuthSAHandler{
		userSvc: service.NewSAUserService(database.CentralDB),
		roleSvc: service.NewSARoleService(database.CentralDB),
	}
}

// parseSAUserID / saUserErrorStatus / saUserErrorJSON siguen el mismo patrón que
// sa_role_handler.go (parseSARoleID / saRoleErrorStatus / saRoleErrorJSON).
func parseSAUserID(c fiber.Ctx) (uint, error) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return 0, errors.New("ID inválido")
	}
	return uint(id), nil
}

func saUserErrorStatus(err error) int {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return fiber.StatusNotFound
	case errors.Is(err, service.ErrSAUserEmailTaken),
		errors.Is(err, service.ErrSAUserLastSuperadmin):
		return fiber.StatusConflict
	case errors.Is(err, service.ErrSAUserNameRequired),
		errors.Is(err, service.ErrSAUserEmailRequired),
		errors.Is(err, service.ErrSAUserPasswordTooShort),
		errors.Is(err, service.ErrSAUserInvalidSystemRole),
		errors.Is(err, service.ErrSAUserActiveRequired):
		return fiber.StatusBadRequest
	case errors.Is(err, service.ErrSAUserCannotDelegate),
		errors.Is(err, service.ErrSAUserNotSuperadmin),
		errors.Is(err, service.ErrSAUserProtectedAccount):
		return fiber.StatusForbidden
	default:
		return fiber.StatusInternalServerError
	}
}

func saUserErrorJSON(c fiber.Ctx, err error) error {
	return c.Status(saUserErrorStatus(err)).JSON(fiber.Map{"error": err.Error()})
}

func saUserActorClaims(c fiber.Ctx) *middleware.SuperAdminClaims {
	claims, _ := c.Locals("sa_claims").(*middleware.SuperAdminClaims)
	return claims
}

// logSAUserAudit escribe una fila de auditoría para una operación sobre usuarios centrales.
// Nunca se le pasa contraseña/token/secret en el payload — ver cada call site.
func logSAUserAudit(c fiber.Ctx, action string, entityID uint, payload fiber.Map) {
	if database.CentralDB == nil {
		return
	}
	actorID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		UserID:    actorID,
		Action:    action,
		Entity:    "sa_user",
		EntityID:  entityID,
		Payload:   saas.MetaJSON(payload),
		IPAddress: c.IP(),
	})
}

// GET /api/superadmin/login
func (h *AuthSAHandler) LoginAPI(c fiber.Ctx) error {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Email == "" || body.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email y password son requeridos"})
	}

	var user database.SuperAdminUser
	if err := database.CentralDB.Where("email = ?", body.Email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Credenciales inválidas"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Error interno"})
	}

	if !user.CheckPassword(body.Password) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Credenciales inválidas"})
	}
	// Active se exige también en el login, no solo en el middleware: así el usuario recibe un
	// mensaje claro en vez de un token que el próximo request rechazaría igual (ver
	// middleware.verifySuperAdminSession).
	if !user.Active {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Cuenta desactivada"})
	}

	claims := &middleware.SuperAdminClaims{
		UserID:       user.ID,
		Email:        user.Email,
		Role:         user.Role,
		Type:         "superadmin",
		TokenVersion: user.TokenVersion,
		Permissions:  saPermissionsForUser(&user),
		SAJWTVersion: middleware.CurrentSuperAdminJWTVersion(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(config.AppConfig.SAJWTSecret))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Error generando token"})
	}

	return c.JSON(fiber.Map{
		"token":      tokenString,
		"expires_in": 28800,
		"user": fiber.Map{
			"id":    user.ID,
			"email": user.Email,
			"role":  user.Role,
		},
	})
}

// GET /api/superadmin/users
func (h *AuthSAHandler) ListUsersAPI(c fiber.Ctx) error {
	var users []database.SuperAdminUser
	if err := database.CentralDB.Order("id asc").Find(&users).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Error interno"})
	}

	out := make([]fiber.Map, 0, len(users))
	for _, u := range users {
		out = append(out, fiber.Map{
			"id":         u.ID,
			"name":       u.Name,
			"email":      u.Email,
			"role":       u.Role,
			"role_id":    u.RoleID,
			"active":     u.Active,
			"created_at": u.CreatedAt,
			"updated_at": u.UpdatedAt,
		})
	}
	return c.JSON(fiber.Map{"data": out})
}

// createUserRequest — DTO explícito (mass-assignment): SOLO estos 4 campos pueden llegar a
// SAUserService.Create. No incluye "role" a propósito: crear un usuario con Role="superadmin" NO
// es posible por esta vía bajo ninguna circunstancia (decisión explícita del diseño, Paso E) — la
// ÚNICA forma de que exista un superadmin nuevo es crear un admin aquí y promoverlo después con
// PUT /users/:id/system-role.
type createUserRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	RoleID   *uint  `json:"role_id"`
}

// POST /api/superadmin/users
func (h *AuthSAHandler) CreateUserAPI(c fiber.Ctx) error {
	var body createUserRequest
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	user, err := h.userSvc.Create(saUserActorClaims(c), body.Name, body.Email, body.Password, body.RoleID)
	if err != nil {
		return saUserErrorJSON(c, err)
	}

	roleName := ""
	if user.RoleID != nil {
		if role, rerr := h.roleSvc.GetByID(*user.RoleID); rerr == nil {
			roleName = role.Name
		}
	}
	logSAUserAudit(c, "user_created", user.ID, fiber.Map{
		"email": user.Email, "role_id": user.RoleID, "role_name": roleName,
	})

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"success": true,
		"data": fiber.Map{
			"id":         user.ID,
			"name":       user.Name,
			"email":      user.Email,
			"role":       user.Role,
			"role_id":    user.RoleID,
			"created_at": user.CreatedAt,
		},
	})
}

// updateUserBasicInfoRequest — DTO explícito (mass-assignment): SOLO name/email. No tiene campos
// Role/RoleID/Active/Password/TokenVersion/DeletedAt, así que aunque el body enviado los incluya,
// json.Unmarshal los ignora — nunca llegan a este handler, mucho menos al servicio. Ver
// TestUpdateUserAPI_MassAssignment_IgnoresProtectedFields.
type updateUserBasicInfoRequest struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
}

// PUT /api/superadmin/users/:id — SOLO name/email (§6/§7). Role/RoleID/Active/Password quedaron
// en endpoints dedicados (system-role/role/status/password) — este handler no los toca ni los
// lee del body. Comportamiento conservado del diseño previo a este grupo (Paso B, aprobado): un
// usuario editando su PROPIO nombre/email nunca necesitó ningún permiso; usuarios_central.update
// solo se exige para editar a alguien más.
func (h *AuthSAHandler) UpdateUserAPI(c fiber.Ctx) error {
	id, err := parseSAUserID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	actorID, _ := c.Locals("sa_user_id").(uint)
	if id != actorID {
		if !middleware.HasSAPermission(saUserActorClaims(c), "usuarios_central.update") {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "No tienes permiso para esta acción", "permission": "usuarios_central.update",
			})
		}
	}

	before, err := h.userSvc.GetByID(id)
	if err != nil {
		return saUserErrorJSON(c, err)
	}

	var body updateUserBasicInfoRequest
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	updated, err := h.userSvc.UpdateBasicInfo(id, body.Name, body.Email)
	if err != nil {
		return saUserErrorJSON(c, err)
	}

	if updated.Name != before.Name || updated.Email != before.Email {
		logSAUserAudit(c, "user_updated", id, fiber.Map{
			"name_before": before.Name, "name_after": updated.Name,
			"email_before": before.Email, "email_after": updated.Email,
		})
	}
	return c.JSON(fiber.Map{"success": true})
}

type changeUserRoleRequest struct {
	RoleID uint `json:"role_id"`
}

// PUT /api/superadmin/users/:id/role — SOLO RoleID (rol granular). Nunca toca Role
// (admin/superadmin) — ver ChangeUserSystemRoleAPI para eso. Techo de delegación obligatorio
// dentro de SAUserService.ChangeRole; no hay caso especial para "el actor se asigna a sí mismo"
// (el techo ya lo cubre igual que a un tercero — ver §9 del spec y su test dedicado).
func (h *AuthSAHandler) ChangeUserRoleAPI(c fiber.Ctx) error {
	id, err := parseSAUserID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	var body changeUserRoleRequest
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.RoleID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "role_id es requerido"})
	}

	before, err := h.userSvc.GetByID(id)
	if err != nil {
		return saUserErrorJSON(c, err)
	}
	oldRoleName := ""
	if before.RoleID != nil {
		if r, rerr := h.roleSvc.GetByID(*before.RoleID); rerr == nil {
			oldRoleName = r.Name
		}
	}

	updated, newRole, err := h.userSvc.ChangeRole(saUserActorClaims(c), id, body.RoleID)
	if err != nil {
		return saUserErrorJSON(c, err)
	}

	logSAUserAudit(c, "user_role_changed", id, fiber.Map{
		"role_id_before": before.RoleID, "role_id_after": updated.RoleID,
		"role_name_before": oldRoleName, "role_name_after": newRole.Name,
	})
	return c.JSON(fiber.Map{"success": true})
}

type changeUserSystemRoleRequest struct {
	Role string `json:"role"`
}

// PUT /api/superadmin/users/:id/system-role — ÚNICO endpoint capaz de tocar Role
// (admin↔superadmin). Protegido en la ruta con middleware.RequireSuperAdminOnly() (routes.go);
// SAUserService.ChangeSystemRole vuelve a exigir actorClaims.Role=="superadmin" como segunda
// barrera, y aplica la protección de último superadmin al demover.
func (h *AuthSAHandler) ChangeUserSystemRoleAPI(c fiber.Ctx) error {
	id, err := parseSAUserID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	var body changeUserSystemRoleRequest
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	before, err := h.userSvc.GetByID(id)
	if err != nil {
		return saUserErrorJSON(c, err)
	}
	oldRole := before.Role

	updated, err := h.userSvc.ChangeSystemRole(saUserActorClaims(c), id, body.Role)
	if err != nil {
		return saUserErrorJSON(c, err)
	}

	if updated.Role != oldRole {
		logSAUserAudit(c, "user_system_role_changed", id, fiber.Map{
			"role_before": oldRole, "role_after": updated.Role,
		})
	}
	return c.JSON(fiber.Map{"success": true})
}

type changeUserStatusRequest struct {
	Active *bool `json:"active"`
}

// PATCH /api/superadmin/users/:id/status — SOLO Active. Cuenta protegida + último superadmin
// (al desactivar) los aplica SAUserService.ChangeStatus.
func (h *AuthSAHandler) ChangeUserStatusAPI(c fiber.Ctx) error {
	id, err := parseSAUserID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	var body changeUserStatusRequest
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Active == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": service.ErrSAUserActiveRequired.Error()})
	}

	before, err := h.userSvc.GetByID(id)
	if err != nil {
		return saUserErrorJSON(c, err)
	}

	updated, err := h.userSvc.ChangeStatus(saUserActorClaims(c), id, *body.Active)
	if err != nil {
		return saUserErrorJSON(c, err)
	}

	if updated.Active != before.Active {
		logSAUserAudit(c, "user_status_changed", id, fiber.Map{
			"active_before": before.Active, "active_after": updated.Active,
		})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/superadmin/users/:id/password — reset por un actor distinto del dueño de la cuenta.
// Cuenta protegida (superadmin destino) la aplica SAUserService.ResetPassword. Nunca se registra
// ni se devuelve la contraseña.
func (h *AuthSAHandler) ResetUserPasswordAPI(c fiber.Ctx) error {
	id, err := parseSAUserID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	var body struct {
		NewPassword string `json:"new_password"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	if _, err := h.userSvc.ResetPassword(saUserActorClaims(c), id, body.NewPassword); err != nil {
		return saUserErrorJSON(c, err)
	}

	logSAUserAudit(c, "user_password_reset", id, fiber.Map{})
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/superadmin/users/:id — soft-delete (DeletedAt). Cuenta protegida + último
// superadmin los aplica SAUserService.Destroy.
func (h *AuthSAHandler) DestroyUserAPI(c fiber.Ctx) error {
	id, err := parseSAUserID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	before, err := h.userSvc.GetByID(id)
	if err != nil {
		return saUserErrorJSON(c, err)
	}

	if _, err := h.userSvc.Destroy(saUserActorClaims(c), id); err != nil {
		return saUserErrorJSON(c, err)
	}

	logSAUserAudit(c, "user_deleted", id, fiber.Map{"email": before.Email, "role": before.Role})
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/superadmin/me/password — sin cambios respecto a antes de este grupo (self-service,
// requiere la contraseña actual; no pasa por SAUserService porque no tiene ninguna de las
// invariantes de actor-vs-objetivo que motivan ese servicio).
func (h *AuthSAHandler) ChangeMyPasswordAPI(c fiber.Ctx) error {
	userIDAny := c.Locals("sa_user_id")
	userID, _ := userIDAny.(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "No autorizado"})
	}

	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	currentPassword := strings.TrimSpace(body.CurrentPassword)
	newPassword := strings.TrimSpace(body.NewPassword)
	if currentPassword == "" || newPassword == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "current_password y new_password son requeridos"})
	}
	if len(newPassword) < 8 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "La nueva contraseña debe tener mínimo 8 caracteres"})
	}

	var user database.SuperAdminUser
	if err := database.CentralDB.First(&user, userID).Error; err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "No autorizado"})
	}
	if !user.CheckPassword(currentPassword) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "La contraseña actual no es correcta"})
	}

	if err := user.SetPassword(newPassword); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Error interno"})
	}
	if err := database.CentralDB.Model(&user).Update("password", user.Password).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Error interno"})
	}
	// Cambio de contraseña: invalida también el token con el que se hizo esta misma request —
	// el usuario deberá volver a iniciar sesión. Comportamiento esperado, no un bug.
	if err := user.IncrementTokenVersion(database.CentralDB); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Error interno"})
	}
	return c.JSON(fiber.Map{"success": true})
}
