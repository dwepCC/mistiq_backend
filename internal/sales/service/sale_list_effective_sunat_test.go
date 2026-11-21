package service

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// legacyEffectiveSunatCodeSQL es la subconsulta correlacionada que usaba List antes de
// joinEffectiveSunatCode (O(n²) en MySQL). Se conserva SOLO aquí como oráculo: el JOIN nuevo debe
// devolver exactamente las mismas ventas.
const legacyEffectiveSunatCodeSQL = "COALESCE(" +
	"(SELECT fds.sunat_code FROM tenant_sales fe JOIN tenant_document_series fds ON fds.id = fe.series_id " +
	"WHERE fe.issued_from_nota_sale_id = tenant_sales.id AND fe.deleted_at IS NULL ORDER BY fe.id LIMIT 1), " +
	"(SELECT ds.sunat_code FROM tenant_document_series ds WHERE ds.id = tenant_sales.series_id))"

func TestEffectiveSunatCode_JoinEquivaleASubconsultaCorrelacionada(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantDocumentSeries{}); err != nil {
		t.Fatal(err)
	}
	codes := []string{"00", "01", "03", "07", "08"}
	var seriesIDs []uint
	for i, c := range codes {
		s := database.TenantDocumentSeries{BranchID: 1, SunatCode: c, Series: fmt.Sprintf("S%02d", i), DocType: "X", Active: true, Correlative: 1}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		seriesIDs = append(seriesIDs, s.ID)
	}
	// Serie sin sunat_code: un hijo con esa serie no debe tapar el código propio del padre.
	empty := database.TenantDocumentSeries{BranchID: 1, SunatCode: "", Series: "SXX", DocType: "X", Active: true, Correlative: 1}
	if err := db.Create(&empty).Error; err != nil {
		t.Fatal(err)
	}
	seriesIDs = append(seriesIDs, empty.ID)

	rng := rand.New(rand.NewSource(7))
	now := time.Now()
	var parents []uint
	mk := func(parent *uint, origin string) uint {
		s := database.TenantSale{
			SeriesID: seriesIDs[rng.Intn(len(seriesIDs))], DocType: "NOTA_VENTA", Number: "N", BranchID: 1, UserID: 1,
			IssueDate: now, Subtotal: 10, Total: 10, Status: "paid", PaymentMethod: "cash",
			SaleOrigin: origin, IssuedFromNotaSaleID: parent,
		}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	for i := 0; i < 60; i++ {
		parents = append(parents, mk(nil, "direct"))
	}
	// Hijos: algunos padres con 0, 1 o 2 hijos (gana el de menor id) y algunos hijos borrados.
	for _, p := range parents {
		p := p
		switch rng.Intn(4) {
		case 1:
			mk(&p, "converted_from_nota")
		case 2:
			a := mk(&p, "converted_from_nota")
			mk(&p, "converted_from_nota")
			if rng.Intn(2) == 0 {
				db.Delete(&database.TenantSale{}, a) // el primero borrado: debe valer el segundo
			}
		}
	}

	for _, want := range [][]string{{"00"}, {"01", "03"}, {"03"}, {"07", "08"}, {"00", "01", "03", "07", "08"}} {
		var legacy, joined []uint
		if err := db.Model(&database.TenantSale{}).Where(legacyEffectiveSunatCodeSQL+" IN ?", want).Pluck("tenant_sales.id", &legacy).Error; err != nil {
			t.Fatal(err)
		}
		q := joinEffectiveSunatCode(db.Model(&database.TenantSale{}))
		if err := q.Where(effectiveSunatCodeExpr+" IN ?", want).Pluck("tenant_sales.id", &joined).Error; err != nil {
			t.Fatal(err)
		}
		sort.Slice(legacy, func(i, j int) bool { return legacy[i] < legacy[j] })
		sort.Slice(joined, func(i, j int) bool { return joined[i] < joined[j] })
		if len(legacy) != len(joined) {
			t.Fatalf("codes %v: legacy=%d filas, join=%d filas", want, len(legacy), len(joined))
		}
		for i := range legacy {
			if legacy[i] != joined[i] {
				t.Fatalf("codes %v: difieren en la posición %d (legacy=%d join=%d)", want, i, legacy[i], joined[i])
			}
		}
		if len(joined) == 0 && len(want) > 1 {
			t.Fatalf("codes %v: el escenario debería devolver filas", want)
		}
	}
}

// SkipSummary no calcula los totales pero sí devuelve las filas y el total paginado.
func TestSaleList_SkipSummary(t *testing.T) {
	f := setupCommercialReportDB(t)
	svc := NewSaleService(f.db)

	rows, total, sum, err := svc.List(SaleListParams{Limit: 10, SkipSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || total == 0 {
		t.Fatalf("debe devolver filas y total paginado, got rows=%d total=%d", len(rows), total)
	}
	if sum.SumTotal != 0 || sum.CountActive != 0 || len(sum.PaymentTotals) != 0 {
		t.Fatalf("SkipSummary no debe calcular totales: %+v", sum)
	}

	_, _, full, err := svc.List(SaleListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if full.SumTotal == 0 {
		t.Fatal("sin SkipSummary los totales deben calcularse")
	}
}
