package database

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// DeletedUserEmailError indica que el correo pertenece a un usuario eliminado (borrado lógico).
// El índice único de tenant_users incluye las filas eliminadas, así que el correo sigue ocupado;
// el frontend usa estos datos para ofrecer "restaurar" o "crear uno nuevo".
type DeletedUserEmailError struct {
	UserID    uint
	Name      string
	Email     string
	RoleName  string
	DeletedAt time.Time
}

func (e *DeletedUserEmailError) Error() string {
	return "ya existe un usuario eliminado con ese correo"
}

// FindDeletedUserByEmail devuelve el usuario eliminado con ese correo, o nil si no hay.
func FindDeletedUserByEmail(db *gorm.DB, email string) (*TenantUser, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, nil
	}
	var u TenantUser
	err := db.Unscoped().Where("LOWER(email) = ? AND deleted_at IS NOT NULL", email).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// NewDeletedUserEmailError arma el error con los datos que el frontend muestra al usuario.
func NewDeletedUserEmailError(db *gorm.DB, u *TenantUser) *DeletedUserEmailError {
	e := &DeletedUserEmailError{UserID: u.ID, Name: u.Name, Email: u.Email}
	if u.DeletedAt.Valid {
		e.DeletedAt = u.DeletedAt.Time
	}
	var role TenantRole
	if err := db.Unscoped().Select("name").First(&role, u.RoleID).Error; err == nil {
		e.RoleName = role.Name
	}
	return e
}

// ArchiveDeletedUserEmail libera el correo de un usuario eliminado renombrándolo, sin perder el
// historial (ventas, cajas, etc. siguen apuntando a su id). Solo toca filas ya eliminadas.
func ArchiveDeletedUserEmail(db *gorm.DB, u *TenantUser) error {
	archived := fmt.Sprintf("eliminado-%d-%s", u.ID, u.Email)
	if len(archived) > 255 {
		archived = archived[:255]
	}
	res := db.Unscoped().Model(&TenantUser{}).
		Where("id = ? AND deleted_at IS NOT NULL", u.ID).
		Update("email", archived)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("el usuario eliminado ya no existe")
	}
	return nil
}
