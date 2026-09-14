package service

// SAUserService administra los usuarios del panel central (SuperAdminUser): creación, datos
// básicos, asignación de rol granular (RoleID), rol de sistema (Role: admin/superadmin), estado
// (Active), reset de contraseña y borrado (soft-delete). Complementa a SARoleService — este
// archivo NUNCA administra SARole/SAPermission/SARolePermission, solo SuperAdminUser.
//
// Réplica del mismo patrón que SARoleService (Fase 2): el servicio contiene las invariantes
// (delegación, cuenta protegida, último superadmin, transacciones); el handler valida el
// request, arma el DTO, llama a este servicio y traduce sus errores centinela a HTTP.
//
// Reglas de seguridad de este servicio (Fase 5, etapa 3, Grupo 7, Paso E — todas verificadas con
// tests dedicados en internal/superadmin/handler/*_grupo7_test.go):
//
//   - Role (admin/superadmin) y RoleID (SARole) son conceptos completamente independientes.
//     Create() NUNCA acepta Role — todo usuario nuevo nace con Role="admin"; la ÚNICA función de
//     este archivo capaz de escribir Role="superadmin" es ChangeSystemRole(), y solo la alcanza
//     un actor que YA es superadmin real (verificado aquí también, no solo en la ruta —
//     RequireSuperAdminOnly() en routes.go es la primera barrera, esta es la segunda).
//   - Techo de delegación (middleware.CanDelegateAll, Paso C): Create() (si recibe RoleID) y
//     ChangeRole() exigen que TODOS los permisos efectivos del rol destino sean delegables por el
//     actor. Esto es lo único que impide que RoleID se use como atajo de escalamiento — nunca se
//     asume que "el actor tiene usuarios_central.change_role" ya es suficiente.
//   - Cuenta protegida: ningún actor salvo un superadmin real puede ResetPassword/ChangeStatus/
//     Destroy sobre un usuario con Role=="superadmin" — ni con el permiso granular correspondiente.
//     UpdateBasicInfo (nombre/email) queda deliberadamente FUERA de esta regla (no es una vía de
//     escalamiento ni de toma de control de la cuenta).
//   - Último superadmin: ChangeSystemRole (al demover), ChangeStatus (al desactivar un
//     superadmin) y Destroy (sobre un superadmin) nunca pueden dejar el sistema con cero
//     superadmins activos — comprobación y mutación dentro de la MISMA transacción con bloqueo de
//     fila (clause.Locking{Strength:"UPDATE"}, mismo idioma que pkg/saas/payments.go).
import (
	"errors"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Errores centinela — mismo criterio que SARoleService: distinguen 400/403/404/409 sin parsear
// texto. "No encontrado" (usuario o rol) reutiliza gorm.ErrRecordNotFound directamente.
var (
	// 400 — validación de input.
	ErrSAUserNameRequired      = errors.New("el nombre es requerido")
	ErrSAUserEmailRequired     = errors.New("el email es requerido")
	ErrSAUserPasswordTooShort  = errors.New("la contraseña debe tener mínimo 8 caracteres")
	ErrSAUserInvalidSystemRole = errors.New("role debe ser admin o superadmin")
	ErrSAUserActiveRequired    = errors.New("active es requerido")

	// 403 — anti-escalamiento / cuenta protegida.
	ErrSAUserCannotDelegate   = errors.New("no puedes delegar uno o más permisos del rol destino")
	ErrSAUserNotSuperadmin    = errors.New("esta operación está reservada al superadmin real")
	ErrSAUserProtectedAccount = errors.New("no puedes operar sobre una cuenta superadmin sin ser superadmin real")

	// 409 — conflicto con el estado actual.
	ErrSAUserEmailTaken     = errors.New("el email ya está registrado")
	ErrSAUserLastSuperadmin = errors.New("no puede quedar el sistema sin ningún superadmin activo")
)

type SAUserService struct {
	db      *gorm.DB
	roleSvc *SARoleService
}

func NewSAUserService(db *gorm.DB) *SAUserService {
	return &SAUserService{db: db, roleSvc: NewSARoleService(db)}
}

// =================== Lecturas ===================

func (s *SAUserService) GetByID(id uint) (*database.SuperAdminUser, error) {
	var user database.SuperAdminUser
	if err := s.db.First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// =================== Helpers internos ===================

func normalizeEmail(email string) string {
	return strings.TrimSpace(strings.ToLower(email))
}

// ensureCanDelegateRole valida que el rol exista y que TODOS sus permisos efectivos sean
// delegables por el actor (middleware.CanDelegateAll) — único punto de esta comprobación,
// reutilizado por Create (si recibe RoleID) y ChangeRole.
func (s *SAUserService) ensureCanDelegateRole(actorClaims *middleware.SuperAdminClaims, roleID uint) (*database.SARole, error) {
	role, err := s.roleSvc.GetByID(roleID)
	if err != nil {
		return nil, err
	}
	keys, err := s.roleSvc.GetRolePermissionKeys(roleID)
	if err != nil {
		return nil, err
	}
	if !middleware.CanDelegateAll(actorClaims, keys) {
		return nil, ErrSAUserCannotDelegate
	}
	return role, nil
}

// lockActiveSuperadmins bloquea (SELECT ... FOR UPDATE, vía clause.Locking) y retorna TODAS las
// filas actualmente Role=="superadmin" && Active==true && no eliminadas — debe llamarse SIEMPRE
// dentro de una transacción; el bloqueo se mantiene hasta el commit/rollback. Mismo idioma que
// pkg/saas/payments.go (clause.Locking{Strength:"UPDATE"}), la única forma de garantizar la
// invariante bajo concurrencia (ver comentario de ensureNotLastActiveSuperadmin).
func lockActiveSuperadmins(tx *gorm.DB) ([]database.SuperAdminUser, error) {
	var rows []database.SuperAdminUser
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("role = ? AND active = ? AND deleted_at IS NULL", "superadmin", true).
		Find(&rows).Error
	return rows, err
}

// ensureNotLastActiveSuperadmin verifica, DENTRO de la transacción que ya bloqueó las filas
// (lockActiveSuperadmins), que remover/demover/desactivar a `targetID` de la condición
// "superadmin activo" deje al menos un superadmin operativo. Debe llamarse siempre con las filas
// ya bloqueadas por lockActiveSuperadmins en la MISMA tx — el conteo y la mutación posterior
// ocurren sin soltar el bloqueo, así que dos transacciones concurrentes nunca pueden leer ambas
// "queda 1 más" y proceder las dos (la segunda espera a que la primera confirme, y ya ve el
// conteo actualizado — ver internal/superadmin/service/sa_user_service_test.go para la prueba de
// concurrencia real y su documentación honesta sobre qué garantiza SQLite en los tests frente a
// MySQL en producción).
func ensureNotLastActiveSuperadmin(tx *gorm.DB, targetID uint) error {
	rows, err := lockActiveSuperadmins(tx)
	if err != nil {
		return err
	}
	remaining := 0
	for _, r := range rows {
		if r.ID != targetID {
			remaining++
		}
	}
	if remaining == 0 {
		return ErrSAUserLastSuperadmin
	}
	return nil
}

// isProtectedAccount: Role=="superadmin" es intocable (reset_password/change_status/destroy)
// para cualquier actor que no sea él mismo un superadmin real.
func isProtectedAccount(target *database.SuperAdminUser, actorClaims *middleware.SuperAdminClaims) bool {
	if target.Role != "superadmin" {
		return false
	}
	return actorClaims == nil || actorClaims.Role != "superadmin"
}

// =================== Escrituras ===================

// Create crea un usuario del panel central. SIEMPRE con Role="admin" — este método no acepta ni
// puede producir Role="superadmin" bajo ninguna circunstancia (ver ChangeSystemRole, la única vía
// legítima). Si roleID no es nil, se exige el techo de delegación sobre los permisos del rol.
func (s *SAUserService) Create(actorClaims *middleware.SuperAdminClaims, name, email, password string, roleID *uint) (*database.SuperAdminUser, error) {
	name = strings.TrimSpace(name)
	email = normalizeEmail(email)
	password = strings.TrimSpace(password)

	if name == "" {
		return nil, ErrSAUserNameRequired
	}
	if email == "" {
		return nil, ErrSAUserEmailRequired
	}
	if len(password) < 8 {
		return nil, ErrSAUserPasswordTooShort
	}

	var existing database.SuperAdminUser
	if err := s.db.Where("email = ?", email).First(&existing).Error; err == nil {
		return nil, ErrSAUserEmailTaken
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if roleID != nil {
		if _, err := s.ensureCanDelegateRole(actorClaims, *roleID); err != nil {
			return nil, err
		}
	}

	user := &database.SuperAdminUser{Name: name, Email: email, Role: "admin", RoleID: roleID, Active: true}
	if err := user.SetPassword(password); err != nil {
		return nil, err
	}
	if err := s.db.Create(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

// UpdateBasicInfo modifica ÚNICAMENTE name/email. Nunca toca Role, RoleID, Active, Password,
// TokenVersion ni DeletedAt — no porque el caller los filtre, sino porque este método no acepta
// esos parámetros en absoluto (defensa estructural contra mass assignment, no solo por DTO en la
// capa HTTP). nil = "no tocar ese campo"; string vacío tras trim = error de validación.
func (s *SAUserService) UpdateBasicInfo(targetID uint, name, email *string) (*database.SuperAdminUser, error) {
	user, err := s.GetByID(targetID)
	if err != nil {
		return nil, err
	}

	updates := map[string]interface{}{}
	if name != nil {
		v := strings.TrimSpace(*name)
		if v == "" {
			return nil, ErrSAUserNameRequired
		}
		updates["name"] = v
	}
	if email != nil {
		v := normalizeEmail(*email)
		if v == "" {
			return nil, ErrSAUserEmailRequired
		}
		var existing database.SuperAdminUser
		if err := s.db.Where("email = ? AND id <> ?", v, user.ID).First(&existing).Error; err == nil {
			return nil, ErrSAUserEmailTaken
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		updates["email"] = v
	}
	if len(updates) == 0 {
		return user, nil
	}
	if err := s.db.Model(user).Updates(updates).Error; err != nil {
		return nil, err
	}
	return s.GetByID(targetID)
}

// ChangeRole asigna el RoleID granular de un usuario (nunca toca Role). Techo de delegación
// obligatorio sobre TODOS los permisos efectivos del rol destino — se aplica igual sin importar
// si el objetivo es el propio actor u otro usuario (no hay caso especial de "self": el techo ya
// impide que nadie se asigne a sí mismo, o a otro, un rol que exceda lo que puede delegar).
// Transaccional: RoleID + IncrementTokenVersion se aplican juntos o no se aplica nada.
func (s *SAUserService) ChangeRole(actorClaims *middleware.SuperAdminClaims, targetID, newRoleID uint) (*database.SuperAdminUser, *database.SARole, error) {
	user, err := s.GetByID(targetID)
	if err != nil {
		return nil, nil, err
	}
	role, err := s.ensureCanDelegateRole(actorClaims, newRoleID)
	if err != nil {
		return nil, nil, err
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(user).Update("role_id", newRoleID).Error; err != nil {
			return err
		}
		return user.IncrementTokenVersion(tx)
	})
	if err != nil {
		return nil, nil, err
	}
	updated, err := s.GetByID(targetID)
	return updated, role, err
}

// ChangeSystemRole es la ÚNICA función de todo el sistema capaz de escribir Role="superadmin" o
// de demover un superadmin a "admin". Comprobación de actor redundante con la ruta a propósito
// (RequireSuperAdminOnly ya lo exige en routes.go; esto es la segunda barrera, defensa en
// profundidad — si algún día una ruta quedara mal cableada, este método sigue rechazando).
// Demover (superadmin→admin) exige que quede al menos otro superadmin activo, comprobado y
// aplicado dentro de la MISMA transacción bloqueada (ver ensureNotLastActiveSuperadmin).
// Promover (admin→superadmin) nunca reduce el conteo de superadmins, así que no necesita esa
// comprobación — solo la barrera de actor.
func (s *SAUserService) ChangeSystemRole(actorClaims *middleware.SuperAdminClaims, targetID uint, newRole string) (*database.SuperAdminUser, error) {
	if actorClaims == nil || actorClaims.Role != "superadmin" {
		return nil, ErrSAUserNotSuperadmin
	}
	newRole = strings.TrimSpace(newRole)
	if newRole != "admin" && newRole != "superadmin" {
		return nil, ErrSAUserInvalidSystemRole
	}

	user, err := s.GetByID(targetID)
	if err != nil {
		return nil, err
	}
	if user.Role == newRole {
		return user, nil // no-op: no es un evento de seguridad, no se invalida sesión de más
	}
	demoting := user.Role == "superadmin" && newRole == "admin"

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if demoting {
			if err := ensureNotLastActiveSuperadmin(tx, user.ID); err != nil {
				return err
			}
		}
		if err := tx.Model(user).Update("role", newRole).Error; err != nil {
			return err
		}
		return user.IncrementTokenVersion(tx)
	})
	if err != nil {
		return nil, err
	}
	return s.GetByID(targetID)
}

// ChangeStatus activa/desactiva un usuario. Cuenta protegida: un actor no-superadmin nunca puede
// tocar el estado de una cuenta Role=="superadmin" (ni activarla ni desactivarla — ver comentario
// de isProtectedAccount, extensión deliberada del diseño aprobado: la cuenta protegida queda
// completamente fuera del alcance de un no-superadmin, no solo para desactivarla). Desactivar un
// superadmin, incluso hecho por otro superadmin real, exige que quede al menos otro activo.
func (s *SAUserService) ChangeStatus(actorClaims *middleware.SuperAdminClaims, targetID uint, active bool) (*database.SuperAdminUser, error) {
	user, err := s.GetByID(targetID)
	if err != nil {
		return nil, err
	}
	if isProtectedAccount(user, actorClaims) {
		return nil, ErrSAUserProtectedAccount
	}
	if user.Active == active {
		return user, nil // no-op
	}
	deactivatingSuperadmin := !active && user.Role == "superadmin"

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if deactivatingSuperadmin {
			if err := ensureNotLastActiveSuperadmin(tx, user.ID); err != nil {
				return err
			}
		}
		if err := tx.Model(user).Update("active", active).Error; err != nil {
			return err
		}
		return user.IncrementTokenVersion(tx)
	})
	if err != nil {
		return nil, err
	}
	return s.GetByID(targetID)
}

// ResetPassword — cuenta protegida aplica igual que ChangeStatus/Destroy. Nunca registra ni
// retorna la contraseña (el caller HTTP tampoco debe hacerlo — ver AuthSAHandler.ResetUserPasswordAPI).
func (s *SAUserService) ResetPassword(actorClaims *middleware.SuperAdminClaims, targetID uint, newPassword string) (*database.SuperAdminUser, error) {
	user, err := s.GetByID(targetID)
	if err != nil {
		return nil, err
	}
	if isProtectedAccount(user, actorClaims) {
		return nil, ErrSAUserProtectedAccount
	}
	newPassword = strings.TrimSpace(newPassword)
	if len(newPassword) < 8 {
		return nil, ErrSAUserPasswordTooShort
	}
	if err := user.SetPassword(newPassword); err != nil {
		return nil, err
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(user).Update("password", user.Password).Error; err != nil {
			return err
		}
		return user.IncrementTokenVersion(tx)
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// Destroy hace soft-delete (DeletedAt, ya existente en el modelo — nunca borrado físico). Cuenta
// protegida + último superadmin, igual criterio que ChangeStatus. No se llama a
// IncrementTokenVersion aparte: verifySuperAdminSession ya excluye filas con deleted_at (First()
// de GORM), así que el soft-delete invalida la sesión por sí solo en el siguiente request — un
// incremento adicional sería una escritura sin efecto de seguridad extra.
func (s *SAUserService) Destroy(actorClaims *middleware.SuperAdminClaims, targetID uint) (*database.SuperAdminUser, error) {
	user, err := s.GetByID(targetID)
	if err != nil {
		return nil, err
	}
	if isProtectedAccount(user, actorClaims) {
		return nil, ErrSAUserProtectedAccount
	}
	deletingSuperadmin := user.Role == "superadmin"

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if deletingSuperadmin {
			// Un superadmin eliminado deja de contar como "activo" igual que uno desactivado —
			// se excluye con el mismo criterio (ensureNotLastActiveSuperadmin ya compara por ID,
			// sea la baja por Active=false o por DeletedAt).
			if err := ensureNotLastActiveSuperadmin(tx, user.ID); err != nil {
				return err
			}
		}
		return tx.Delete(user).Error
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}
