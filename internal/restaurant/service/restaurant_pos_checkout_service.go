package service

import (
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// POSCheckoutInput reúne, en una sola operación, todo lo que hoy el POS de venta
// rápida envía en 4 requests (openSession → addOrder → getSession → billSession).
type POSCheckoutInput struct {
	// Sesión: si SessionID viene (borrador POS existente) se reutiliza; si no, se abre una nueva.
	SessionID     *uint
	BranchID      uint
	UserID        uint
	EmployeeType  string
	StaffID       *uint
	OrderType         string
	Guests            int
	Notes             string
	ContactID         *uint
	CustomerName      string
	CustomerPhone     string
	DeliveryDriverID  *uint
	DeliveryAddress   string
	DeliveryReference string
	EstimatedMinutes  int

	// Pedido (ítems del carrito).
	Items []NewOrderItem

	// Cobro.
	SeriesID        uint
	DocType         string
	Currency        string
	IssueDate       time.Time
	CashSessionID   *uint
	DiscountMode    string
	DiscountValue   float64
	DiscountAmount  float64
	Payments        []PaymentInput
	CentralTenantID uint
}

// RestaurantPOSCheckoutService orquesta el checkout del POS de venta rápida
// reutilizando de forma SECUENCIAL los servicios existentes (Opción A): no hay
// refactorización transaccional profunda; cada paso conserva su propia transacción
// y bloqueo, igual que hoy. El beneficio es eliminar los round-trips HTTP: el
// cliente hace una sola llamada y el backend encadena los pasos sin latencia de red
// entre ellos.
//
// No modifica el flujo de mesas/comandas/cocina: solo compone Open+Add+Bill para el POS.
// La venta rápida SIEMPRE cierra la sesión (CloseSession=true) y NO imprime a cocina
// (mismo comportamiento que el flujo actual del POS).
type RestaurantPOSCheckoutService struct {
	rs *RestaurantService
}

// NewRestaurantPOSCheckoutService construye el orquestador sobre el servicio base.
func NewRestaurantPOSCheckoutService(db *gorm.DB) *RestaurantPOSCheckoutService {
	return &RestaurantPOSCheckoutService{rs: New(db)}
}

// Checkout ejecuta OpenSession → AddOrder → BillTable y devuelve la venta creada.
// La construcción de print_data y el encolado fiscal se hacen en el handler (igual
// que en BillSession), para mantener este servicio enfocado en el dominio del POS.
func (s *RestaurantPOSCheckoutService) Checkout(in POSCheckoutInput, taxCfg tax.Config) (*database.TenantSale, error) {
	// Venta directa: sin mesa ni cocina, así que no hay nada que gestionar. Se emite con el
	// mismo servicio que el panel ERP, en una sola transacción, en vez de crear sesión +
	// pedido + comandas para borrarlas al facturar.
	if isDirectSaleCheckout(in) {
		return s.checkoutDirect(in, taxCfg)
	}

	// 1) Sesión: reutilizar la existente o abrir una nueva (venta rápida sin mesa).
	var sessionID uint
	if in.SessionID != nil && *in.SessionID > 0 {
		sessionID = *in.SessionID
	} else {
		guests := in.Guests
		if guests <= 0 {
			guests = 1
		}
		sess, err := s.rs.OpenSession(OpenSessionInput{
			TableID:           nil,
			StaffID:           in.StaffID,
			BranchID:          in.BranchID,
			UserID:            in.UserID,
			Guests:            guests,
			Notes:             in.Notes,
			OrderType:         in.OrderType,
			ContactID:         in.ContactID,
			CustomerName:      in.CustomerName,
			CustomerPhone:     in.CustomerPhone,
			DeliveryDriverID:  in.DeliveryDriverID,
			DeliveryAddress:   in.DeliveryAddress,
			DeliveryReference: in.DeliveryReference,
			EstimatedMinutes:  in.EstimatedMinutes,
		})
		if err != nil {
			return nil, err
		}
		sessionID = sess.ID
	}

	// 2) Pedido: agregar los ítems del carrito (crea comandas, igual que hoy).
	if len(in.Items) > 0 {
		if _, err := s.rs.AddOrder(sessionID, in.StaffID, in.UserID, in.Items, in.Notes); err != nil {
			return nil, err
		}
	}

	// 3) Cobro: factura la sesión y la cierra (borra las comandas, sin cocina).
	return s.rs.BillTable(BillInput{
		SessionID:       sessionID,
		UserID:          in.UserID,
		EmployeeType:    in.EmployeeType,
		SeriesID:        in.SeriesID,
		DocType:         in.DocType,
		IssueDate:       in.IssueDate,
		Currency:        in.Currency,
		ContactID:       in.ContactID,
		Payments:        in.Payments,
		CashSessionID:   in.CashSessionID,
		CloseSession:    true,
		DiscountAmount:  in.DiscountAmount,
		DiscountMode:    in.DiscountMode,
		DiscountValue:   in.DiscountValue,
		CentralTenantID: in.CentralTenantID,
	}, taxCfg)
}
