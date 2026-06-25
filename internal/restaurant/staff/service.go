package staff

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/restaurantperm"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ErrPINDuplicate se devuelve al asignar un PIN ya usado por otro staff activo.
var ErrPINDuplicate = errors.New("este PIN ya está asignado a otro usuario del restaurante")

// ErrTenantOwnerRoleLocked el usuario maestro del tenant no puede cambiar su rol de administrador.
var ErrTenantOwnerRoleLocked = errors.New("no se puede cambiar el rol del usuario principal del sistema")

type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) GetPermCacheVersion() (uint, error) {
	var cfg database.TenantRestaurantSetting
	if err := s.db.First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return cfg.PermCacheVersion, nil
}

func (s *Service) BumpPermCacheVersion() error {
	var cfg database.TenantRestaurantSetting
	if err := s.db.First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			cfg.PermCacheVersion = 1
			cfg.StaffV2Enabled = true
			return s.db.Create(&cfg).Error
		}
		return err
	}
	return s.db.Model(&cfg).UpdateColumn("perm_cache_version", gorm.Expr("perm_cache_version + 1")).Error
}

// ResolvePermissionKeys obtiene permisos efectivos (cache → staff profile).
// tenantSlug aísla la cache Redis/memoria entre empresas (obligatorio en producción).
func (s *Service) ResolvePermissionKeys(tenantSlug string, tenantID, userID, permVer uint) ([]string, error) {
	if keys, ok := restaurantperm.GetCached(tenantSlug, tenantID, userID, permVer); ok {
		return keys, nil
	}
	keys, err := s.computePermissionKeys(userID)
	if err != nil {
		return nil, err
	}
	restaurantperm.SetCached(tenantSlug, tenantID, userID, permVer, keys)
	return keys, nil
}

func (s *Service) computePermissionKeys(userID uint) ([]string, error) {
	var st database.TenantRestaurantStaff
	err := s.db.Where("user_id = ? AND is_active = ?", userID, true).First(&st).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	f := staffFlags(&st)
	flags := restaurantperm.StaffFlags{
		CanCharge: f.CanCharge, CanDiscount: f.CanDiscount,
		CanOpenTable: f.CanOpenTable, KitchenAccess: f.KitchenAccess,
		DeliveryAccess: f.DeliveryAccess,
	}
	return restaurantperm.EmployeeTypeToKeys(normalizeEmployeeType(st.EmployeeType), flags), nil
}

func (s *Service) HasPermission(tenantSlug string, tenantID, userID, permVer uint, perm string) (bool, error) {
	keys, err := s.ResolvePermissionKeys(tenantSlug, tenantID, userID, permVer)
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if k == perm {
			return true, nil
		}
	}
	return false, nil
}

// AuthenticatePIN valida PIN y estación.
func (s *Service) AuthenticatePIN(pin, station string) (userID, staffID uint, employeeType string, err error) {
	pin = strings.TrimSpace(pin)
	if len(pin) < 4 || len(pin) > 6 {
		return 0, 0, "", errors.New("PIN inválido")
	}
	allowed := restaurantperm.StationAllowedTypes(station)
	if len(allowed) == 0 {
		return 0, 0, "", errors.New("estación no válida")
	}

	var candidates []database.TenantRestaurantStaff
	if err := s.db.Where("is_active = ? AND pin_hash != ''", true).Find(&candidates).Error; err != nil {
		return 0, 0, "", err
	}
	var matched *database.TenantRestaurantStaff
	for i := range candidates {
		st := &candidates[i]
		if !matchesStation(st, allowed) {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(st.PinHash), []byte(pin)) != nil {
			continue
		}
		if matched != nil {
			return 0, 0, "", errors.New("PIN duplicado en el sistema; contacte al administrador")
		}
		matched = st
	}
	if matched == nil {
		return 0, 0, "", errors.New("PIN incorrecto o sin acceso a esta estación")
	}
	return matched.UserID, matched.ID, normalizeEmployeeType(matched.EmployeeType), nil
}

func (s *Service) RestaurantRoleEditLocked(userID uint, currentEmployeeType string) (bool, error) {
	isOwner, err := database.IsTenantOwnerUser(s.db, userID)
	if err != nil || !isOwner {
		return false, err
	}
	return normalizeEmployeeType(currentEmployeeType) == "admin", nil
}

