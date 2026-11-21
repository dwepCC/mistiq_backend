package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"
)

// Ventas en USD: el catálogo está en soles, así que el precio autorizado de una línea es precio ÷ TC. Antes el precio
// de la línea se comparaba tal cual con el de catálogo y la venta en dólares salía con los números en soles (US$ 100
// por un producto de S/ 100).

// usdSale corre cada intento en su propio subtest: setupPriceHardeningDB nombra la BD en memoria con t.Name().
func usdSale(t *testing.T, price float64, rate *float64, override bool) error {
	t.Helper()
	var out error
	t.Run(fmt.Sprintf("p%.4f_o%v_r%v", price, override, rate != nil), func(t *testing.T) { out = usdSaleOnce(t, price, rate, override) })
	return out
}

func usdSaleOnce(t *testing.T, price float64, rate *float64, override bool) error {
	t.Helper()
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p := database.TenantProduct{Code: "EQ1", Name: "Equipo", Type: "product", Unit: "NIU", SalePrice: 100,
		IgvAffectationType: "10", PriceIncludesIgv: true, BranchID: 1, Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	_, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: "00", IssueDate: time.Now(),
		Currency: "USD", ExchangeRate: rate, TaxConfig: tax.DefaultConfig(), UserCanOverridePrice: override,
		Payments: []PaymentInput{{Method: "cash", Amount: price}},
		Items:    []SaleItemInput{{ProductID: &pid, Quantity: 1, UnitPrice: price, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true}},
	})
	return err
}

func TestUSDSale_PriceMustBeCatalogPriceConvertedWithRate(t *testing.T) {
	rate := 3.5 // S/ 100 → US$ 28.571428…
	if err := usdSale(t, 100.0/3.5, &rate, false); err != nil {
		t.Errorf("el precio convertido exacto debe pasar: %v", err)
	}
	if err := usdSale(t, 28.57, &rate, false); err != nil {
		t.Errorf("el precio convertido redondeado a centavos debe pasar: %v", err)
	}
	// El defecto: la línea conserva el número en soles y solo cambia la etiqueta a USD.
	err := usdSale(t, 100, &rate, false)
	if err == nil || !strings.Contains(err.Error(), "precio autorizado") || !strings.Contains(err.Error(), "US$") {
		t.Errorf("US$ 100 por un producto de S/ 100 debe rechazarse con el precio en dólares, err=%v", err)
	}
	if err := usdSale(t, 28.60, &rate, false); err == nil {
		t.Error("un precio en dólares distinto del convertido debe rechazarse")
	}
	if err := usdSale(t, 20, &rate, true); err != nil {
		t.Errorf("con sales.override_price se acepta el precio pactado: %v", err)
	}
}

func TestUSDSale_WithoutExchangeRateCannotAuthorizePrice(t *testing.T) {
	err := usdSale(t, 28.57, nil, false)
	if err == nil || !strings.Contains(err.Error(), "tipo de cambio") {
		t.Errorf("sin TC no se puede validar un precio en dólares, err=%v", err)
	}
}

func TestPENSale_PriceValidationUnchanged(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p := database.TenantProduct{Code: "EQ2", Name: "Equipo", Type: "product", Unit: "NIU", SalePrice: 100,
		IgvAffectationType: "10", PriceIncludesIgv: true, BranchID: 1, Active: true}
	db.Create(&p)
	pid := p.ID
	rate := 3.5 // en soles el TC se ignora
	_, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: "00", IssueDate: time.Now(),
		Currency: "PEN", ExchangeRate: &rate, TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: 100}},
		Items:    []SaleItemInput{{ProductID: &pid, Quantity: 1, UnitPrice: 100, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true}},
	})
	if err != nil {
		t.Errorf("venta en soles con el precio de catálogo: %v", err)
	}
}

// Sin sales.override_price el TC editable queda acotado al oficial del día: con TC=10 el producto de S/ 100 saldría a US$ 10
// y pasaría la validación de precio (catálogo ÷ el TC escrito), cobrando en realidad unos S/ 34.
func TestUSDSale_RateFarFromOfficialRejectedWithoutOverride(t *testing.T) {
	old := dailyfx.Lookup
	defer func() { dailyfx.Lookup = old }()
	dailyfx.Lookup = func(day string) (float64, bool, bool) { return 3.5, false, true }

	far := 10.0
	err := usdSale(t, 100.0/far, &far, false)
	if err == nil || !strings.Contains(err.Error(), "tipo de cambio oficial") {
		t.Errorf("TC=10 sin override debe rechazarse, err=%v", err)
	}
	near := 3.55
	if err := usdSale(t, 100.0/near, &near, false); err != nil {
		t.Errorf("TC cercano al oficial debe pasar: %v", err)
	}
	if err := usdSale(t, 100.0/far, &far, true); err != nil {
		t.Errorf("con override el TC no se acota: %v", err)
	}
}
