package service

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Escenario del reporte de ventas frente al dashboard: una nota de venta convertida a boleta, una
// boleta directa, una NV sin convertir, una factura directa y una nota de débito.
//
// Lo que cuenta el dashboard (CommercialSalesNoNotes, tipo efectivo vía el hijo):
//
//	BOLETA 150 (NV1 convertida 100 + boleta directa 50) · NOTA_VENTA 30 · FACTURA 200 → total 380.
type commercialReportFixture struct {
	db                              *gorm.DB
	nv1, boletaHijo, boletaDirecta  uint
	nv2, facturaDirecta, notaDebito uint
}

func setupCommercialReportDB(t *testing.T) *commercialReportFixture {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantSale{}, &database.TenantSalePayment{}, &database.TenantSaleDetraccion{},
		&database.TenantDocumentSeries{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	mkSeries := func(code, series, docType string) uint {
		s := database.TenantDocumentSeries{BranchID: 1, SunatCode: code, Series: series, DocType: docType, Active: true, Correlative: 1}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	nvS, bolS, facS, ndS := mkSeries("00", "NV01", "NOTA DE VENTA"), mkSeries("03", "B001", "BOLETA"),
		mkSeries("01", "F001", "FACTURA"), mkSeries("08", "BD01", "NOTA_DEBITO")

	now := time.Now()
	mk := func(seriesID uint, docType, number string, total float64, origin string, parent *uint) uint {
		s := database.TenantSale{
			SeriesID: seriesID, DocType: docType, Number: number, BranchID: 1, UserID: 1,
			IssueDate: now, Subtotal: total, Total: total, Status: "paid", PaymentMethod: "cash",
			SaleOrigin: origin, IssuedFromNotaSaleID: parent,
		}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	f := &commercialReportFixture{db: db}
	f.nv1 = mk(nvS, "NOTA_VENTA", "NV01-1", 100, "direct", nil)
	nv1 := f.nv1
	f.boletaHijo = mk(bolS, "BOLETA", "B001-1", 100, "converted_from_nota", &nv1)
	f.boletaDirecta = mk(bolS, "BOLETA", "B001-2", 50, "direct", nil)
	f.nv2 = mk(nvS, "NOTA_VENTA", "NV01-2", 30, "direct", nil)
	f.facturaDirecta = mk(facS, "FACTURA", "F001-1", 200, "direct", nil)
	f.notaDebito = mk(ndS, "NOTA_DEBITO", "BD01-1", 20, "direct", nil)
	return f
}

func idsOf(sales []database.TenantSale) []uint {
	out := make([]uint, 0, len(sales))
	for _, s := range sales {
		out = append(out, s.ID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sameIDs(got []uint, want ...uint) bool {
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// El total del reporte sin filtros debe ser el del dashboard: 380. La nota de débito ya no suma
// y los conteos excluyen notas.
func TestSaleList_CommercialReport_TotalCoincideConDashboard(t *testing.T) {
	f := setupCommercialReportDB(t)
	_, _, sum, err := NewSaleService(f.db).List(SaleListParams{CommercialReport: true})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SumActive != 380 {
		t.Errorf("sum_active = %.2f, want 380 (igual que el dashboard)", sum.SumActive)
	}
	if sum.CountActive != 4 {
		t.Errorf("count_active = %d, want 4 (la nota de débito no es una operación de venta)", sum.CountActive)
	}
}

// Regla de fecha de emisión: filtrar por Boleta incluye el COMPROBANTE hijo (con su fecha) y NO la NV de la que salió: 100 + 50 = 150.
func TestSaleList_CommercialReport_BoletaIncluyeNVConvertidaSinDuplicar(t *testing.T) {
	f := setupCommercialReportDB(t)
	rows, _, sum, err := NewSaleService(f.db).List(SaleListParams{CommercialReport: true, SunatCodes: []string{"03"}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SumActive != 150 {
		t.Errorf("total Boleta = %.2f, want 150", sum.SumActive)
	}
	if got := idsOf(rows); !sameIDs(got, f.boletaHijo, f.boletaDirecta) {
		t.Errorf("filas Boleta = %v, want comprobante hijo (%d) y boleta directa (%d), sin la NV (%d)",
			got, f.boletaHijo, f.boletaDirecta, f.nv1)
	}
}

// Filtrar por Nota de venta (00) no debe traer la NV que ya se convirtió: dejó de ser nota de
// venta para el dashboard (cuenta como boleta).
func TestSaleList_CommercialReport_NotaVentaExcluyeLaConvertida(t *testing.T) {
	f := setupCommercialReportDB(t)
	rows, _, sum, err := NewSaleService(f.db).List(SaleListParams{CommercialReport: true, SunatCodes: []string{"00"}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SumActive != 30 || !sameIDs(idsOf(rows), f.nv2) {
		t.Errorf("notas de venta = %v por %.2f, want solo la no convertida (%d) por 30", idsOf(rows), sum.SumActive, f.nv2)
	}
}

func TestSaleList_CommercialReport_FacturaYSumaDeTipos(t *testing.T) {
	f := setupCommercialReportDB(t)
	svc := NewSaleService(f.db)
	var total float64
	for _, code := range []string{"01", "03", "00"} {
		_, _, sum, err := svc.List(SaleListParams{CommercialReport: true, SunatCodes: []string{code}})
		if err != nil {
			t.Fatal(err)
		}
		total += sum.SumActive
	}
	// Los tres tipos reparten el total sin perder ni duplicar nada.
	if total != 380 {
		t.Errorf("suma de Factura+Boleta+NV = %.2f, want 380", total)
	}
}

// Sin el flag, las pantallas de ventas y facturación conservan el comportamiento de siempre:
// el comprobante hijo sigue apareciendo como fila (ahí se necesita su estado SUNAT).
func TestSaleList_SinReporte_ConservaElComprobanteHijo(t *testing.T) {
	f := setupCommercialReportDB(t)
	rows, _, _, err := NewSaleService(f.db).List(SaleListParams{SunatCodes: []string{"03"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(rows); !sameIDs(got, f.boletaHijo, f.boletaDirecta) {
		t.Errorf("filas = %v, want las dos boletas (hijo %d y directa %d)", got, f.boletaHijo, f.boletaDirecta)
	}
}

// Listado crudo (Facturación de Tukichef) filtrado por tipo 01,03: las filas incluyen el
// comprobante hijo de la conversión, así que las tarjetas deben contarlo (100 + 50 = 150) y
// coincidir con la suma de las filas, en vez de dejarlo fuera.
func TestSaleList_RawBoletaListing_TotalCoincideConLasFilas(t *testing.T) {
	f := setupCommercialReportDB(t)
	rows, _, sum, err := NewSaleService(f.db).List(SaleListParams{SunatCodes: []string{"03"}})
	if err != nil {
		t.Fatal(err)
	}
	var rowsTotal float64
	for _, r := range rows {
		rowsTotal += r.Total
	}
	if rowsTotal != 150 || sum.SumActive != rowsTotal {
		t.Errorf("filas = %.2f, sum_active = %.2f; ambos deben ser 150", rowsTotal, sum.SumActive)
	}
	if sum.CountActive != int64(len(rows)) {
		t.Errorf("count_active = %d, filas = %d", sum.CountActive, len(rows))
	}
}
