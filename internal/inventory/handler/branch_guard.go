package handler

import (
	"errors"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Aislamiento por sucursal en inventario (Fase 3A). Un registro de otra sucursal responde 404 ("no encontrado",
// como cotizaciones) para no revelar qué IDs existen; uno legible pero fuera de la sucursal activa (admin que
// ve todo) responde 403 BRANCH_FORBIDDEN al intentar modificarlo: para operar en otra sucursal hay que cambiar a ella.

func notFoundJSON(c fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
}

func writeForbiddenJSON(c fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error": "Solo puede modificar registros de la sucursal activa; cambie de sucursal para operar en otra",
		"code":  branch.CodeBranchForbidden,
	})
}

// guardRecordRead devuelve (true) si puede continuar; si no, ya respondió 404.
func guardRecordRead(c fiber.Ctx, recordBranchID uint, msg string) (bool, error) {
	if branch.RecordReadable(c, recordBranchID) {
		return true, nil
	}
	return false, notFoundJSON(c, msg)
}

// guardRecordWrite exige legible (404) y luego de la sucursal activa (403).
func guardRecordWrite(c fiber.Ctx, recordBranchID uint, msg string) (bool, error) {
	if !branch.RecordReadable(c, recordBranchID) {
		return false, notFoundJSON(c, msg)
	}
	if !branch.RecordWritable(c, recordBranchID) {
		return false, writeForbiddenJSON(c)
	}
	return true, nil
}

// documentBranch devuelve el branch_id de un documento de inventario.
func documentBranch(tdb *gorm.DB, id uint) (uint, error) {
	var doc database.TenantInventoryDocument
	if err := tdb.Select("id", "branch_id").First(&doc, id).Error; err != nil {
		return 0, err
	}
	return doc.BranchID, nil
}

func loadTransfer(tdb *gorm.DB, id uint) (database.TenantTransfer, error) {
	var tr database.TenantTransfer
	err := tdb.First(&tr, id).Error
	return tr, err
}

// canOriginateTransferFrom: el origen debe ser la sucursal ACTIVA, incluso para quien ve todas (debe cambiar de sucursal).
func canOriginateTransferFrom(c fiber.Ctx, from uint) bool {
	if branch.ActiveBranchID(c) == 0 || !branch.CanAccessBranch(c, from) {
		return false
	}
	return branch.RecordWritable(c, from)
}

// destinationBranchValid: el destino debe existir y estar activo.
func destinationBranchValid(tdb *gorm.DB, id uint) error {
	var b database.TenantBranch
	if err := tdb.Where("active = ?", true).First(&b, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("la sucursal destino no existe o está inactiva")
		}
		return err
	}
	return nil
}

// visibleBranchIDs sucursales cuyo stock/transferencias puede ver el usuario: nil = todas (ve todo).
func visibleBranchIDs(c fiber.Ctx) []uint {
	if branch.IsBranchAdmin(c) {
		return nil
	}
	seen := map[uint]bool{}
	out := []uint{}
	add := func(id uint) {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	add(branch.ActiveBranchID(c))
	for _, id := range branch.AllowedBranchIDs(c) {
		add(id)
	}
	return out
}

// guardDocument valida el acceso al documento de inventario id. write=true exige sucursal activa.
// Devuelve ok=false cuando ya respondió (404/403).
func guardDocument(c fiber.Ctx, id uint, write bool) (bool, error) {
	bid, err := documentBranch(db(c), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, notFoundJSON(c, "documento no encontrado")
		}
		return false, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if write {
		return guardRecordWrite(c, bid, "documento no encontrado")
	}
	return guardRecordRead(c, bid, "documento no encontrado")
}

// guardTransfer valida el acceso a la transferencia id. role: "confirm" (la sucursal activa debe ser el DESTINO),
// "cancel" (la activa debe ser el ORIGEN). Una transferencia en la que el usuario no participa responde 404.
func guardTransfer(c fiber.Ctx, id uint, role string) (bool, error) {
	tr, err := loadTransfer(db(c), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, notFoundJSON(c, "transferencia no encontrada")
		}
		return false, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if !branch.CanAccessBranch(c, tr.FromBranchID) && !branch.CanAccessBranch(c, tr.ToBranchID) {
		return false, notFoundJSON(c, "transferencia no encontrada")
	}
	active := branch.ActiveBranchID(c)
	switch role {
	case "confirm":
		if active == 0 || active != tr.ToBranchID {
			return false, c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "La transferencia solo la confirma la sucursal de destino",
				"code":  branch.CodeBranchForbidden,
			})
		}
	case "cancel":
		if active == 0 || active != tr.FromBranchID {
			return false, c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "La transferencia solo la cancela la sucursal de origen",
				"code":  branch.CodeBranchForbidden,
			})
		}
	}
	return true, nil
}

// guardTransferLogReverse: anular una transferencia legacy (por ID de línea) exige acceso a origen Y destino.
func guardTransferLogReverse(c fiber.Ctx, logID uint) (bool, error) {
	var row database.TenantTransferLog
	if err := db(c).Select("id", "from_branch_id", "to_branch_id").First(&row, logID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, notFoundJSON(c, "transferencia no encontrada")
		}
		return false, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if !branch.CanAccessBranch(c, row.FromBranchID) || !branch.CanAccessBranch(c, row.ToBranchID) {
		return false, notFoundJSON(c, "transferencia no encontrada")
	}
	if branch.ActiveBranchID(c) == 0 {
		return false, writeForbiddenJSON(c)
	}
	return true, nil
}

func containsUint(list []uint, v uint) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
