package handler

import (
	"strconv"

	"tukifac/internal/ecommerce/service"
	"tukifac/pkg/middleware"
	"tukifac/pkg/tenantctx"

	"github.com/gofiber/fiber/v3"
)

// customerAccountJSON nunca incluye PasswordHash — respuesta pública explícita en vez de confiar
// en que TenantEcommerceCustomerAccount.PasswordHash quede con json:"-" (defensa en profundidad:
// si alguna vez se le agrega un campo sensible al modelo, este handler no lo expone solo).
type customerAccountJSON struct {
	ID    uint    `json:"id"`
	Name  string  `json:"name"`
	Phone string  `json:"phone"`
	Email *string `json:"email"`
}

// currentCustomerID lee el customer_account_id inyectado por
// middleware.EcommerceCustomerAuthRequired/Optional — NUNCA se acepta uno enviado por el cliente
// en el body/query (Contrato ecommerce v2 Fase 4, "ownership real, no confiar en el frontend").
func currentCustomerID(c fiber.Ctx) (uint, bool) {
	id, ok := c.Locals("ecommerce_customer_id").(uint)
	return id, ok && id > 0
}

func (h *EcommerceHandler) RegisterCustomerAPI(c fiber.Ctx) error {
	var body struct {
		Name     string `json:"name"`
		Phone    string `json:"phone"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	account, err := service.NewEcommerceService(db(c)).RegisterCustomer(service.RegisterCustomerInput{
		Name: body.Name, Phone: body.Phone, Email: body.Email, Password: body.Password,
	})
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	token, err := middleware.BuildEcommerceCustomerToken(account.ID, account.Phone, tenantctx.Slug(c))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "no se pudo crear la sesión"})
	}
	return c.Status(201).JSON(fiber.Map{
		"token":    token,
		"customer": customerAccountJSON{ID: account.ID, Name: account.Name, Phone: account.Phone, Email: account.Email},
	})
}

func (h *EcommerceHandler) LoginCustomerAPI(c fiber.Ctx) error {
	var body struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	account, err := service.NewEcommerceService(db(c)).LoginCustomer(service.LoginCustomerInput{
		Phone: body.Phone, Password: body.Password,
	})
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"error": err.Error()})
	}
	token, err := middleware.BuildEcommerceCustomerToken(account.ID, account.Phone, tenantctx.Slug(c))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "no se pudo crear la sesión"})
	}
	return c.JSON(fiber.Map{
		"token":    token,
		"customer": customerAccountJSON{ID: account.ID, Name: account.Name, Phone: account.Phone, Email: account.Email},
	})
}

func (h *EcommerceHandler) CustomerMeAPI(c fiber.Ctx) error {
	customerID, ok := currentCustomerID(c)
	if !ok {
		return c.Status(401).JSON(fiber.Map{"error": "sesión inválida"})
	}
	account, err := service.NewEcommerceService(db(c)).GetCustomerAccount(customerID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(customerAccountJSON{ID: account.ID, Name: account.Name, Phone: account.Phone, Email: account.Email})
}

// ── Direcciones ──────────────────────────────────────────────────────

func addressInputFromBody(c fiber.Ctx) (service.CustomerAddressInput, error) {
	var body struct {
		Label       string `json:"label"`
		AddressLine string `json:"address_line"`
		Reference   string `json:"reference"`
		Ubigeo      string `json:"ubigeo"`
		Phone       string `json:"phone"`
		IsDefault   bool   `json:"is_default"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return service.CustomerAddressInput{}, err
	}
	return service.CustomerAddressInput{
		Label: body.Label, AddressLine: body.AddressLine, Reference: body.Reference,
		Ubigeo: body.Ubigeo, Phone: body.Phone, IsDefault: body.IsDefault,
	}, nil
}

func (h *EcommerceHandler) ListCustomerAddressesAPI(c fiber.Ctx) error {
	customerID, ok := currentCustomerID(c)
	if !ok {
		return c.Status(401).JSON(fiber.Map{"error": "sesión inválida"})
	}
	rows, err := service.NewEcommerceService(db(c)).ListCustomerAddresses(customerID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *EcommerceHandler) CreateCustomerAddressAPI(c fiber.Ctx) error {
	customerID, ok := currentCustomerID(c)
	if !ok {
		return c.Status(401).JSON(fiber.Map{"error": "sesión inválida"})
	}
	input, err := addressInputFromBody(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	addr, err := service.NewEcommerceService(db(c)).CreateCustomerAddress(customerID, input)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": addr})
}

func (h *EcommerceHandler) UpdateCustomerAddressAPI(c fiber.Ctx) error {
	customerID, ok := currentCustomerID(c)
	if !ok {
		return c.Status(401).JSON(fiber.Map{"error": "sesión inválida"})
	}
	addressID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	input, err := addressInputFromBody(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	addr, err := service.NewEcommerceService(db(c)).UpdateCustomerAddress(customerID, uint(addressID), input)
	if err != nil {
		// Mismo mensaje tanto para "no existe" como para "es de otro cliente" — no revela cuál.
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": addr})
}

func (h *EcommerceHandler) DeleteCustomerAddressAPI(c fiber.Ctx) error {
	customerID, ok := currentCustomerID(c)
	if !ok {
		return c.Status(401).JSON(fiber.Map{"error": "sesión inválida"})
	}
	addressID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewEcommerceService(db(c)).DeleteCustomerAddress(customerID, uint(addressID)); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ── Pedidos del cliente ──────────────────────────────────────────────

func (h *EcommerceHandler) ListCustomerOrdersAPI(c fiber.Ctx) error {
	customerID, ok := currentCustomerID(c)
	if !ok {
		return c.Status(401).JSON(fiber.Map{"error": "sesión inválida"})
	}
	rows, err := service.NewEcommerceService(db(c)).ListCustomerOrders(customerID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// GetCustomerOrderAPI el 404 es idéntico tanto si el ID no existe como si el pedido es de OTRO
// cliente — un cliente nunca debe poder distinguir "no existe" de "no es tuyo" (evita usar el
// endpoint para enumerar pedidos ajenos probando IDs).
func (h *EcommerceHandler) GetCustomerOrderAPI(c fiber.Ctx) error {
	customerID, ok := currentCustomerID(c)
	if !ok {
		return c.Status(401).JSON(fiber.Map{"error": "sesión inválida"})
	}
	orderID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	order, items, err := service.NewEcommerceService(db(c)).GetCustomerOrder(customerID, uint(orderID))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": order, "items": items})
}

func (h *EcommerceHandler) LinkGuestOrderAPI(c fiber.Ctx) error {
	customerID, ok := currentCustomerID(c)
	if !ok {
		return c.Status(401).JSON(fiber.Map{"error": "sesión inválida"})
	}
	var body struct {
		OrderID uint `json:"order_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	order, err := service.NewEcommerceService(db(c)).LinkGuestOrder(customerID, body.OrderID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": order})
}
