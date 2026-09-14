package service

import (
	"testing"
)

func TestValidateSaleItemPrices(t *testing.T) {
	cases := []struct {
		name    string
		items   []SaleItemInput
		wantErr bool
	}{
		{
			name:  "precio real pasa",
			items: []SaleItemInput{{Description: "Gaseosa 500ml", UnitPrice: 3.5, Quantity: 1}},
		},
		{
			name:    "precio en 0 se rechaza",
			items:   []SaleItemInput{{Description: "Gaseosa 500ml", UnitPrice: 0, Quantity: 1}},
			wantErr: true,
		},
		{
			name:    "precio negativo se rechaza",
			items:   []SaleItemInput{{Description: "Gaseosa 500ml", UnitPrice: -1, Quantity: 1}},
			wantErr: true,
		},
		{
			name: "bonificación (código 15) también exige precio real",
			items: []SaleItemInput{{
				Description: "Gaseosa 500ml (bonificación)", UnitPrice: 0, Quantity: 1, IgvAffectationType: "15",
			}},
			wantErr: true,
		},
		{
			name: "una línea válida no salva a otra en 0",
			items: []SaleItemInput{
				{Description: "Polo", UnitPrice: 25, Quantity: 1},
				{Description: "Pantalón", UnitPrice: 0, Quantity: 1},
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSaleItemPrices(tc.items)
			if tc.wantErr && err == nil {
				t.Fatalf("esperaba error, no hubo ninguno")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("no esperaba error, obtuve: %v", err)
			}
		})
	}
}

// TestSaleService_Create_RejectsZeroUnitPrice ejercita el camino real (Create) con un ítem
// manual (sin product_id, así resolveComboItems/fillMissingItemCodes no tocan la BD) para
// confirmar que la venta se rechaza antes de persistir nada.
func TestSaleService_Create_RejectsZeroUnitPrice(t *testing.T) {
	db := setupSaleCombosDB(t)
	svc := NewSaleService(db)

	_, err := svc.Create(CreateSaleInput{
		BranchID: 1,
		UserID:   1,
		Items: []SaleItemInput{
			{Description: "Servicio manual", Code: "MANUAL", Unit: "NIU", Quantity: 1, UnitPrice: 0, IgvAffectationType: "10"},
		},
	})
	if err == nil {
		t.Fatal("esperaba que Create() rechace un ítem con unit_price 0, no devolvió error")
	}
}
