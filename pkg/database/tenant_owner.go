package database

import "gorm.io/gorm"

// TenantOwnerUserID devuelve el ID del primer usuario creado en el tenant (usuario maestro al registrar la empresa).
func TenantOwnerUserID(db *gorm.DB) (uint, bool, error) {
	var minID *uint
	err := db.Model(&TenantUser{}).Select("MIN(id)").Scan(&minID).Error
	if err != nil || minID == nil || *minID == 0 {
		return 0, false, err
	}
	return *minID, true, nil
}

// IsTenantOwnerUser indica si el usuario es el primero creado al provisionar el tenant.
func IsTenantOwnerUser(db *gorm.DB, userID uint) (bool, error) {
	if userID == 0 {
		return false, nil
	}
	ownerID, ok, err := TenantOwnerUserID(db)
	if err != nil || !ok {
		return false, err
	}
	return userID == ownerID, nil
}
