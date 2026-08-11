package prepayment

import (
	"strings"

	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	sunatpre "tukifac/pkg/sunat/prepayment"
)

// ReconcileAllPendingVouchers abre vouchers pendientes cuya venta ya fue aceptada por SUNAT
// (legacy PHP: state_type_id = 05). Se ejecuta antes de listar anticipos disponibles.
func (s *Service) ReconcileAllPendingVouchers() error {
	var rows []database.TenantSalePrepaymentVoucher
	err := s.db.
		Where("status = ?", sunatpre.StatusPendingAcceptance).
		Find(&rows).Error
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ContactID == nil || *row.ContactID == 0 {
			var sale database.TenantSale
			if s.db.First(&sale, row.SaleID).Error == nil && sale.ContactID != nil && *sale.ContactID > 0 {
				_ = s.db.Model(&row).Update("contact_id", sale.ContactID).Error
			}
		}
		if s.isSaleFiscallyAccepted(row.SaleID) {
			_ = s.OnFiscalAccept(row.SaleID)
		}
	}
	return nil
}

// ReconcileVouchersForContact mantiene compatibilidad; reconcilia todo el tenant.
func (s *Service) ReconcileVouchersForContact(_ uint) error {
	return s.ReconcileAllPendingVouchers()
}

func (s *Service) isSaleFiscallyAccepted(saleID uint) bool {
	var sale database.TenantSale
	if err := s.db.First(&sale, saleID).Error; err != nil {
		return false
	}
	if billingstate.NormalizeBillingStatus(sale.BillingStatus) == billingstate.BillingAccepted {
		return true
	}
	var inv database.TenantInvoice
	if err := s.db.Where("sale_id = ?", saleID).First(&inv).Error; err != nil {
		return false
	}
	if billingstate.HasAcceptanceEvidence(&inv) {
		return true
	}
	// PHP legacy: state_type_id = 05 aunque falte URL del CDR persistida.
	st := strings.TrimSpace(inv.SunatStatus)
	code := strings.TrimSpace(inv.SunatCDRCode)
	return st == "accepted" || code == "0"
}
