package service

import (
	"errors"
	"strings"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type UserService struct {
	db *gorm.DB
}

func NewUserService(db *gorm.DB) *UserService {
	return &UserService{db: db}
}

type UserListParams struct {
	Query  string
	RoleID uint
	Active *bool
}

func (s *UserService) List(params UserListParams) ([]database.TenantUser, error) {
	var users []database.TenantUser
	q := s.db.Model(&database.TenantUser{})
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
}

func (s *UserService) Create(input CreateUserInput) (*database.TenantUser, error) {
	if input.Name == "" || input.Email == "" || input.Password == "" {
		return nil, errors.New("nombre, email y contraseña son requeridos")
	}
	if input.RoleID == 0 {
		return nil, errors.New("rol es requerido")
	}

	var existing database.TenantUser
	if err := s.db.Where("email = ?", input.Email).First(&existing).Error; err == nil {
		return nil, errors.New("el email ya está registrado")
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

	if input.RoleID > 0 && input.RoleID != user.RoleID {
		locked, err := s.TenantRoleEditLocked(id, user.RoleID)
		if err != nil {
			return err
		}
		if locked {
			return errors.New("no se puede cambiar el rol del usuario principal del sistema")
		}
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

	if input.Password != "" {
		if err := user.SetPassword(input.Password); err != nil {
			return err
		}
		updates["password"] = user.Password
	}

	return s.db.Model(&database.TenantUser{}).Where("id = ?", id).Updates(updates).Error
}

func (s *UserService) Delete(id uint) error {
	return s.db.Delete(&database.TenantUser{}, id).Error
}

func (s *UserService) ToggleActive(id uint) error {
	var user database.TenantUser
	if err := s.db.First(&user, id).Error; err != nil {
		return err
	}
	return s.db.Model(&user).Update("active", !user.Active).Error
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

	var existing database.TenantUser
	if err := s.db.Where("email = ? AND id <> ?", email, id).First(&existing).Error; err == nil {
		return errors.New("el email ya está registrado")
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
	return s.db.Model(&database.TenantUser{}).Where("id = ?", id).Update("password", user.Password).Error
}
