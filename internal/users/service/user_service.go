package service

import (
	"errors"
	"strings"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/saas"

	"gorm.io/gorm"
)

type UserService struct {
	db *gorm.DB
	// tenantID activa el tope de usuarios del plan en altas y reactivaciones (ver
	// saas.ReserveUserSeat). 0 = sin validar (uso interno/tests).
	tenantID uint
}

func NewUserService(db *gorm.DB) *UserService {
	return &UserService{db: db}
}

// WithTenant devuelve el servicio validando el límite de usuarios del plan de ese tenant.
func (s *UserService) WithTenant(tenantID uint) *UserService {
	return &UserService{db: s.db, tenantID: tenantID}
}

// inSeatTx ejecuta fn en una transacción que ya reservó un cupo de usuario del plan. Si no hay
// tenant configurado o el tope no aplica, fn corre directamente sobre la conexión del servicio.
func (s *UserService) inSeatTx(fn func(tx *UserService) error) error {
	if s.tenantID == 0 {
		return fn(s)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := saas.ReserveUserSeat(tx, s.tenantID); err != nil {
			return err
		}
		return fn(&UserService{db: tx})
	})
}

type UserListParams struct {
	Query  string
	RoleID uint
	Active *bool
	// OnlyDeleted lista únicamente los usuarios eliminados (para poder restaurarlos).
	OnlyDeleted bool
}

func (s *UserService) List(params UserListParams) ([]database.TenantUser, error) {
	var users []database.TenantUser
	q := s.db.Model(&database.TenantUser{})
	if params.OnlyDeleted {
		q = q.Unscoped().Where("tenant_users.deleted_at IS NOT NULL")
	}
	if params.Query != "" {
		q = q.Where("name LIKE ? OR email LIKE ?", "%"+params.Query+"%", "%"+params.Query+"%")
	}
	if params.RoleID > 0 {
		q = q.Where("role_id = ?", params.RoleID)
	}
	if params.Active != nil {
		q = q.Where("active = ?", *params.Active)
	}
	err := q.Order("name ASC").Find(&users).Error
	return users, err
}

