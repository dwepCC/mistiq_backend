package service

import (
	"fmt"
	"net/mail"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ── Registro / login ─────────────────────────────────────────────────

type RegisterCustomerInput struct {
	Name     string
	Phone    string
	Email    string // opcional
	Password string
}

func normalizeCustomerEmail(raw string) (*string, error) {
	email := strings.TrimSpace(raw)
	if email == "" {
		return nil, nil
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, fmt.Errorf("el correo no tiene un formato válido")
	}
	email = strings.ToLower(email)
	return &email, nil
}

// RegisterCustomer crea una cuenta de cliente final — SEPARADA de TenantUser (staff/RBAC) y de
// TenantContact (facturación). No autocompleta ContactID: vincular un TenantContact de forma
// segura queda fuera de esta fase (Contrato v2 §1.4.2).
func (s *EcommerceService) RegisterCustomer(input RegisterCustomerInput) (*database.TenantEcommerceCustomerAccount, error) {
	name := strings.TrimSpace(input.Name)
	phone := strings.TrimSpace(input.Phone)
	if name == "" {
		return nil, fmt.Errorf("el nombre es obligatorio")
	}
	if len(phone) < 6 {
		return nil, fmt.Errorf("ingresa un celular válido")
	}
	if len(input.Password) < 6 {
		return nil, fmt.Errorf("la contraseña debe tener al menos 6 caracteres")
	}
	email, err := normalizeCustomerEmail(input.Email)
	if err != nil {
		return nil, err
	}

	// Unicidad DENTRO del tenant actual (s.db ya está scopeado a esa BD — no hay unicidad global
	// posible ni deseable en una arquitectura multi-tenant con una BD por tenant).
	var existing int64
	s.db.Model(&database.TenantEcommerceCustomerAccount{}).Where("phone = ?", phone).Count(&existing)
	if existing > 0 {
		return nil, fmt.Errorf("ya existe una cuenta con ese celular")
	}
	if email != nil {
		var existingEmail int64
		s.db.Model(&database.TenantEcommerceCustomerAccount{}).Where("email = ?", *email).Count(&existingEmail)
		if existingEmail > 0 {
			return nil, fmt.Errorf("ya existe una cuenta con ese correo")
		}
	}

	account := &database.TenantEcommerceCustomerAccount{Name: name, Phone: phone, Email: email, Active: true}
	if err := account.SetPassword(input.Password); err != nil {
		return nil, err
	}
	if err := s.db.Create(account).Error; err != nil {
		return nil, fmt.Errorf("no se pudo crear la cuenta")
	}
	return account, nil
}

type LoginCustomerInput struct {
	Phone    string
	Password string
}

// LoginCustomer NO distingue en el mensaje de error entre "cuenta no existe" y "contraseña
// incorrecta" — mismo criterio de no facilitar enumeración de cuentas que ya sigue el resto del
// sistema de auth (TenantAuthAPI tampoco distingue "usuario no existe" de "password incorrecta").
func (s *EcommerceService) LoginCustomer(input LoginCustomerInput) (*database.TenantEcommerceCustomerAccount, error) {
	phone := strings.TrimSpace(input.Phone)
	var account database.TenantEcommerceCustomerAccount
	if err := s.db.Where("phone = ? AND active = ?", phone, true).First(&account).Error; err != nil {
		return nil, fmt.Errorf("celular o contraseña incorrectos")
	}
	if !account.CheckPassword(input.Password) {
		return nil, fmt.Errorf("celular o contraseña incorrectos")
	}
	return &account, nil
}

func (s *EcommerceService) GetCustomerAccount(id uint) (*database.TenantEcommerceCustomerAccount, error) {
	var account database.TenantEcommerceCustomerAccount
	if err := s.db.Where("active = ?", true).First(&account, id).Error; err != nil {
		return nil, fmt.Errorf("cuenta no encontrada")
	}
	return &account, nil
}

// ── Direcciones (todas exigen ownership: customerID viene SIEMPRE del token, nunca del body) ──

type CustomerAddressInput struct {
	Label       string
	AddressLine string
	Reference   string
	Ubigeo      string
	Phone       string
	IsDefault   bool
}

func (s *EcommerceService) ListCustomerAddresses(customerID uint) ([]database.TenantEcommerceCustomerAddress, error) {
	var rows []database.TenantEcommerceCustomerAddress
	err := s.db.Where("customer_account_id = ?", customerID).Order("is_default DESC, id DESC").Find(&rows).Error
	return rows, err
}

func (s *EcommerceService) CreateCustomerAddress(customerID uint, input CustomerAddressInput) (*database.TenantEcommerceCustomerAddress, error) {
	addressLine := strings.TrimSpace(input.AddressLine)
	if addressLine == "" {
		return nil, fmt.Errorf("la dirección es obligatoria")
	}
	if ubigeo := strings.TrimSpace(input.Ubigeo); ubigeo != "" && len(ubigeo) != 6 {
		return nil, fmt.Errorf("el ubigeo debe tener 6 dígitos")
	}
	addr := &database.TenantEcommerceCustomerAddress{
		CustomerAccountID: customerID,
		Label:             strings.TrimSpace(input.Label),
		AddressLine:       addressLine,
		Reference:         strings.TrimSpace(input.Reference),
		Ubigeo:            strings.TrimSpace(input.Ubigeo),
		Phone:             strings.TrimSpace(input.Phone),
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if input.IsDefault {
			if err := tx.Model(&database.TenantEcommerceCustomerAddress{}).
				Where("customer_account_id = ?", customerID).Update("is_default", false).Error; err != nil {
				return err
			}
			addr.IsDefault = true
		}
		return tx.Create(addr).Error
	})
	if err != nil {
		return nil, err
	}
	return addr, nil
}

