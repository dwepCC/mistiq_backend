package service

import "tukifac/pkg/database"

func (s *SaleService) enrichSalesWithDetraccion(sales []database.TenantSale) {
	if len(sales) == 0 {
		return
	}
	ids := make([]uint, len(sales))
	for i := range sales {
		ids[i] = sales[i].ID
	}
	var rows []database.TenantSaleDetraccion
	if err := s.db.Where("sale_id IN ?", ids).Find(&rows).Error; err != nil {
		return
	}
	bySale := make(map[uint]database.TenantSaleDetraccion, len(rows))
	for _, r := range rows {
		bySale[r.SaleID] = r
	}
	for i := range sales {
		r, ok := bySale[sales[i].ID]
		if !ok {
			continue
		}
		sales[i].HasDetraccion = true
		sales[i].DetraccionAmount = r.DetractionAmountPen
		sales[i].NetPayable = r.NetPayablePen
		sales[i].DetraccionRatePercent = r.RatePercent
	}
}
