package prepayment

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// VoucherHandler listado de anticipos abiertos para deducción.
type VoucherHandler struct{}

func NewVoucherHandler() *VoucherHandler {
	return &VoucherHandler{}
}

func tenantDB(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

// ListOpenVouchersAPI GET /prepayment/vouchers?contact_id=&affectation_group=&tax_rate=
// contact_id es opcional: si se omite, lista todos los anticipos abiertos (como PHP legacy).
func (h *VoucherHandler) ListOpenVouchersAPI(c fiber.Ctx) error {
	db := tenantDB(c)
	if db == nil {
		return c.Status(500).JSON(fiber.Map{"error": "tenant no disponible"})
	}
	contactID, _ := strconv.ParseUint(c.Query("contact_id"), 10, 64)
	group := c.Query("affectation_group")
	if strings.TrimSpace(group) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "affectation_group es requerido"})
	}
	taxRate, _ := strconv.ParseFloat(c.Query("tax_rate", "18"), 64)
	rows, err := NewService(db).ListOpenVouchers(uint(contactID), group, taxRate)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(rows)
}