func (s *UserService) GetByID(id uint) (*database.TenantUser, error) {
	var user database.TenantUser
	if err := s.db.First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

type CreateUserInput struct {
	RoleID    uint   `json:"role_id"`
	BranchID  *uint  `json:"branch_id"`
	BranchIDs []uint `json:"branch_ids"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	Phone     string `json:"phone"`
	Active    bool   `json:"active"`
	// ReplaceDeleted: si el correo pertenece a un usuario eliminado, libera ese correo (renombrando
	// al eliminado, que conserva su historial) y crea un usuario nuevo. Sin esto Create devuelve
	// *database.DeletedUserEmailError para que el cliente elija entre restaurar o crear uno nuevo.
	ReplaceDeleted bool `json:"replace_deleted"`
}

func (s *UserService) Create(input CreateUserInput) (*database.TenantUser, error) {
	// Se valida siempre, también con active=false: el tag `default:true` de TenantUser hace que GORM
	// inserte activo a un bool en cero, así que "crear inactivo" no es una vía para saltarse el tope.
	var created *database.TenantUser
	err := s.inSeatTx(func(tx *UserService) error {
		u, err := tx.create(input)
		created = u
		return err
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *UserService) create(input CreateUserInput) (*database.TenantUser, error) {
	if input.Name == "" || input.Email == "" || input.Password == "" {
		return nil, errors.New("nombre, email y contraseña son requeridos")
	}
	if input.RoleID == 0 {
		return nil, errors.New("rol es requerido")
	}

	input.Email = strings.TrimSpace(input.Email)
	var existing database.TenantUser
	if err := s.db.Where("email = ?", input.Email).First(&existing).Error; err == nil {
		return nil, errors.New("el email ya está registrado")
	}
	if deleted, err := database.FindDeletedUserByEmail(s.db, input.Email); err != nil {
		return nil, err
	} else if deleted != nil {
		if !input.ReplaceDeleted {
			return nil, database.NewDeletedUserEmailError(s.db, deleted)
		}
		if err := database.ArchiveDeletedUserEmail(s.db, deleted); err != nil {
			return nil, err
		}
	}

	user := &database.TenantUser{
		RoleID:   input.RoleID,
		BranchID: input.BranchID,
		Name:     input.Name,
		Email:    input.Email,
		Phone:    input.Phone,
		Active:   input.Active,
	}
	if database.TenantBranchMultiSchemaReady(s.db) {
		home := input.BranchID
		user.HomeBranchID = home
		var role database.TenantRole
		if err := s.db.First(&role, input.RoleID).Error; err == nil {
			user.CanSwitchBranch = branch.IsTenantAdmin(role.Name)
		}
	}
	if err := user.SetPassword(input.Password); err != nil {
		return nil, err
	}
	if err := s.db.Create(user).Error; err != nil {
		return nil, err
	}
	branchIDs := input.BranchIDs
	if len(branchIDs) == 0 && input.BranchID != nil && *input.BranchID > 0 {
		branchIDs = []uint{*input.BranchID}
	}
	if len(branchIDs) > 0 {
		if err := branch.SetUserAssignedBranches(s.db, user.ID, branchIDs, false); err != nil {
			return nil, err
		}
		_ = s.db.First(user, user.ID)
	}
	return user, nil
}

type UpdateUserInput struct {
	RoleID    uint   `json:"role_id"`
	BranchID  *uint  `json:"branch_id"`
	BranchIDs []uint `json:"branch_ids"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	Phone     string `json:"phone"`
	Active    bool   `json:"active"`
}

func (s *UserService) TenantRoleEditLocked(userID uint, roleID uint) (bool, error) {
	isOwner, err := database.IsTenantOwnerUser(s.db, userID)
	if err != nil || !isOwner {
		return false, err
	}
	var role database.TenantRole
	if err := s.db.First(&role, roleID).Error; err != nil {
		return false, err
	}
	return branch.IsTenantAdmin(role.Name), nil
}

func (s *UserService) Update(id uint, input UpdateUserInput) error {
	user, err := s.GetByID(id)
	if err != nil {
		return errors.New("usuario no encontrado")
	}
	if input.Active && !user.Active {
		// Reactivar ocupa un cupo del plan igual que un alta nueva.
		return s.inSeatTx(func(tx *UserService) error { return tx.update(id, input) })
	}
	return s.update(id, input)
}

func (s *UserService) update(id uint, input UpdateUserInput) error {
	user, err := s.GetByID(id)
	if err != nil {
		return errors.New("usuario no encontrado")
	}

	if input.RoleID > 0 && input.RoleID != user.RoleID {
		locked, err := s.TenantRoleEditLocked(id, user.RoleID)
		if err != nil {
			return err
		}
		if locked {
			return errors.New("no se puede cambiar el rol del usuario principal del sistema")
		}
	}

	// El usuario principal (el primero creado al aprovisionar el tenant) no puede quedar
	// inactivo: es quien recibe el acceso maestro de soporte (ver TenantService.MasterAccess,
	// que ya rechaza "usuario propietario inactivo") y, sin él, nadie puede reactivar la cuenta.
	// Editar sus demás datos (nombre, email, teléfono, contraseña) sigue permitido.
	if !input.Active && user.Active {
		isOwner, err := database.IsTenantOwnerUser(s.db, id)
		if err != nil {
			return err
		}
		if isOwner {
			return errors.New("no se puede desactivar al usuario principal del sistema")
		}
	}

	if newEmail := strings.TrimSpace(input.Email); newEmail != "" && !strings.EqualFold(newEmail, user.Email) {
		if err := s.ensureEmailFree(newEmail, id); err != nil {
			return err
		}
		input.Email = newEmail
	}

	updates := map[string]interface{}{
		"name":      input.Name,
		"email":     input.Email,
		"phone":     input.Phone,
		"role_id":   input.RoleID,
		"active":    input.Active,
		"branch_id": input.BranchID,
	}
	if database.TenantBranchMultiSchemaReady(s.db) {
		updates["home_branch_id"] = input.BranchID
		var role database.TenantRole
		if err := s.db.First(&role, input.RoleID).Error; err == nil {
			updates["can_switch_branch"] = branch.IsTenantAdmin(role.Name)
		}
	}
	branchIDs := input.BranchIDs
	if len(branchIDs) == 0 && input.BranchID != nil && *input.BranchID > 0 {
		branchIDs = []uint{*input.BranchID}
	}
	if len(branchIDs) > 0 {
		if err := branch.SetUserAssignedBranches(s.db, id, branchIDs, true); err != nil {
			return err
		}
	} else if user.BranchID != nil && input.BranchID != nil && *user.BranchID != *input.BranchID {
		_ = branch.BumpSessionVersion(s.db, id)
	} else if (user.BranchID == nil) != (input.BranchID == nil) {
		_ = branch.BumpSessionVersion(s.db, id)
	}

	passwordChanged := false
	if input.Password != "" {
		if err := user.SetPassword(input.Password); err != nil {
			return err
		}
		updates["password"] = user.Password
		passwordChanged = true
	}

	if err := s.db.Model(&database.TenantUser{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return err
	}
	// Revoca los access tokens vigentes del usuario (409 SESSION_UPDATED) cuando cambia su rol, su estado
	// activo o su contraseña: el JWT lleva rol y permisos y vive hasta 24 h.
	if (input.RoleID > 0 && input.RoleID != user.RoleID) || input.Active != user.Active || passwordChanged {
		_ = branch.BumpSessionVersion(s.db, id)
	}
	return nil
}

// Delete elimina (borrado lógico) a un usuario. Se rechaza cuando:
//   - es el usuario principal: es quien recibe el acceso maestro de soporte y el que define
//     "principal" (el de menor id entre los no eliminados); si se borra, ese título pasa a otro
//     usuario sin que nadie lo note (incidente industrialrafaz: se borraron todos los admins).
//   - es el último administrador activo: sin él nadie podría gestionar roles ni usuarios.
// ensureEmailFree valida que el correo no lo use otro usuario, ni activo ni eliminado (el índice
// único cuenta también a los eliminados; sin esto el cliente recibía el error crudo de MySQL).
func (s *UserService) ensureEmailFree(email string, excludeID uint) error {
	var existing database.TenantUser
	if err := s.db.Where("email = ? AND id <> ?", email, excludeID).First(&existing).Error; err == nil {
		return errors.New("el email ya está registrado")
	}
	deleted, err := database.FindDeletedUserByEmail(s.db, email)
	if err != nil {
		return err
	}
	if deleted != nil && deleted.ID != excludeID {
		return errors.New("ese correo pertenece a un usuario eliminado: restáurelo desde «Ver eliminados» o use otro correo")
	}
	return nil
}

// Restore reactiva a un usuario eliminado conservando su id (y con él su historial de ventas,
// cajas, etc.). Se exige contraseña nueva: la anterior no vuelve. Nombre, teléfono, rol y
// sucursales se toman de input; el correo es el del usuario eliminado. Ocupa un cupo del plan.
func (s *UserService) Restore(id uint, input CreateUserInput) (*database.TenantUser, error) {
	var restored *database.TenantUser
	err := s.inSeatTx(func(tx *UserService) error {
		u, err := tx.restore(id, input)
		restored = u
		return err
	})
	if err != nil {
		return nil, err
	}
	return restored, nil
}

func (s *UserService) restore(id uint, input CreateUserInput) (*database.TenantUser, error) {
	if strings.TrimSpace(input.Name) == "" || input.Password == "" {
		return nil, errors.New("nombre y contraseña son requeridos")
	}
	if input.RoleID == 0 {
		return nil, errors.New("rol es requerido")
	}
	var user database.TenantUser
	if err := s.db.Unscoped().First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("usuario no encontrado")
		}
		return nil, err
	}
	if !user.DeletedAt.Valid {
		return nil, errors.New("el usuario no está eliminado")
	}
	var role database.TenantRole
	if err := s.db.First(&role, input.RoleID).Error; err != nil {
		return nil, errors.New("rol no válido")
	}
	hasher := database.TenantUser{}
	if err := hasher.SetPassword(input.Password); err != nil {
		return nil, err
	}

	updates := map[string]interface{}{
		"deleted_at": nil,
		"active":     true,
		"name":       strings.TrimSpace(input.Name),
		"phone":      strings.TrimSpace(input.Phone),
		"role_id":    input.RoleID,
		"password":   hasher.Password,
	}
	if database.TenantBranchMultiSchemaReady(s.db) {
		updates["can_switch_branch"] = branch.IsTenantAdmin(role.Name)
	}
	if err := s.db.Unscoped().Model(&database.TenantUser{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, err
	}

	branchIDs := input.BranchIDs
	if len(branchIDs) == 0 && input.BranchID != nil && *input.BranchID > 0 {
		branchIDs = []uint{*input.BranchID}
	}
	if len(branchIDs) > 0 {
		if err := branch.SetUserAssignedBranches(s.db, id, branchIDs, true); err != nil {
			return nil, err
		}
	} else {
		// Conserva las sucursales que ya tenía, pero invalida cualquier sesión anterior.
		_ = branch.BumpSessionVersion(s.db, id)
	}
	var fresh database.TenantUser // struct nuevo: reusar `user` conserva el DeletedAt anterior
	if err := s.db.First(&fresh, id).Error; err != nil {
		return nil, err
	}
	return &fresh, nil
}

func (s *UserService) Delete(id uint) error {
	var user database.TenantUser
	if err := s.db.First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // ya no existe: nada que borrar
		}
		return err
	}

	isOwner, err := database.IsTenantOwnerUser(s.db, id)
	if err != nil {
		return err
	}
	if isOwner {
		return errors.New("no se puede eliminar al usuario principal del sistema")
	}

	var role database.TenantRole
	if err := s.db.First(&role, user.RoleID).Error; err == nil && branch.IsTenantAdmin(role.Name) && user.Active {
		var others int64
		if err := s.db.Model(&database.TenantUser{}).
			Joins("JOIN tenant_roles ON tenant_roles.id = tenant_users.role_id").
			Where("tenant_roles.name = ? AND tenant_users.active = ? AND tenant_users.id <> ?", role.Name, true, id).
			Count(&others).Error; err != nil {
			return err
		}
		if others == 0 {
			return errors.New("no se puede eliminar al último administrador activo")
		}
	}

	return s.db.Delete(&database.TenantUser{}, id).Error
}

