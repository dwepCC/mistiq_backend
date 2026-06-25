package detraccion

import (
	"errors"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	sunatdet "tukifac/pkg/sunat/detraccion"

	"gorm.io/gorm"
)

// Service persiste y carga detracción por venta.
type Service struct {
	db      *gorm.DB
	catalog *sunatdet.CatalogProvider
}

func NewService(db *gorm.DB) *Service {
	cat, _ := sunatdet.DefaultCatalog()
	return &Service{db: db, catalog: cat}
}

// SaleInput datos de detracción enviados al crear venta.
type SaleInput struct {
	GoodCode          string `json:"good_code"`
	PaymentMethodCode string `json:"payment_method_code,omitempty"`
}

// PersistInput datos completos para guardar detracción.
type PersistInput struct {
	SaleID            uint
	OperationTypeCode string
	SunatDocCode      string
	Currency          string
	ExchangeRate      *float64
	SaleTotal         float64
	GravadoTotal      float64
	BankAccount       string
	PaymentMethodCode string
	Detraccion        *SaleInput
	ContactEsPercepcion bool
}

// Evaluate calcula detracción sin persistir (validación de pagos en venta 1001).
func (s *Service) Evaluate(in PersistInput) (sunatdet.CalcResult, error) {
	return s.evaluate(in)
}

func (s *Service) evaluate(in PersistInput) (sunatdet.CalcResult, error) {
	op := in.OperationTypeCode
	if op != sunatdet.OpDetraccionGeneral {
		return sunatdet.CalcResult{}, nil
	}
	if in.Detraccion == nil {
		return sunatdet.CalcResult{}, errors.New("operación sujeta a detracción requiere datos de detracción")
	}
	paymentCode := strings.TrimSpace(in.PaymentMethodCode)
	if paymentCode == "" {
		paymentCode = strings.TrimSpace(in.Detraccion.PaymentMethodCode)
	}
	return sunatdet.Evaluate(s.catalog, sunatdet.CalcInput{
		OperationTypeCode:   op,
		SunatDocCode:        in.SunatDocCode,
		Currency:            in.Currency,
		ExchangeRate:        in.ExchangeRate,
		GravadoTotalPEN:     in.GravadoTotal,
		SaleTotalPEN:        in.SaleTotal,
		GoodCode:            in.Detraccion.GoodCode,
		BankAccount:         in.BankAccount,
		PaymentMethodCode:   paymentCode,
		ContactEsPercepcion: in.ContactEsPercepcion,
	})
}

// Persist evalúa y guarda tenant_sale_detraccion cuando operation_type = 1001.
func (s *Service) Persist(in PersistInput) (*database.TenantSaleDetraccion, error) {
	op := in.OperationTypeCode
	if op != sunatdet.OpDetraccionGeneral {
		return nil, nil
	}
	eval, err := s.evaluate(in)
	if err != nil {
		return nil, err
	}
	if !eval.Applicable {
		return nil, errors.New(eval.Reason)
	}

	row := database.TenantSaleDetraccion{
		SaleID:              in.SaleID,
		GoodCode:            eval.GoodCode,
		PaymentMethodCode:   eval.PaymentMethodCode,
		BankAccount:         eval.BankAccount,
		RatePercent:         eval.RatePercent,
		BaseAmountPen:       eval.BaseAmountPEN,
		DetractionAmountPen: eval.DetractionAmountPEN,
		InvoiceTotalPen:     in.SaleTotal,
		NetPayablePen:       eval.NetPayablePEN,
		BnConfirmationStatus: "pending",
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	if err := s.db.Save(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// LoadBySaleID carga detracción de una venta.
func (s *Service) LoadBySaleID(saleID uint) (*database.TenantSaleDetraccion, error) {
	var row database.TenantSaleDetraccion
	err := s.db.First(&row, "sale_id = ?", saleID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ApplyToInvoicePayload añade detraccion y leyenda 2006 al payload Lycet.
func ApplyToInvoicePayload(payload *facturador.InvoicePayload, row *database.TenantSaleDetraccion) {
	if payload == nil || row == nil {
		return
	}
	payload.TipoOperacion = sunatdet.OpDetraccionGeneral
	payload.Detraccion = &facturador.InvoiceDetraction{
		Percent:           row.RatePercent,
		Mount:             row.DetractionAmountPen,
		CtaBanco:          row.BankAccount,
		CodMedioPago:      row.PaymentMethodCode,
		CodBienDetraccion: row.GoodCode,
	}
	facturador.AppendSUNATLegend2006(&payload.Legends)
}
