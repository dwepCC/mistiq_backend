package handler

import (
	"errors"

	"tukifac/internal/products/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/productbranch"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// errBranchForbidden: se intentó asignar/tocar una sucursal fuera de las que el usuario puede ver.
var errBranchForbidden = errors.New("no puede asignar productos a una sucursal a la que no tiene acceso")

// requireAccessibleBranchIDs: cada sucursal pedida debe ser accesible para el usuario (quien ve todo, cualquiera;
// ProductBranchIDs ya validó que existan).
func requireAccessibleBranchIDs(c fiber.Ctx, ids []uint) error {
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if !branch.CanAccessBranch(c, id) {
			return errBranchForbidden
		}
	}
	return nil
}

// scopedBranchSet calcula la lista final de sucursales de un producto cuando el usuario pide `requested` ("set").
//   - Quien ve todo: exactamente lo pedido.
//   - El resto: lo pedido (debe ser accesible) MÁS las sucursales que ya tenía el producto y que él no puede ver
//     (no puede quitarle la venta a una sucursal ajena). Pedir [] ("todas") solo es posible si el producto no
//     está asignado a sucursales ajenas.
func scopedBranchSet(c fiber.Ctx, tdb *gorm.DB, productID uint, requested []uint) ([]uint, error) {
	if branch.IsBranchAdmin(c) {
		return requested, nil
	}
	if err := requireAccessibleBranchIDs(c, requested); err != nil {
		return nil, err
	}
	var foreign []uint
	if productID != 0 {
		cur, err := productbranch.BranchIDs(tdb, []uint{productID})
		if err != nil {
			return nil, err
		}
		for _, id := range cur[productID] {
			if !branch.CanAccessBranch(c, id) {
				foreign = append(foreign, id)
			}
		}
	}
	if len(requested) == 0 {
		if len(foreign) > 0 {
			return nil, errors.New("el producto está asignado a sucursales que no administra; no puede dejarlo disponible en todas")
		}
		return requested, nil
	}
	return append(append([]uint{}, requested...), foreign...), nil
}

// branchPriceAllowed: los precios por sucursal solo se leen/escriben para una sucursal accesible; escribir además
// exige que sea la activa (el modo "todas las sucursales" y las sucursales ajenas son de solo lectura).
func branchPriceReadAllowed(c fiber.Ctx, branchID uint) bool {
	return branch.CanAccessBranch(c, branchID)
}

func branchPriceWriteAllowed(c fiber.Ctx, branchID uint) bool {
	return branch.CanAccessBranch(c, branchID) && branch.RecordWritable(c, branchID) && branch.ActiveBranchID(c) != 0
}

func writeForbidden(c fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error": "Solo puede modificar datos de la sucursal activa; cambie de sucursal para operar en otra",
		"code":  branch.CodeBranchForbidden,
	})
}

// guardProductByID: el producto debe existir y ser visible para el usuario (404 si no). Devuelve ok=false si ya respondió.
func (h *ProductHandler) guardProductByID(c fiber.Ctx, productID uint) (error, bool) {
	p, err := service.NewProductService(db(c)).GetByID(productID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"}), false
	}
	if resp, denied := h.productDenied(c, p); denied {
		return resp, false
	}
	return nil, true
}

// guardBranchPrice: la sucursal :branchId debe ser accesible (404 si no); para escribir, además la activa (403).
func guardBranchPrice(c fiber.Ctx, branchID uint, write bool) (error, bool) {
	if !branchPriceReadAllowed(c, branchID) {
		return c.Status(404).JSON(fiber.Map{"error": "sucursal no encontrada"}), false
	}
	if write && !branchPriceWriteAllowed(c, branchID) {
		return writeForbidden(c), false
	}
	return nil, true
}