func (s *UserService) ToggleActive(id uint) error {
	var user database.TenantUser
	if err := s.db.First(&user, id).Error; err != nil {
		return err
	}
	if !user.Active {
		// Reactivar ocupa un cupo del plan igual que un alta nueva.
		return s.inSeatTx(func(tx *UserService) error { return tx.toggleActive(id) })
	}
	return s.toggleActive(id)
}

func (s *UserService) toggleActive(id uint) error {
	var user database.TenantUser
	if err := s.db.First(&user, id).Error; err != nil {
		return err
	}
	// Mismo criterio que Update: el usuario principal no puede quedar inactivo. Reactivarlo (caso
	// user.Active==false) nunca se bloquea — solo la transición activo→inactivo.
	if user.Active {
		isOwner, err := database.IsTenantOwnerUser(s.db, id)
		if err != nil {
			return err
		}
		if isOwner {
			return errors.New("no se puede desactivar al usuario principal del sistema")
		}
	}
	if err := s.db.Model(&user).Update("active", !user.Active).Error; err != nil {
		return err
	}
	_ = branch.BumpSessionVersion(s.db, id)
	return nil
}

type UpdateProfileInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
}

func (s *UserService) UpdateProfile(id uint, input UpdateProfileInput) error {
	name := strings.TrimSpace(input.Name)
	email := strings.TrimSpace(input.Email)
	phone := strings.TrimSpace(input.Phone)
	if name == "" || email == "" {
		return errors.New("nombre y email son requeridos")
	}

	if err := s.ensureEmailFree(email, id); err != nil {
		return err
	}

	return s.db.Model(&database.TenantUser{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":  name,
		"email": email,
		"phone": phone,
	}).Error
}

