package handler

// SARoleHandler expone el RBAC del panel central (SARole/SAPermission) vía HTTP.
//
// Fase 5, etapa 3, Grupo 7: las rutas de escritura (crear/editar/eliminar rol, asignar permisos)
// llevan RequireSAPermission("roles.create"/"roles.update"/"roles.delete"/"roles.manage") a nivel
// de ruta (routes.go) — eso solo comprueba que el actor puede EJERCER la acción. Este archivo
// añade una segunda capa, el "techo de delegación" (middleware.CanDelegateAll): un actor con
// roles.manage puede EJERCER la administración de cualquier rol, pero solo puede DELEGAR
// (agregar a un rol, o —ver AuthSAHandler— asignarle a un usuario) permisos que él mismo posee.
// Ver internal/superadmin/service/sa_role_service.go para el detalle de por qué esa distinción
// vive aquí y no en el servicio: el servicio no conoce al actor (claims), solo el handler.
//
// Ningún handler de este archivo toca superadmin_users.role ni .role_id: la asignación de un rol
// a un usuario es un flujo aparte (AuthSAHandler / SAUserService, ver Grupo 7 Paso E).
import (
	"errors"
	"strconv"

	"tukifac/internal/superadmin/service"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type SARoleHandler struct {
	svc *service.SARoleService
}

func NewSARoleHandler() *SARoleHandler {
	return &SARoleHandler{svc: service.NewSARoleService(database.CentralDB)}
}

// saRoleErrorStatus mapea los errores centinela de SARoleService al código HTTP correcto:
//   - gorm.ErrRecordNotFound                                → 404 (rol/recurso inexistente)
//   - conflicto con el estado actual del recurso (duplicado,
//     rol de sistema, rol con usuarios asignados)            → 409
//   - validación de input (nombre vacío/largo, permiso
//     inexistente, nombre reservado)                         → 400
//   - cualquier otro error (fallo de BD, etc.)                → 500
func saRoleErrorStatus(err error) int {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return fiber.StatusNotFound
	case errors.Is(err, service.ErrSARoleNameTaken),
		errors.Is(err, service.ErrSASystemRoleNotRenamable),
		errors.Is(err, service.ErrSASystemRoleNotDeletable),
		errors.Is(err, service.ErrSARoleHasAssignedUsers):
		return fiber.StatusConflict
	case errors.Is(err, service.ErrSARoleNameRequired),
		errors.Is(err, service.ErrSARoleNameTooLong),
		errors.Is(err, service.ErrSARoleDescriptionTooLong),
		errors.Is(err, service.ErrSAReservedRoleName),
		errors.Is(err, service.ErrSAPermissionNotFound):
		return fiber.StatusBadRequest
	default:
		return fiber.StatusInternalServerError
	}
}

func saRoleErrorJSON(c fiber.Ctx, err error) error {
	return c.Status(saRoleErrorStatus(err)).JSON(fiber.Map{"error": err.Error()})
}

func parseSARoleID(c fiber.Ctx) (uint, error) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return 0, errors.New("ID inválido")
	}
	return uint(id), nil
}

// saRoleActorClaims lee los claims del actor ya validados por SuperAdminAuthAPI — usado para el
// techo de delegación (middleware.CanDelegateAll necesita el claims completo, no solo la lista de
// permisos, porque también resuelve el bypass de superadmin).
func saRoleActorClaims(c fiber.Ctx) *middleware.SuperAdminClaims {
	claims, _ := c.Locals("sa_claims").(*middleware.SuperAdminClaims)
	return claims
}

// logSARoleAudit escribe una fila de auditoría para una operación sobre roles (creación, edición,
// borrado, cambio de permisos). Sigue la convención ya usada en el resto del proyecto: se escribe
// en la capa handler, nunca en el servicio, y nunca se registra nada más allá de lo que la propia
// operación ya expone (aquí no hay contraseñas/tokens/secrets que filtrar).
func logSARoleAudit(c fiber.Ctx, action string, entityID uint, payload fiber.Map) {
	if database.CentralDB == nil {
		return
	}
	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		UserID:    saUserID,
		Action:    action,
		Entity:    "sa_role",
		EntityID:  entityID,
		Payload:   saas.MetaJSON(payload),
		IPAddress: c.IP(),
	})
}

