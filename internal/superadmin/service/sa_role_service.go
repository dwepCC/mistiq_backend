package service

// SARoleService administra el RBAC del panel central: SARole, SAPermission, SARolePermission.
//
// Réplica adaptada de internal/users/service/role_service.go (RBAC de tenants). Diferencias
// deliberadas frente al original, por las reglas de seguridad de la Fase 2:
//   - Create() siempre crea roles con IsSystem=false — los roles de sistema (Admin/Soporte/
//     Finanzas) solo los crea el seed (database.SASeedRolesAndPermissions), nunca este servicio.
//   - El nombre "Superadmin" (cualquier variación de mayúsculas/minúsculas) está reservado y
//     jamás puede crearse ni asignarse a un rol vía Create/Update — el superadmin real es un
//     bypass total por SuperAdminUser.Role, completamente ajeno a la tabla sa_roles.
//   - Update() no permite renombrar un rol de sistema (rompería el match por nombre que usa el
//     seed para decidir "el rol ya existe, no lo toco" — ver database.SASeedRolesAndPermissions).
//   - Delete() rechaza roles de sistema Y roles que todavía tengan usuarios asignados (RoleID),
//     para no dejar SuperAdminUser.RoleID apuntando a un rol inexistente.
//   - SetRolePermissions() valida que TODOS los IDs de permiso existan antes de tocar la BD, y
//     hace todo el reemplazo (borrar + recrear) dentro de una única transacción: o se aplica
//     completo, o no se aplica nada.
//
// Este servicio NUNCA escribe en superadmin_users.role (el campo de bypass) ni en
// superadmin_users.role_id — la asignación de rol a un usuario es responsabilidad de otro flujo
// (fuera del alcance de esta fase) que deberá, a su vez, impedir que un usuario se asigne a sí
// mismo o a otros un rol con permisos que él mismo no posee. Aquí solo se deja el terreno
// preparado: no existe ninguna ruta de código en este archivo capaz de convertir a un usuario en
// superadmin ni de tocar el RBAC de tenants (tablas tenant_roles/tenant_permissions/
// tenant_role_permissions, en una BD distinta por tenant).

import (
	"errors"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// reservedSuperadminRoleName es el nombre que ningún rol del RBAC central puede usar: el
// superadmin real no es una fila de sa_roles, es el bypass de SuperAdminUser.Role.
const reservedSuperadminRoleName = "superadmin"

// Errores centinela — permiten que la capa HTTP (Fase 3) distinga 400 (input inválido) de
// 409 (conflicto con el estado actual del recurso) sin parsear mensajes de texto. "No
// encontrado" se señaliza reusando gorm.ErrRecordNotFound directamente (mismo patrón que el
// resto del proyecto, ver plan_handler.go GetAPI), sin un sentinel propio.
var (
	// 400 — validación de input.
	ErrSARoleNameRequired       = errors.New("el nombre del rol es requerido")
	ErrSARoleNameTooLong        = errors.New("el nombre del rol no puede superar 100 caracteres")
	ErrSARoleDescriptionTooLong = errors.New("la descripción no puede superar 255 caracteres")
	ErrSAReservedRoleName       = errors.New("el nombre 'Superadmin' está reservado: el superadmin real no depende de un rol de BD")
	ErrSAPermissionNotFound     = errors.New("uno o más permisos no existen")

	// 409 — conflicto con el estado actual del recurso.
	ErrSARoleNameTaken          = errors.New("ya existe un rol con ese nombre")
	ErrSASystemRoleNotRenamable = errors.New("no se puede renombrar un rol del sistema")
	ErrSASystemRoleNotDeletable = errors.New("no se puede eliminar un rol del sistema")
	ErrSARoleHasAssignedUsers   = errors.New("no se puede eliminar un rol que tiene usuarios asignados")
)

type SARoleService struct {
	db *gorm.DB
}

func NewSARoleService(db *gorm.DB) *SARoleService {
	return &SARoleService{db: db}
}

// =================== Lecturas ===================

// List retorna todos los roles del panel central, ordenados por nombre.
func (s *SARoleService) List() ([]database.SARole, error) {
	var roles []database.SARole
	err := s.db.Order("name ASC").Find(&roles).Error
	return roles, err
}

// GetByID retorna un rol por su ID.
func (s *SARoleService) GetByID(id uint) (*database.SARole, error) {
	var role database.SARole
	if err := s.db.First(&role, id).Error; err != nil {
		return nil, err
	}
	return &role, nil
}

// AllPermissions retorna el catálogo completo de permisos disponibles (solo lectura — este
// servicio no crea/edita/elimina permisos individuales, eso lo gobierna el seed idempotente).
func (s *SARoleService) AllPermissions() ([]database.SAPermission, error) {
	var perms []database.SAPermission
	err := s.db.Order("module ASC, action ASC").Find(&perms).Error
	return perms, err
}

// RolePermissions retorna los IDs de permisos asignados a un rol.
func (s *SARoleService) RolePermissions(roleID uint) ([]uint, error) {
	var rps []database.SARolePermission
	if err := s.db.Where("role_id = ?", roleID).Find(&rps).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, len(rps))
	for i, rp := range rps {
		ids[i] = rp.PermissionID
	}
	return ids, nil
}

