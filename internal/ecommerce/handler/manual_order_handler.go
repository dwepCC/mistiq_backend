package handler

import (
	"encoding/json"
	"strings"

	"tukifac/internal/ecommerce/service"
	"tukifac/pkg/tenantstorage"

	"github.com/gofiber/fiber/v3"
)

// CreateManualOrderAPI POST /api/ecommerce/orders — pedido registrado por el personal (WhatsApp, llamada,
// mostrador). Mismas reglas que el de la tienda (precio y stock salen del servidor), pero admite productos
// no publicados y empresas de envío que la tienda no muestra.
func (h *EcommerceHandler) CreateManualOrderAPI(c fiber.Ctx) error {
	var body struct {
		CustomerName   string `json:"customer_name"`
		CustomerPhone  string `json:"customer_phone"`
		ContactID      *uint  `json:"contact_id"`
		DeliveryMethod string `json:"delivery_method"`
		Shipping       *struct {
			CarrierID          *uint  `json:"carrier_id"`
			Ubigeo             string `json:"ubigeo"`
			Agency             string `json:"agency"`
			AgencyAddress      string `json:"agency_address"`
			RecipientName      string `json:"recipient_name"`
			RecipientDocType   string `json:"recipient_doc_type"`
			RecipientDocNumber string `json:"recipient_doc_number"`
			RecipientPhone     string `json:"recipient_phone"`
		} `json:"shipping"`
		Address *struct {
			AddressLine string `json:"address_line"`
			Reference   string `json:"reference"`
			Ubigeo      string `json:"ubigeo"`
		} `json:"address"`
		Items []struct {
			ProductID      uint            `json:"product_id"`
			PresentationID *uint           `json:"presentation_id"`
			Quantity       float64         `json:"quantity"`
			Combo          json.RawMessage `json:"combo"`
		} `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	items := make([]service.CreateOrderItemInput, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, service.CreateOrderItemInput{
			ProductID: it.ProductID, PresentationID: it.PresentationID, Quantity: it.Quantity,
			ComboJSON: comboSelection(it.Combo),
		})
	}
	input := service.CreateOrderInput{
		CustomerName: body.CustomerName, CustomerPhone: body.CustomerPhone, ContactID: body.ContactID,
		DeliveryMethod: body.DeliveryMethod, Items: items, Manual: true, UserID: orderUserID(c),
		CentralTenantID: orderCentralTenantID(c),
	}
	if sh := body.Shipping; sh != nil {
		input.CarrierID = sh.CarrierID
		input.DestinationUbigeo = sh.Ubigeo
		input.DestinationAgency = sh.Agency
		input.DestinationAddress = sh.AgencyAddress
		input.RecipientName = sh.RecipientName
		input.RecipientDocType = sh.RecipientDocType
		input.RecipientDocNumber = sh.RecipientDocNumber
		input.RecipientPhone = sh.RecipientPhone
	}
	if a := body.Address; a != nil {
		input.GuestAddressLine = a.AddressLine
		input.GuestReference = a.Reference
		input.GuestUbigeo = a.Ubigeo
	}
	order, orderItems, err := service.NewEcommerceService(db(c)).CreateOrder(input)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": order, "items": orderItems, "order_number": order.ID})
}

// UploadPaymentVoucherAPI POST /api/ecommerce/payments/voucher (multipart, campo «image») — sube la foto
// del voucher y devuelve su URL, que luego se envía como voucher_url al registrar el abono.
func (h *EcommerceHandler) UploadPaymentVoucherAPI(c fiber.Ctx) error {
	url, err := uploadEcommerceImage(c, "image", voucherFilePrefix)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"url": url})
}

const voucherFilePrefix = "voucher"

// validVoucherURL acepta vacío o una foto subida con UploadPaymentVoucherAPI en la carpeta de ESTE negocio:
// el abono no puede apuntar a una dirección cualquiera.
func validVoucherURL(c fiber.Ctx, raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	ruc, err := tenantstorage.ResolveTenantRUC(c)
	if err != nil {
		return false
	}
	prefix := tenantstorage.TenantUploadPublicURL(ruc, "ecommerce", voucherFilePrefix+"-")
	return strings.HasPrefix(raw, prefix) && !strings.ContainsAny(raw[len(prefix):], `/\?#`)
}