// GET /api/superadmin/roles
func (h *SARoleHandler) ListAPI(c fiber.Ctx) error {
	roles, err := h.svc.List()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": roles})
}

// GET /api/superadmin/roles/:id
func (h *SARoleHandler) GetAPI(c fiber.Ctx) error {
	id, err := parseSARoleID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	role, err := h.svc.GetByID(id)
	if err != nil {
		return saRoleErrorJSON(c, err)
	}
	return c.JSON(fiber.Map{"data": role})
}

// POST /api/superadmin/roles
func (h *SARoleHandler) CreateAPI(c fiber.Ctx) error {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	role, err := h.svc.Create(body.Name, body.Description)
	if err != nil {
		return saRoleErrorJSON(c, err)
	}
	logSARoleAudit(c, "role_created", role.ID, fiber.Map{
		"role_name":   role.Name,
		"description": role.Description,
	})
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": role})
}

// PUT /api/superadmin/roles/:id — SOLO nombre/descripción. Nunca toca permisos: modificar otros
// atributos del rol no es una vía para esquivar el techo de delegación de SetRolePermissionsAPI
// (ver comentario ahí) — este handler ni siquiera lee ni escribe sa_role_permissions.
func (h *SARoleHandler) UpdateAPI(c fiber.Ctx) error {
	id, err := parseSARoleID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	before, err := h.svc.GetByID(id)
	if err != nil {
		return saRoleErrorJSON(c, err)
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := h.svc.Update(id, body.Name, body.Description); err != nil {
		return saRoleErrorJSON(c, err)
	}
	// Se relee de la BD (en vez de auditar el body crudo) para registrar el valor REALMENTE
	// guardado — Update() aplica strings.TrimSpace antes de persistir.
	after, err := h.svc.GetByID(id)
	if err == nil {
		logSARoleAudit(c, "role_updated", id, fiber.Map{
			"name_before": before.Name, "name_after": after.Name,
			"description_before": before.Description, "description_after": after.Description,
		})
	}
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/superadmin/roles/:id
//
// Techo de delegación (Grupo 7 §17): un actor no puede eliminar un rol cuyo contenido de
// permisos exceda lo que él mismo podría delegar — sin esto, roles.manage se podría usar para
// deshacerse "indirectamente" de un rol cuyo alcance de permisos el actor no controla (y, ya
// eliminado, nada impide recrearlo desde cero con el mismo nombre y otro conjunto de permisos).
// El servicio ya rechaza roles de sistema y roles con usuarios asignados (ErrSASystemRoleNotDeletable/
// ErrSARoleHasAssignedUsers) — esta comprobación corre ANTES, así que ninguna de esas dos reglas
// existentes se relaja ni se esquiva.
func (h *SARoleHandler) DeleteAPI(c fiber.Ctx) error {
	id, err := parseSARoleID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	role, err := h.svc.GetByID(id)
	if err != nil {
		return saRoleErrorJSON(c, err)
	}
	currentKeys, err := h.svc.GetRolePermissionKeys(id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if !middleware.CanDelegateAll(saRoleActorClaims(c), currentKeys) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "No puedes eliminar un rol que contiene permisos que tú mismo no puedes delegar",
		})
	}
	if err := h.svc.Delete(id); err != nil {
		return saRoleErrorJSON(c, err)
	}
	logSARoleAudit(c, "role_deleted", id, fiber.Map{
		"role_name":   role.Name,
		"permissions": currentKeys,
	})
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/superadmin/permissions — catálogo completo (solo lectura; el catálogo lo gobierna
// el seed idempotente, no esta API).
func (h *SARoleHandler) PermissionsCatalogAPI(c fiber.Ctx) error {
	perms, err := h.svc.AllPermissions()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": perms})
}

// GET /api/superadmin/roles/:id/permissions
func (h *SARoleHandler) RolePermissionsAPI(c fiber.Ctx) error {
	id, err := parseSARoleID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if _, err := h.svc.GetByID(id); err != nil {
		return saRoleErrorJSON(c, err)
	}
	ids, err := h.svc.RolePermissions(id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": ids})
}

// PUT /api/superadmin/roles/:id/permissions
//
// Techo de delegación (Grupo 7 §3-§6): SetRolePermissions reemplaza TODO el conjunto de permisos
// del rol, pero la comprobación de delegación se hace SOLO contra los permisos AGREGADOS (los que
// están en el body pero no estaban antes) — no contra el conjunto completo. Esto es deliberado:
//   - Quitar un permiso que el actor no posee está permitido: nunca es una escalada, y bloquearlo
//     dejaría a cualquier rol legado (con permisos que nadie actual delega) imposible de tocar.
//   - Mantener un permiso que ya estaba tampoco es una escalada — el actor no lo está otorgando
//     ahora, ya estaba otorgado por quien lo puso originalmente.
//   - Agregar un permiso que el actor no puede delegar SÍ está prohibido — es exactamente el
//     "caso crítico" del diseño aprobado: un admin con roles.manage pero sin
//     usuarios_central.change_role no puede agregárselo a ningún rol para asignárselo después.
func (h *SARoleHandler) SetRolePermissionsAPI(c fiber.Ctx) error {
	id, err := parseSARoleID(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	role, err := h.svc.GetByID(id)
	if err != nil {
		return saRoleErrorJSON(c, err)
	}
	var body struct {
		PermissionIDs []uint `json:"permission_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	beforeIDs, err := h.svc.RolePermissions(id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	beforeSet := make(map[uint]bool, len(beforeIDs))
	for _, pid := range beforeIDs {
		beforeSet[pid] = true
	}
	afterSet := make(map[uint]bool, len(body.PermissionIDs))
	for _, pid := range body.PermissionIDs {
		if pid != 0 {
			afterSet[pid] = true
		}
	}
	var addedIDs, removedIDs []uint
	for pid := range afterSet {
		if !beforeSet[pid] {
			addedIDs = append(addedIDs, pid)
		}
	}
	for pid := range beforeSet {
		if !afterSet[pid] {
			removedIDs = append(removedIDs, pid)
		}
	}

	// Los IDs agregados aún no están validados contra la BD (SetRolePermissions lo hace después) —
	// PermissionKeysByIDs resuelve en silencio solo los que existan; si alguno no existiera, faltará
	// en addedKeys y CanDelegateAll no lo verá — inofensivo, porque SetRolePermissions rechazará el
	// ID inexistente de todas formas más abajo (ErrSAPermissionNotFound), antes de tocar la BD.
	addedKeysByID, err := h.svc.PermissionKeysByIDs(addedIDs)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	addedKeys := make([]string, 0, len(addedKeysByID))
	for _, key := range addedKeysByID {
		addedKeys = append(addedKeys, key)
	}
	if !middleware.CanDelegateAll(saRoleActorClaims(c), addedKeys) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "No puedes agregar a un rol permisos que tú mismo no puedes delegar",
		})
	}

	if err := h.svc.SetRolePermissions(id, body.PermissionIDs); err != nil {
		return saRoleErrorJSON(c, err)
	}

	removedKeysByID, err := h.svc.PermissionKeysByIDs(removedIDs)
	if err == nil {
		removedKeys := make([]string, 0, len(removedKeysByID))
		for _, key := range removedKeysByID {
			removedKeys = append(removedKeys, key)
		}
		logSARoleAudit(c, "role_permissions_changed", id, fiber.Map{
			"role_name": role.Name,
			"added":     addedKeys,
			"removed":   removedKeys,
		})
	}
	return c.JSON(fiber.Map{"success": true})
}