// loadOwnedAddress carga la dirección SOLO si pertenece a customerID — el error es el mismo tanto
// si el ID no existe como si pertenece a otro cliente (no revela cuál de los dos casos es).
func (s *EcommerceService) loadOwnedAddress(customerID, addressID uint) (*database.TenantEcommerceCustomerAddress, error) {
	var addr database.TenantEcommerceCustomerAddress
	if err := s.db.Where("id = ? AND customer_account_id = ?", addressID, customerID).First(&addr).Error; err != nil {
		return nil, fmt.Errorf("dirección no encontrada")
	}
	return &addr, nil
}

func (s *EcommerceService) UpdateCustomerAddress(customerID, addressID uint, input CustomerAddressInput) (*database.TenantEcommerceCustomerAddress, error) {
	addr, err := s.loadOwnedAddress(customerID, addressID)
	if err != nil {
		return nil, err
	}
	addressLine := strings.TrimSpace(input.AddressLine)
	if addressLine == "" {
		return nil, fmt.Errorf("la dirección es obligatoria")
	}
	if ubigeo := strings.TrimSpace(input.Ubigeo); ubigeo != "" && len(ubigeo) != 6 {
		return nil, fmt.Errorf("el ubigeo debe tener 6 dígitos")
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if input.IsDefault {
			if err := tx.Model(&database.TenantEcommerceCustomerAddress{}).
				Where("customer_account_id = ? AND id <> ?", customerID, addressID).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Model(addr).Updates(map[string]interface{}{
			"label": strings.TrimSpace(input.Label), "address_line": addressLine,
			"reference": strings.TrimSpace(input.Reference), "ubigeo": strings.TrimSpace(input.Ubigeo),
			"phone": strings.TrimSpace(input.Phone), "is_default": input.IsDefault,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return s.loadOwnedAddress(customerID, addressID)
}

func (s *EcommerceService) DeleteCustomerAddress(customerID, addressID uint) error {
	if _, err := s.loadOwnedAddress(customerID, addressID); err != nil {
		return err
	}
	return s.db.Where("id = ? AND customer_account_id = ?", addressID, customerID).
		Delete(&database.TenantEcommerceCustomerAddress{}).Error
}

// ── Pedidos del cliente (ownership real, nunca solo por ID) ─────────

func (s *EcommerceService) ListCustomerOrders(customerID uint) ([]database.TenantEcommerceOrder, error) {
	var rows []database.TenantEcommerceOrder
	err := s.db.Where("customer_account_id = ?", customerID).Order("created_at DESC").Find(&rows).Error
	return rows, err
}

// CustomerDispatchView subconjunto SEGURO de TenantEcommerceDispatch para el cliente final (Fase
// 9, Contrato v2 §11) — deliberadamente sin UserID (quién despachó/entregó es dato interno del
// staff, no del cliente), sin Notes (podrían contener observaciones internas del almacén) y sin
// bultos/peso/dimensiones (dato operativo interno, no algo que el comprador necesite ver). Solo lo
// explícitamente pedido: estado, transportista, tracking, fecha de despacho, fecha de entrega.
type CustomerDispatchView struct {
	Status       string     `json:"status"`
	CarrierName  *string    `json:"carrier_name"`
	TrackingCode *string    `json:"tracking_code"`
	DispatchedAt *time.Time `json:"dispatched_at"`
	DeliveredAt  *time.Time `json:"delivered_at"`
}

// GetCustomerOrder: la condición customer_account_id=? va EN LA MISMA query — un pedido que existe
// pero pertenece a otro cliente (o es de invitado, sin cuenta) da el mismo "no encontrado" que un
// ID que no existe. Nunca se carga el pedido primero y se compara después (evita el error de
// "cargar y comparar en Go", que es fácil de olvidar en un handler futuro). Dispatch: nil si el
// pedido todavía no fue despachado — nunca se fabrica un estado de despacho que no existe.
func (s *EcommerceService) GetCustomerOrder(customerID, orderID uint) (*database.TenantEcommerceOrder, []database.TenantEcommerceOrderItem, *CustomerDispatchView, error) {
	var order database.TenantEcommerceOrder
	if err := s.db.Where("id = ? AND customer_account_id = ?", orderID, customerID).First(&order).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("pedido no encontrado")
	}
	var items []database.TenantEcommerceOrderItem
	if err := s.db.Where("order_id = ?", order.ID).Find(&items).Error; err != nil {
		return nil, nil, nil, err
	}
	dispatch, err := s.GetDispatchByOrderID(order.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	var view *CustomerDispatchView
	if dispatch != nil {
		view = &CustomerDispatchView{
			Status: dispatch.Status, CarrierName: dispatch.CarrierName, TrackingCode: dispatch.TrackingCode,
			DispatchedAt: dispatch.DispatchedAt, DeliveredAt: dispatch.DeliveredAt,
		}
	}
	return &order, items, view, nil
}

// LinkGuestOrder vincula un pedido de invitado a la cuenta autenticada — SOLO si el pedido no
// tiene ya cuenta asignada Y su customer_phone coincide EXACTAMENTE con el teléfono de ESTA cuenta
// (el que ya se registró, no uno enviado en el request). Deliberadamente más estricto que
// "order_id + phone mandado por el cliente" (Contrato v2 §1.4.1): acá el teléfono nunca es un dato
// que el llamador pueda inventar, siempre se deriva del token — así no hay forma de "probar"
// teléfonos ajenos para secuestrar pedidos de otra persona.
func (s *EcommerceService) LinkGuestOrder(customerID, orderID uint) (*database.TenantEcommerceOrder, error) {
	account, err := s.GetCustomerAccount(customerID)
	if err != nil {
		return nil, err
	}
	var order database.TenantEcommerceOrder
	if err := s.db.First(&order, orderID).Error; err != nil {
		return nil, fmt.Errorf("pedido no encontrado")
	}
	if order.CustomerAccountID != nil {
		return nil, fmt.Errorf("pedido no encontrado") // ya vinculado — mismo mensaje genérico
	}
	if !strings.EqualFold(strings.TrimSpace(order.CustomerPhone), strings.TrimSpace(account.Phone)) {
		return nil, fmt.Errorf("pedido no encontrado") // no revela si el ID existe pero el teléfono no coincide
	}
	if err := s.db.Model(&order).Update("customer_account_id", customerID).Error; err != nil {
		return nil, err
	}
	order.CustomerAccountID = &customerID
	return &order, nil
}
