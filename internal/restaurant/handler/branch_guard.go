package handler

import (
	"errors"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Aislamiento por sucursal en Tukichef (Fase 3A). Pisos, mesas y sesiones tienen branch_id; los pedidos
// (TenantTableOrder) y las comandas heredan la sucursal de su sesión. Un registro de otra sucursal responde 404
// ("no encontrado", como cotizaciones); uno legible pero fuera de la sucursal activa (quien ve todo), 403
// BRANCH_FORBIDDEN al intentar modificarlo.

func guardNotFound(c fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
}

func guardWriteForbidden(c fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error": "Solo puede modificar registros de la sucursal activa; cambie de sucursal para operar en otra",
		"code":  branch.CodeBranchForbidden,
	})
}

// guardBranch aplica lectura/escritura sobre el branch_id de un registro. ok=false => ya respondió.
func guardBranch(c fiber.Ctx, recordBranch uint, write bool, notFoundMsg string) (bool, error) {
	if !branch.RecordReadable(c, recordBranch) {
		return false, guardNotFound(c, notFoundMsg)
	}
	if write && !branch.RecordWritable(c, recordBranch) {
		return false, guardWriteForbidden(c)
	}
	return true, nil
}

func guardLookupError(c fiber.Ctx, err error, notFoundMsg string) (bool, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, guardNotFound(c, notFoundMsg)
	}
	return false, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
}

func guardSession(c fiber.Ctx, sessionID uint, write bool) (bool, error) {
	var s database.TenantTableSession
	if err := db(c).Select("id", "branch_id").First(&s, sessionID).Error; err != nil {
		return guardLookupError(c, err, "sesión no encontrada")
	}
	return guardBranch(c, s.BranchID, write, "sesión no encontrada")
}

func guardFloor(c fiber.Ctx, floorID uint, write bool) (bool, error) {
	var f database.TenantRestaurantFloor
	if err := db(c).Select("id", "branch_id").First(&f, floorID).Error; err != nil {
		return guardLookupError(c, err, "piso no encontrado")
	}
	return guardBranch(c, f.BranchID, write, "piso no encontrado")
}

func guardTable(c fiber.Ctx, tableID uint, write bool) (bool, error) {
	var t database.TenantRestaurantTable
	if err := db(c).Select("id", "branch_id").First(&t, tableID).Error; err != nil {
		return guardLookupError(c, err, "mesa no encontrada")
	}
	return guardBranch(c, t.BranchID, write, "mesa no encontrada")
}

// guardComanda: la comanda hereda la sucursal de su sesión.
func guardComanda(c fiber.Ctx, comandaID uint, write bool) (bool, error) {
	var cm database.TenantComanda
	if err := db(c).Select("id", "session_id").First(&cm, comandaID).Error; err != nil {
		return guardLookupError(c, err, "comanda no encontrada")
	}
	return guardSession(c, cm.SessionID, write)
}

// guardTableOrder: el pedido (ronda) hereda la sucursal de su sesión.
func guardTableOrder(c fiber.Ctx, orderID uint, write bool) (bool, error) {
	var o database.TenantTableOrder
	if err := db(c).Select("id", "session_id").First(&o, orderID).Error; err != nil {
		return guardLookupError(c, err, "pedido no encontrado")
	}
	return guardSession(c, o.SessionID, write)
}