// GetRolePermissionKeys retorna los permisos de un rol en formato "module.action" — el formato
// que se cargará en el JWT en la Fase 4. Rol inexistente → lista vacía, sin error (permite
// llamarla de forma defensiva; la validación de existencia del rol la hace el llamador si le
// importa distinguir "rol sin permisos" de "rol inexistente").
func (s *SARoleService) GetRolePermissionKeys(roleID uint) ([]string, error) {
	var perms []database.SAPermission
	err := s.db.Table("sa_permissions").
		Select("sa_permissions.module, sa_permissions.action").
		Joins("INNER JOIN sa_role_permissions ON sa_role_permissions.permission_id = sa_permissions.id").
		Where("sa_role_permissions.role_id = ?", roleID).
		Find(&perms).Error
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(perms))
	for i, p := range perms {
		keys[i] = p.Module + "." + p.Action
	}
	return keys, nil
}

// PermissionKeysByIDs resuelve un conjunto de IDs de permiso a sus claves "module.action" —
// usado tanto por el techo de delegación (middleware.CanDelegateAll necesita claves, no IDs)
// como por la auditoría de role_permissions_changed (permisos agregados/removidos en formato
// legible). IDs que no existan en la BD se omiten en silencio: la validación de "todos los IDs
// deben existir" ya la hace SetRolePermissions antes de tocar la BD — esta función se usa sobre
// IDs que ya se sabe que son válidos (típicamente el resultado de RolePermissions()).
func (s *SARoleService) PermissionKeysByIDs(ids []uint) (map[uint]string, error) {
	out := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var perms []database.SAPermission
	if err := s.db.Where("id IN ?", ids).Find(&perms).Error; err != nil {
		return nil, err
	}
	for _, p := range perms {
		out[p.ID] = p.Module + "." + p.Action
	}
	return out, nil
}

// =================== Escrituras ===================

