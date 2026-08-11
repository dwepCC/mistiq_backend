package prepayment

import (
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	sunatpre "tukifac/pkg/sunat/prepayment"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ResolvedDeduction anticipo validado listo para persistir y XML.
type ResolvedDeduction struct {
	SourceSaleID     uint
	DocumentNumber   string
	RelatedDocType   string
	AffectationGroup string
	Amount           float64
	Total            float64
}

// DeductionPlan resultado de validar deducciones sobre una venta.
type DeductionPlan struct {
	Resolved         []ResolvedDeduction
	ApplyResult      sunatpre.ApplyDeductionResult
	AdjustedSubtotal float64
	AdjustedTax      float64
	AdjustedTotal    float64
}

// ListOpenVouchers anticipos abiertos por afectación (legacy GET /documents/prepayments/{type}, sin filtro de cliente).
func (s *Service) ListOpenVouchers(_ uint, affectationGroup string, taxRatePercent float64) ([]OpenVoucherOption, error) {
	group := strings.TrimSpace(affectationGroup)
	if !sunatpre.IsValidAffectationGroup(group) {
		return nil, errors.New("indique afectación gravado, exonerado o inafecto")
	}
	_ = s.ReconcileAllPendingVouchers()

	var rows []database.TenantSalePrepaymentVoucher
	err := s.db.
		Joins("JOIN tenant_sales ON tenant_sales.id = tenant_sale_prepayment_vouchers.sale_id").
		Where("tenant_sale_prepayment_vouchers.affectation_group = ?", group).
		Where("tenant_sale_prepayment_vouchers.status = ?", sunatpre.StatusOpen).
		Where("tenant_sale_prepayment_vouchers.balance_amount > ?", 0.009).
		Order("tenant_sale_prepayment_vouchers.available_at ASC, tenant_sale_prepayment_vouchers.sale_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]OpenVoucherOption, 0, len(rows))
	for _, row := range rows {
		base, total := sunatpre.BaseFromBalance(row.BalanceAmount, group, taxRatePercent)
		docNum := strings.TrimSpace(row.DocumentNumber)
		if docNum == "" {
			var sale database.TenantSale
			if s.db.First(&sale, row.SaleID).Error == nil {
				docNum = strings.TrimSpace(sale.Number)
			}
		}
		contactIDVal, contactName := s.voucherContactInfo(&row)
		desc := docNum
		if contactName != "" {
			desc = docNum + " — " + contactName
		}
		out = append(out, OpenVoucherOption{
			SourceSaleID:     row.SaleID,
			Description:      desc,
			DocumentNumber:   docNum,
			RelatedDocType:   row.RelatedDocType,
			SunatDocCode:     row.SunatDocCode,
			AffectationGroup: row.AffectationGroup,
			ContactID:        contactIDVal,
			ContactName:      contactName,
			Amount:           base,
			Total:            total,
			BalanceAmount:    row.BalanceAmount,
			Currency:         row.Currency,
		})
	}
	return out, nil
}

// PlanDeductions valida deducciones y calcula totales ajustados.
func (s *Service) PlanDeductions(
	contactID *uint,
	group string,
	saleItems []database.TenantSaleItem,
	deductions []DeductionInput,
	taxRatePercent float64,
) (DeductionPlan, error) {
	if len(deductions) == 0 {
		return DeductionPlan{}, errors.New("agregue al menos un anticipo a deducir")
	}
	if contactID == nil || *contactID == 0 {
		return DeductionPlan{}, errors.New("seleccione un cliente para deducir anticipos")
	}
	_ = s.ReconcileVouchersForContact(*contactID)

	group = strings.TrimSpace(group)
	if !sunatpre.IsValidAffectationGroup(group) {
		return DeductionPlan{}, errors.New("indique afectación gravado, exonerado o inafecto")
	}
	itemAffs := make([]string, 0, len(saleItems))
	for _, it := range saleItems {
		itemAffs = append(itemAffs, it.IgvAffectationType)
	}
	if err := sunatpre.ValidateItemsAffectationGroup(group, itemAffs); err != nil {
		return DeductionPlan{}, err
	}

	groupTotals := grossSaleGroupTotalsFromItems(saleItems)
	resolved := make([]ResolvedDeduction, 0, len(deductions))
	var baseSum, totalSum float64
	seen := make(map[uint]struct{}, len(deductions))

	for _, d := range deductions {
		if d.SourceSaleID == 0 {
			return DeductionPlan{}, errors.New("anticipo inválido")
		}
		if _, ok := seen[d.SourceSaleID]; ok {
			return DeductionPlan{}, errors.New("no repita el mismo comprobante de anticipo")
		}
		seen[d.SourceSaleID] = struct{}{}
		if d.Amount <= 0 {
			return DeductionPlan{}, errors.New("el monto a deducir debe ser mayor a cero")
		}

		var voucher database.TenantSalePrepaymentVoucher
		if err := s.db.First(&voucher, "sale_id = ?", d.SourceSaleID).Error; err != nil {
			return DeductionPlan{}, fmt.Errorf("anticipo no encontrado (venta %d)", d.SourceSaleID)
		}
		if voucher.Status != sunatpre.StatusOpen {
			if !s.isSaleFiscallyAccepted(voucher.SaleID) {
				return DeductionPlan{}, fmt.Errorf("el anticipo %s aún no está aceptado por SUNAT", voucher.DocumentNumber)
			}
			_ = s.OnFiscalAccept(voucher.SaleID)
			if err := s.db.First(&voucher, "sale_id = ?", d.SourceSaleID).Error; err != nil {
				return DeductionPlan{}, fmt.Errorf("anticipo no encontrado (venta %d)", d.SourceSaleID)
			}
		}
		if voucher.BalanceAmount <= 0.009 {
			return DeductionPlan{}, fmt.Errorf("el anticipo %s no tiene saldo disponible", voucher.DocumentNumber)
		}
		if !voucherBelongsToContact(s.db, &voucher, *contactID) {
			return DeductionPlan{}, fmt.Errorf("el anticipo %s no pertenece al cliente seleccionado", voucher.DocumentNumber)
		}
		if voucher.AffectationGroup != group {
			return DeductionPlan{}, fmt.Errorf("el anticipo %s tiene afectación distinta", voucher.DocumentNumber)
		}

		maxBase, _ := sunatpre.BaseFromBalance(voucher.BalanceAmount, group, taxRatePercent)
		if d.Amount > maxBase+0.009 {
			return DeductionPlan{}, fmt.Errorf("el monto deducido supera el saldo del anticipo %s", voucher.DocumentNumber)
		}
		saleMaxBase := sunatpre.SaleGroupDeductibleBase(group, groupTotals) - baseSum
		if saleMaxBase < 0 {
			saleMaxBase = 0
		}
		if d.Amount > saleMaxBase+0.009 {
			return DeductionPlan{}, errors.New("el monto deducido supera el total de la venta")
		}
		total := sunatpre.DeductionTotalFromBaseCapped(d.Amount, group, taxRatePercent, voucher.BalanceAmount)

		resolved = append(resolved, ResolvedDeduction{
			SourceSaleID:     voucher.SaleID,
			DocumentNumber:   voucher.DocumentNumber,
			RelatedDocType:   voucher.RelatedDocType,
			AffectationGroup: voucher.AffectationGroup,
			Amount:           d.Amount,
			Total:            total,
		})
		baseSum += d.Amount
		totalSum += total
	}

	if totalSum > groupTotals.Total+0.009 {
		return DeductionPlan{}, errors.New("el total deducido supera el total de la venta")
	}

	applyRes, err := sunatpre.ApplyDeductionToSaleTotals(group, groupTotals, baseSum, totalSum, taxRatePercent)
	if err != nil {
		return DeductionPlan{}, err
	}

	return DeductionPlan{
		Resolved:         resolved,
		ApplyResult:      applyRes,
		AdjustedSubtotal: applyRes.Totals.Subtotal,
		AdjustedTax:      applyRes.Totals.TaxAmount,
		AdjustedTotal:    applyRes.Totals.Total,
	}, nil
}

// PersistApplicationsTx registra aplicaciones y descuenta saldo de vouchers (transacción).
func (s *Service) PersistApplicationsTx(tx *gorm.DB, consumerSaleID uint, resolved []ResolvedDeduction) error {
	for _, r := range resolved {
		var voucher database.TenantSalePrepaymentVoucher
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&voucher, "sale_id = ?", r.SourceSaleID).Error; err != nil {
			return fmt.Errorf("anticipo no encontrado: %w", err)
		}
		if voucher.BalanceAmount+0.009 < r.Total {
			return fmt.Errorf("saldo insuficiente en anticipo %s", voucher.DocumentNumber)
		}
		row := database.TenantSalePrepaymentApplication{
			ConsumerSaleID:   consumerSaleID,
			SourceSaleID:     r.SourceSaleID,
			DocumentNumber:   r.DocumentNumber,
			RelatedDocType:   r.RelatedDocType,
			AffectationGroup: r.AffectationGroup,
			Amount:           r.Amount,
			Total:            r.Total,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		newBalance := voucher.BalanceAmount - r.Total
		if newBalance < 0 {
			newBalance = 0
		}
		if err := tx.Model(&voucher).Update("balance_amount", newBalance).Error; err != nil {
			return err
		}
	}
	return nil
}

// LoadApplicationsByConsumerSale carga deducciones aplicadas en una venta.
func (s *Service) LoadApplicationsByConsumerSale(consumerSaleID uint) ([]database.TenantSalePrepaymentApplication, error) {
	var rows []database.TenantSalePrepaymentApplication
	err := s.db.Where("consumer_sale_id = ?", consumerSaleID).Order("id ASC").Find(&rows).Error
	return rows, err
}

// DeductionFiscalContext reconstruye totales SUNAT de deducción para billing/XML.
func (s *Service) DeductionFiscalContext(
	consumerSaleID uint,
	items []database.TenantSaleItem,
	taxRatePercent float64,
) ([]database.TenantSalePrepaymentApplication, sunatpre.SaleGroupTotals, sunatpre.ApplyDeductionResult, bool, error) {
	apps, err := s.LoadApplicationsByConsumerSale(consumerSaleID)
	if err != nil {
		return nil, sunatpre.SaleGroupTotals{}, sunatpre.ApplyDeductionResult{}, false, err
	}
	if len(apps) == 0 {
		return nil, sunatpre.SaleGroupTotals{}, sunatpre.ApplyDeductionResult{}, false, nil
	}
	group := apps[0].AffectationGroup
	var baseSum, totalSum float64
	for _, a := range apps {
		if a.AffectationGroup != group {
			return nil, sunatpre.SaleGroupTotals{}, sunatpre.ApplyDeductionResult{}, false, fmt.Errorf("aplicaciones de anticipo con afectaciones mixtas")
		}
		baseSum += a.Amount
		totalSum += a.Total
	}
	grossTotals := grossSaleGroupTotalsFromItems(items)
	applyRes, err := sunatpre.ApplyDeductionToSaleTotals(group, grossTotals, baseSum, totalSum, taxRatePercent)
	if err != nil {
		return nil, sunatpre.SaleGroupTotals{}, sunatpre.ApplyDeductionResult{}, false, err
	}
	return apps, grossTotals, applyRes, true, nil
}

// grossSaleGroupTotalsFromItems suma bases bruta por ítem (PHP: total_value / bases antes de deducir anticipo).
// Incluye GlobalDiscountSubtotal porque el Subtotal persistido ya tiene descontado el global.
func grossSaleGroupTotalsFromItems(items []database.TenantSaleItem) sunatpre.SaleGroupTotals {
	var out sunatpre.SaleGroupTotals
	for _, it := range items {
		base := itemGrossBase(it)
		aff := strings.TrimSpace(it.IgvAffectationType)
		if aff == "" {
			aff = "10"
		}
		switch {
		case strings.HasPrefix(aff, "2"):
			out.ExoneradoSubtotal += base
			out.ExoneradoTotal += it.Total
		case strings.HasPrefix(aff, "3"):
			out.InafectoSubtotal += base
			out.InafectoTotal += it.Total
		default:
			if tax.IsGravado(aff) {
				out.GravadoSubtotal += base
				out.GravadoTax += it.TaxAmount
				out.GravadoTotal += it.Total
			}
		}
		out.Subtotal += base
		out.TaxAmount += it.TaxAmount
		out.Total += it.Total
	}
	out.GravadoSubtotal = round2(out.GravadoSubtotal)
	out.ExoneradoSubtotal = round2(out.ExoneradoSubtotal)
	out.InafectoSubtotal = round2(out.InafectoSubtotal)
	out.Subtotal = round2(out.Subtotal)
	return out
}

func itemGrossBase(it database.TenantSaleItem) float64 {
	if it.GlobalDiscountSubtotal > 0 {
		return round2(it.Subtotal + it.GlobalDiscountSubtotal)
	}
	return round2(it.Subtotal)
}

func saleGroupTotalsFromItems(items []database.TenantSaleItem) sunatpre.SaleGroupTotals {
	var out sunatpre.SaleGroupTotals
	for _, it := range items {
		aff := strings.TrimSpace(it.IgvAffectationType)
		if aff == "" {
			aff = "10"
		}
		switch {
		case strings.HasPrefix(aff, "2"):
			out.ExoneradoSubtotal += it.Subtotal
			out.ExoneradoTotal += it.Total
		case strings.HasPrefix(aff, "3"):
			out.InafectoSubtotal += it.Subtotal
			out.InafectoTotal += it.Total
		default:
			if tax.IsGravado(aff) {
				out.GravadoSubtotal += it.Subtotal
				out.GravadoTax += it.TaxAmount
				out.GravadoTotal += it.Total
			}
		}
		out.Subtotal += it.Subtotal
		out.TaxAmount += it.TaxAmount
		out.Total += it.Total
	}
	return out
}

func voucherBelongsToContact(db *gorm.DB, voucher *database.TenantSalePrepaymentVoucher, contactID uint) bool {
	if voucher.ContactID != nil && *voucher.ContactID == contactID {
		return true
	}
	var sale database.TenantSale
	if db.First(&sale, voucher.SaleID).Error != nil || sale.ContactID == nil {
		return false
	}
	return *sale.ContactID == contactID
}

func (s *Service) voucherContactInfo(voucher *database.TenantSalePrepaymentVoucher) (*uint, string) {
	var contactID uint
	if voucher.ContactID != nil && *voucher.ContactID > 0 {
		contactID = *voucher.ContactID
	} else {
		var sale database.TenantSale
		if s.db.First(&sale, voucher.SaleID).Error == nil && sale.ContactID != nil && *sale.ContactID > 0 {
			contactID = *sale.ContactID
		}
	}
	if contactID == 0 {
		return nil, ""
	}
	var contact database.TenantContact
	if s.db.First(&contact, contactID).Error != nil {
		return &contactID, ""
	}
	name := strings.TrimSpace(contact.TradeName)
	if name == "" {
		name = strings.TrimSpace(contact.BusinessName)
	}
	return &contactID, name
}
