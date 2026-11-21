package service

import (
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/taxpayment"
)

// enrichSalesWithChangeAmount calcula el vuelto de cada venta para listados/reportes — mismo
// criterio que print_data.go (buildPrintData): suma los pagos directos (sin contar el marcador
// "credito" ni pagos de detracción) y lo compara contra el importe cobrable (net_payable si la
// venta tiene detracción, si no sale.Total); el excedente es el vuelto. Debe correr DESPUÉS de
// enrichSalesWithDetraccion, para que HasDetraccion/NetPayable ya estén poblados.
func (s *SaleService) enrichSalesWithChangeAmount(sales []database.TenantSale) {
	if len(sales) == 0 {
		return
	}
	ids := make([]uint, len(sales))
	for i := range sales {
		ids[i] = sales[i].ID
	}
	var payments []database.TenantSalePayment
	if err := s.db.Where("sale_id IN ?", ids).Find(&payments).Error; err != nil {
		return
	}
	directSumBySale := make(map[uint]float64, len(sales))
	for _, p := range payments {
		if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		directSumBySale[p.SaleID] += p.Amount
	}
	for i := range sales {
		paid, ok := directSumBySale[sales[i].ID]
		if !ok {
			continue
		}
		payable := sales[i].Total
		if sales[i].HasDetraccion && sales[i].NetPayable > 0 {
			payable = sales[i].NetPayable
		}
		sales[i].ChangeAmount = money.CalcPaymentChange(paid, payable)
	}
}