func (s *UserService) ChangePassword(id uint, currentPassword, newPassword string) error {
	currentPassword = strings.TrimSpace(currentPassword)
	newPassword = strings.TrimSpace(newPassword)
	if currentPassword == "" || newPassword == "" {
		return errors.New("contraseña actual y nueva son requeridas")
	}
	if len(newPassword) < 8 {
		return errors.New("la nueva contraseña debe tener mínimo 8 caracteres")
	}

	user, err := s.GetByID(id)
	if err != nil {
		return errors.New("usuario no encontrado")
	}
	if !user.CheckPassword(currentPassword) {
		return errors.New("la contraseña actual no es correcta")
	}
	if err := user.SetPassword(newPassword); err != nil {
		return err
	}
	if err := s.db.Model(&database.TenantUser{}).Where("id = ?", id).Update("password", user.Password).Error; err != nil {
		return err
	}
	// Cambiar la contraseña revoca los access tokens vigentes de ESTA cuenta (incluido el de quien la cambia:
	// su próxima petición recibe 409 SESSION_UPDATED y el cliente refresca con el refresh token, que no se
	// invalida y reconstruye los claims con la versión nueva). Decisión: tolerar en vez de re-emitir aquí,
	// porque la emisión de JWT vive en internal/auth y ChangePassword no conoce el refresh token.
	_ = branch.BumpSessionVersion(s.db, id)
	return nil
}
