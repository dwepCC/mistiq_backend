package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ErrIdempotentReplay: la clave de idempotencia ya pertenece a una venta creada. Quien lo recibe
// recibe también esa venta (no nil) y debe devolverla como si el cobro acabara de terminar, SIN
// repetir efectos secundarios (encolar a SUNAT, imprimir comanda, etc.): ya ocurrieron en el
// primer intento.
var ErrIdempotentReplay = errors.New("la venta de este intento ya fue registrada")

// MaxIdempotencyKeyLen coincide con tenant_sales.idempotency_key (size:64).
const MaxIdempotencyKeyLen = 64

// NormalizeIdempotencyKey recorta espacios y descarta claves vacías o demasiado largas (se tratan
// como "sin clave": el cobro sigue funcionando, solo sin la protección).
func NormalizeIdempotencyKey(raw string) string {
	k := strings.TrimSpace(raw)
	if k == "" || len(k) > MaxIdempotencyKeyLen {
		return ""
	}
	return k
}

// FindSaleByIdempotencyKey busca la venta que ya usó esta clave para este usuario. Devuelve
// (nil, nil) si no existe o si la clave es vacía.
func FindSaleByIdempotencyKey(db *gorm.DB, userID uint, key string) (*database.TenantSale, error) {
	key = NormalizeIdempotencyKey(key)
	if key == "" || userID == 0 {
		return nil, nil
	}
	var sale database.TenantSale
	err := db.Where("user_id = ? AND idempotency_key = ?", userID, key).First(&sale).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sale, nil
}

// IsDuplicateIdempotencyError detecta la violación de uk_tenant_sales_user_idempotency (MySQL 1062
// o SQLite UNIQUE): dos peticiones con la misma clave llegaron a la vez y la otra ganó el insert.
func IsDuplicateIdempotencyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "uk_tenant_sales_user_idempotency") ||
		(strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "idempotency_key"))
}

// idempotencyKeyPtr devuelve el puntero para la columna nullable (nil si no hay clave).
func idempotencyKeyPtr(key string) *string {
	key = NormalizeIdempotencyKey(key)
	if key == "" {
		return nil
	}
	return &key
}

// ReplayIfIdempotentConflict resuelve la carrera de dos peticiones simultáneas con la misma clave:
// si el insert falló por la clave duplicada, devuelve la venta ganadora y ErrIdempotentReplay.
// En cualquier otro caso devuelve (nil, err) intacto para que el llamador lo propague.
func ReplayIfIdempotentConflict(db *gorm.DB, userID uint, key string, err error) (*database.TenantSale, error) {
	if !IsDuplicateIdempotencyError(err) {
		return nil, err
	}
	existing, findErr := FindSaleByIdempotencyKey(db, userID, key)
	if findErr != nil || existing == nil {
		return nil, err
	}
	return existing, ErrIdempotentReplay
}