// Create crea un rol personalizado (IsSystem=false siempre — los roles de sistema solo los crea
// el seed).
func (s *SARoleService) Create(name, description string) (*database.SARole, error) {
	name = strings.TrimSpace(name)
	if err := validateSARoleName(name); err != nil {
		return nil, err
	}
	if err := validateSARoleDescription(description); err != nil {
		return nil, err
	}

	var existing database.SARole
	if err := s.db.Where("name = ?", name).First(&existing).Error; err == nil {
		return nil, ErrSARoleNameTaken
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	role := &database.SARole{Name: name, Description: description, IsSystem: false}
	if err := s.db.Create(role).Error; err != nil {
		return nil, err
	}
	return role, nil
}

// Update edita nombre/descripción de un rol. Los roles de sistema no pueden renombrarse (el
// nombre es la clave que usa el seed para no duplicar/pisar roles ya existentes); su descripción
// sí puede editarse libremente.
func (s *SARoleService) Update(id uint, name, description string) error {
	var role database.SARole
	if err := s.db.First(&role, id).Error; err != nil {
		return err
	}

	name = strings.TrimSpace(name)
	if err := validateSARoleName(name); err != nil {
		return err
	}
	if err := validateSARoleDescription(description); err != nil {
		return err
	}

	if role.IsSystem && name != role.Name {
		return ErrSASystemRoleNotRenamable
	}

	if name != role.Name {
		var existing database.SARole
		if err := s.db.Where("name = ? AND id <> ?", name, id).First(&existing).Error; err == nil {
			return ErrSARoleNameTaken
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}

	return s.db.Model(&database.SARole{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":        name,
		"description": description,
	}).Error
}

// Delete elimina un rol personalizado. Rechaza roles de sistema y roles que todavía tengan
// usuarios asignados (para no dejar superadmin_users.role_id apuntando a un rol inexistente).
// Elimina también sus relaciones sa_role_permissions dentro de la misma transacción.
func (s *SARoleService) Delete(id uint) error {
	var role database.SARole
	if err := s.db.First(&role, id).Error; err != nil {
		return err
	}
	if role.IsSystem {
		return ErrSASystemRoleNotDeletable
	}

	var usersWithRole int64
	if err := s.db.Model(&database.SuperAdminUser{}).Where("role_id = ?", id).Count(&usersWithRole).Error; err != nil {
		return err
	}
	if usersWithRole > 0 {
		return ErrSARoleHasAssignedUsers
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", id).Delete(&database.SARolePermission{}).Error; err != nil {
			return err
		}
		return tx.Delete(&role).Error
	})
}

// SetRolePermissions reemplaza todos los permisos de un rol de forma atómica: o se validan y
// aplican todas las relaciones nuevas, o no se modifica nada (transacción). Valida que el rol y
// TODOS los permisos indicados existan antes de tocar la BD, y descarta IDs duplicados/cero.
//
// Fase 4: al terminar, invalida (TokenVersion++) la sesión de TODO usuario que tenga este rol
// asignado — sus JWT ya emitidos llevan cacheados los permisos anteriores
// (SuperAdminClaims.Permissions), así que deben quedar sin efecto de inmediato, no recién en 8h.
func (s *SARoleService) SetRolePermissions(roleID uint, permissionIDs []uint) error {
	var role database.SARole
	if err := s.db.First(&role, roleID).Error; err != nil {
		return err
	}

	uniqueIDs := dedupePermissionIDs(permissionIDs)

	if len(uniqueIDs) > 0 {
		var count int64
		if err := s.db.Model(&database.SAPermission{}).Where("id IN ?", uniqueIDs).Count(&count).Error; err != nil {
			return err
		}
		if int(count) != len(uniqueIDs) {
			return ErrSAPermissionNotFound
		}
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).Delete(&database.SARolePermission{}).Error; err != nil {
			return err
		}
		for _, pid := range uniqueIDs {
			if err := tx.Create(&database.SARolePermission{RoleID: roleID, PermissionID: pid}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	return s.invalidateSessionsForRole(roleID)
}

// invalidateSessionsForRole incrementa TokenVersion de todos los SuperAdminUser con este RoleID,
// en un único UPDATE atómico (evita N consultas + condiciones de carrera entre ellas).
func (s *SARoleService) invalidateSessionsForRole(roleID uint) error {
	return s.db.Model(&database.SuperAdminUser{}).
		Where("role_id = ?", roleID).
		UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error
}

// =================== Validaciones ===================

func validateSARoleName(name string) error {
	if name == "" {
		return ErrSARoleNameRequired
	}
	if len(name) > 100 {
		return ErrSARoleNameTooLong
	}
	if strings.EqualFold(name, reservedSuperadminRoleName) {
		return ErrSAReservedRoleName
	}
	return nil
}

func validateSARoleDescription(description string) error {
	if len(description) > 255 {
		return ErrSARoleDescriptionTooLong
	}
	return nil
}

func dedupePermissionIDs(ids []uint) []uint {
	seen := make(map[uint]bool, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
