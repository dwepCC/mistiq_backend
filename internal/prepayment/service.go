package prepayment

import (
	"errors"
	"strings"
	"time"

	"tukifac/pkg/database"
	sunatpre "tukifac/pkg/sunat/prepayment"

	"gorm.io/gorm"
)

// Service persiste y carga vouchers de anticipo por venta.
type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// PersistInput datos para registrar un comprobante de anticipo al crear la venta.
type PersistInput struct {
	SaleID            uint
	ContactID         *uint
	SunatDocCode      string
	DocumentNumber    string
	OperationTypeCode string
	Currency          string
	SaleTotal         float64
	AffectationGroup  string
	ItemAffs          []string
}

// Persist guarda tenant_sale_prepayment_vouchers cuando la venta es emisión de anticipo.
func (s *Service) Persist(in PersistInput) (*database.TenantSalePrepaymentVoucher, error) {
	if err := sunatpre.ValidateItemsAffectationGroup(in.AffectationGroup, in.ItemAffs); err != nil {
		return nil, err
	}
	sunatCode := strings.TrimSpace(in.SunatDocCode)
	if sunatCode != "01" && sunatCode != "03" {
		return nil, errors.New("el comprobante de anticipo solo aplica a factura (01) o boleta (03)")
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = "PEN"
	}
	now := time.Now()
	row := database.TenantSalePrepaymentVoucher{
		SaleID:            in.SaleID,
		ContactID:         in.ContactID,
		SunatDocCode:      sunatCode,
		DocumentNumber:    strings.TrimSpace(in.DocumentNumber),
		OperationTypeCode: strings.TrimSpace(in.OperationTypeCode),
		AffectationGroup:  strings.TrimSpace(in.AffectationGroup),
		RelatedDocType:    sunatpre.RelatedDocTypeForSunatCode(sunatCode),
		OriginalAmount:    in.SaleTotal,
		BalanceAmount:     in.SaleTotal,
		Currency:          currency,
		Status:            sunatpre.StatusPendingAcceptance,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if row.OperationTypeCode == "" {
		row.OperationTypeCode = sunatpre.EmitOperationTypeCode()
	}
	if err := s.db.Save(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// LoadBySaleID carga el voucher de anticipo de una venta.
func (s *Service) LoadBySaleID(saleID uint) (*database.TenantSalePrepaymentVoucher, error) {
	var row database.TenantSalePrepaymentVoucher
	err := s.db.First(&row, "sale_id = ?", saleID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// OnFiscalAccept marca el voucher como disponible tras aceptación SUNAT (sin consumir saldo).
func (s *Service) OnFiscalAccept(saleID uint) error {
	var row database.TenantSalePrepaymentVoucher
	if err := s.db.First(&row, "sale_id = ?", saleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if row.Status == sunatpre.StatusOpen {
		return nil
	}
	now := time.Now()
	updates := map[string]interface{}{
		"status":       sunatpre.StatusOpen,
		"available_at": &now,
		"updated_at":   now,
	}
	if strings.TrimSpace(row.DocumentNumber) == "" {
		var sale database.TenantSale
		if s.db.First(&sale, saleID).Error == nil && strings.TrimSpace(sale.Number) != "" {
			updates["document_number"] = strings.TrimSpace(sale.Number)
		}
	}
	return s.db.Model(&row).Updates(updates).Error
}