func (s *Service) guardTenantOwnerRestaurantRoleChange(userID uint, newEmployeeType string) error {
	var current database.TenantRestaurantStaff
	err := s.db.Where("user_id = ?", userID).First(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	locked, err := s.RestaurantRoleEditLocked(userID, current.EmployeeType)
	if err != nil || !locked {
		return err
	}
	if normalizeEmployeeType(newEmployeeType) != "admin" {
		return ErrTenantOwnerRoleLocked
	}
	return nil
}

func (s *Service) UpsertStaffForUser(userID uint, employeeType string, pin string, flags UpsertFlags) error {
	employeeType = normalizeEmployeeType(employeeType)
	if err := s.guardTenantOwnerRestaurantRoleChange(userID, employeeType); err != nil {
		return err
	}
	if employeeType == "" {
		if err := s.db.Where("user_id = ?", userID).Delete(&database.TenantRestaurantStaff{}).Error; err != nil {
			return err
		}
		return s.BumpPermCacheVersion()
	}
	var st database.TenantRestaurantStaff
	err := s.db.Where("user_id = ?", userID).First(&st).Error
	isNew := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !isNew {
		return err
	}
	st.UserID = userID
	st.EmployeeType = employeeType
	st.IsActive = true
	if flags.DisplayName != "" {
		st.DisplayName = flags.DisplayName
	}
	if flags.StaffCode != "" {
		st.StaffCode = flags.StaffCode
	}
	if flags.CanCharge || employeeType == "cashier" || employeeType == "admin" {
		st.CanCharge = flags.CanCharge || employeeType == "cashier" || employeeType == "admin"
	} else {
		st.CanCharge = flags.CanCharge
	}
	st.CanDiscount = flags.CanDiscount
	st.CanOpenTable = flags.CanOpenTable || employeeType == "waiter" || employeeType == "admin"
	st.KitchenAccess = flags.KitchenAccess || employeeType == "cook" || employeeType == "admin"
	st.DeliveryAccess = flags.DeliveryAccess || employeeType == "driver"
	if flags.ClearPin {
		st.PinHash = ""
	} else if pin != "" {
		pin = strings.TrimSpace(pin)
		if err := ValidatePINFormat(pin); err != nil {
			return err
		}
		if err := s.validatePINUnique(pin, userID); err != nil {
			return err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		st.PinHash = string(hash)
	}
	if isNew {
		if err := s.db.Create(&st).Error; err != nil {
			return err
		}
	} else {
		if err := s.db.Save(&st).Error; err != nil {
			return err
		}
	}
	return s.BumpPermCacheVersion()
}

type UpsertFlags struct {
	DisplayName    string
	StaffCode      string
	CanCharge      bool
	CanDiscount    bool
	CanOpenTable   bool
	KitchenAccess  bool
	DeliveryAccess bool
	ClearPin       bool
}

// CreateStaffUserInput alta de usuario operativo Tukichef (rol tenant mínimo + perfil staff).
type CreateStaffUserInput struct {
	Name         string
	Email        string
	Phone        string
	EmployeeType string
	Pin          string
	StaffCode    string
	DisplayName  string
	BranchIDs    []uint
}

func (s *Service) CreateStaffUser(in CreateStaffUserInput) (*StaffManagementItem, error) {
	name := strings.TrimSpace(in.Name)
	email := strings.TrimSpace(strings.ToLower(in.Email))
	employeeType := normalizeEmployeeType(in.EmployeeType)
	if name == "" {
		return nil, errors.New("nombre requerido")
	}
	if email == "" {
		return nil, errors.New("email requerido")
	}
	if employeeType == "" {
		return nil, errors.New("rol de restaurante requerido")
	}
	if err := ValidatePINFormat(in.Pin); err != nil {
		return nil, err
	}
	if err := s.validatePINUnique(in.Pin, 0); err != nil {
		return nil, err
	}
	if err := s.ensureEmailAvailable(email); err != nil {
		return nil, err
	}

	roleID, err := s.ensureOperativeRestaurantRoleID()
	if err != nil {
		return nil, err
	}
	branchIDs := in.BranchIDs
	if len(branchIDs) == 0 {
		mainID, err := s.mainBranchID()
		if err != nil {
			return nil, err
		}
		branchIDs = []uint{mainID}
	}
	if err := branch.ValidateBranchIDsExist(s.db, branchIDs); err != nil {
		return nil, err
	}
	homeBranchID := branchIDs[0]

	var createdUserID uint
	err = s.db.Transaction(func(tx *gorm.DB) error {
		txSvc := &Service{db: tx}
		user := &database.TenantUser{
			RoleID:          roleID,
			HomeBranchID:    &homeBranchID,
			BranchID:        &homeBranchID,
			Name:            name,
			Email:           email,
			Phone:           strings.TrimSpace(in.Phone),
			Active:          true,
			CanSwitchBranch: len(branchIDs) > 1,
		}
		if err := user.SetPassword(randomOperativePassword()); err != nil {
			return err
		}
		if err := tx.Create(user).Error; err != nil {
			return fmt.Errorf("creando usuario: %w", err)
		}
		createdUserID = user.ID
		if err := branch.SetUserAssignedBranches(tx, user.ID, branchIDs, false); err != nil {
			return err
		}
		flags := UpsertFlags{
			DisplayName: strings.TrimSpace(in.DisplayName),
			StaffCode:   strings.TrimSpace(in.StaffCode),
		}
		if err := txSvc.UpsertStaffForUser(user.ID, employeeType, in.Pin, flags); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	items, err := s.ListStaffManagement()
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.UserID == createdUserID {
			return &item, nil
		}
	}
	return &StaffManagementItem{
		UserID: createdUserID, Name: name, Email: email, Active: true,
		EmployeeType: employeeType, HasPin: true, StaffActive: true, ProfileComplete: true,
	}, nil
}

const operativeRestaurantRoleName = "Operativo Restaurante"

func (s *Service) ensureOperativeRestaurantRoleID() (uint, error) {
	var role database.TenantRole
	err := s.db.Where("name = ?", operativeRestaurantRoleName).First(&role).Error
	if err == nil {
		return role.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	role = database.TenantRole{
		Name:        operativeRestaurantRoleName,
		Description: "Acceso operativo Tukichef (PIN). Sin permisos del panel ERP.",
		IsSystem:    true,
	}
	if err := s.db.Create(&role).Error; err != nil {
		return 0, fmt.Errorf("rol operativo: %w", err)
	}
	return role.ID, nil
}

func (s *Service) mainBranchID() (uint, error) {
	var branch database.TenantBranch
	if err := s.db.Where("is_main = ? AND active = ?", true, true).First(&branch).Error; err == nil {
		return branch.ID, nil
	}
	if err := s.db.Where("active = ?", true).Order("id ASC").First(&branch).Error; err != nil {
		return 0, errors.New("no hay sucursal activa configurada")
	}
	return branch.ID, nil
}

func randomOperativePassword() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("op-%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func normalizeEmployeeType(et string) string {
	et = strings.TrimSpace(strings.ToLower(et))
	switch et {
	case "vendedor", "cajero":
		return "cashier"
	case "mozo":
		return "waiter"
	case "cocinero":
		return "cook"
	default:
		return et
	}
}

// StaffListItem fila admin (sin exponer pin_hash).
type StaffListItem struct {
	ID           uint   `json:"id"`
	UserID       uint   `json:"user_id"`
	EmployeeType string `json:"employee_type"`
	DisplayName  string `json:"display_name"`
	StaffCode    string `json:"staff_code"`
	IsActive     bool   `json:"is_active"`
	HasPin       bool   `json:"has_pin"`
}

// StaffManagementItem usuario tenant con perfil restaurante opcional (pantalla Ajustes Tukichef).
type StaffManagementItem struct {
	UserID       uint     `json:"user_id"`
	Name         string   `json:"name"`
	Email        string   `json:"email"`
	Active       bool     `json:"active"`
	StaffID      uint     `json:"staff_id,omitempty"`
	EmployeeType string   `json:"employee_type"`
	DisplayName  string   `json:"display_name"`
	StaffCode    string   `json:"staff_code"`
	HasPin            bool     `json:"has_pin"`
	StaffActive       bool     `json:"staff_active"`
	ProfileComplete   bool     `json:"profile_complete"`
	BranchIDs         []uint   `json:"branch_ids,omitempty"`
	BranchNames       []string `json:"branch_names,omitempty"`
	RoleEditLocked    bool     `json:"role_edit_locked"`
}

func (s *Service) ListStaffManagement() ([]StaffManagementItem, error) {
	var users []database.TenantUser
	if err := s.db.Order("name, email").Find(&users).Error; err != nil {
		return nil, err
	}
	var staffRows []database.TenantRestaurantStaff
	if err := s.db.Find(&staffRows).Error; err != nil {
		return nil, err
	}
	byUser := make(map[uint]*database.TenantRestaurantStaff, len(staffRows))
	for i := range staffRows {
		byUser[staffRows[i].UserID] = &staffRows[i]
	}
	out := make([]StaffManagementItem, 0, len(users))
	for _, u := range users {
		item := StaffManagementItem{
			UserID: u.ID,
			Name:   u.Name,
			Email:  u.Email,
			Active: u.Active,
		}
		if st, ok := byUser[u.ID]; ok {
			item.StaffID = st.ID
			item.EmployeeType = st.EmployeeType
			item.DisplayName = st.DisplayName
			item.StaffCode = st.StaffCode
			item.HasPin = strings.TrimSpace(st.PinHash) != ""
			item.StaffActive = st.IsActive
			item.ProfileComplete = st.IsActive && strings.TrimSpace(st.EmployeeType) != ""
			if locked, err := s.RestaurantRoleEditLocked(u.ID, st.EmployeeType); err == nil {
				item.RoleEditLocked = locked
			}
		}
		ids, _ := branch.ResolveDisplayBranchIDs(s.db, u.ID, u.HomeBranchID, u.BranchID)
		item.BranchIDs = ids
		for _, bid := range ids {
			if b, err := branch.GetBranchBrief(s.db, bid); err == nil {
				item.BranchNames = append(item.BranchNames, b.Name)
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// AssignUserBranches actualiza sucursales asignadas a un usuario operativo.
func (s *Service) AssignUserBranches(userID uint, branchIDs []uint) error {
	if userID == 0 {
		return errors.New("usuario inválido")
	}
	return branch.SetUserAssignedBranches(s.db, userID, branchIDs, true)
}

func (s *Service) ListStaff() ([]StaffListItem, error) {
	var list []database.TenantRestaurantStaff
	if err := s.db.Order("display_name, employee_type").Find(&list).Error; err != nil {
		return nil, err
	}
	out := make([]StaffListItem, 0, len(list))
	for _, st := range list {
		out = append(out, StaffListItem{
			ID:           st.ID,
			UserID:       st.UserID,
			EmployeeType: st.EmployeeType,
			DisplayName:  st.DisplayName,
			StaffCode:    st.StaffCode,
			IsActive:     st.IsActive,
			HasPin:       strings.TrimSpace(st.PinHash) != "",
		})
	}
	return out, nil
}

func (s *Service) GetStaffByUserID(userID uint) (*database.TenantRestaurantStaff, error) {
	var st database.TenantRestaurantStaff
	if err := s.db.Where("user_id = ? AND is_active = ?", userID, true).First(&st).Error; err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Service) GetStaffByID(id uint) (*database.TenantRestaurantStaff, error) {
	var st database.TenantRestaurantStaff
	if err := s.db.First(&st, id).Error; err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Service) StaffDisplayName(st *database.TenantRestaurantStaff) string {
	if st == nil {
		return ""
	}
	if st.DisplayName != "" {
		return st.DisplayName
	}
	var u database.TenantUser
	if s.db.Select("name").First(&u, st.UserID).Error == nil {
		return u.Name
	}
	return ""
}

// ensureEmailAvailable rechaza emails en uso; elimina restos de altas fallidas (soft delete).
func (s *Service) ensureEmailAvailable(email string) error {
	var existing database.TenantUser
	err := s.db.Unscoped().Where("email = ?", email).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing.DeletedAt.Valid {
		s.purgeStaffUser(existing.ID)
		return nil
	}
	var st database.TenantRestaurantStaff
	if err := s.db.Where("user_id = ?", existing.ID).First(&st).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		s.purgeStaffUser(existing.ID)
		return nil
	}
	return errors.New("el email ya está registrado")
}

// purgeStaffUser elimina por completo un usuario y su perfil (alta fallida o limpieza).
func (s *Service) purgeStaffUser(userID uint) {
	if userID == 0 {
		return
	}
	_ = s.db.Where("user_id = ?", userID).Delete(&database.TenantRestaurantStaff{}).Error
	if branch.UserBranchesReady(s.db) {
		_ = s.db.Where("user_id = ?", userID).Delete(&database.TenantUserBranch{}).Error
	}
	_ = s.db.Unscoped().Delete(&database.TenantUser{}, userID).Error
}

// validatePINUnique impide dos staff activos con el mismo PIN (login Tukichef).
func (s *Service) validatePINUnique(pin string, excludeUserID uint) error {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return nil
	}
	var others []database.TenantRestaurantStaff
	q := s.db.Where("is_active = ? AND pin_hash != ''", true)
	if excludeUserID > 0 {
		q = q.Where("user_id != ?", excludeUserID)
	}
	if err := q.Find(&others).Error; err != nil {
		return err
	}
	for i := range others {
		if bcrypt.CompareHashAndPassword([]byte(others[i].PinHash), []byte(pin)) == nil {
			return ErrPINDuplicate
		}
	}
	return nil
}

func ValidatePINFormat(pin string) error {
	pin = strings.TrimSpace(pin)
	if len(pin) < 4 || len(pin) > 6 {
		return fmt.Errorf("el PIN debe tener entre 4 y 6 dígitos")
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return fmt.Errorf("el PIN solo puede contener dígitos")
		}
	}
	return nil
}
