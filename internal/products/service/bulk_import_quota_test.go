package service

import (
	"testing"

	"tukifac/pkg/database"
)

func quotaItems(n int, start int) []BulkImportItem {
	out := make([]BulkImportItem, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, BulkImportItem{RowNumber: start + i + 2, Name: "Producto " + string(rune('A'+start+i)), Code: "COD" + string(rune('A'+start+i)), SalePrice: 10})
	}
	return out
}

func countLiveProducts(t *testing.T, svc *ProductService) int64 {
	t.Helper()
	var n int64
	svc.db.Model(&database.TenantProduct{}).Count(&n)
	return n
}

// Con cupo para 3 productos nuevos, importar 5 crea solo 3 y los otros 2 se informan como omitidos por el plan (no como errores).
func TestBulkImport_PlanQuotaImportsOnlyWhatFits(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	svc.SetImportQuota(NewProductQuota(10, 7)) // tope 10, ya hay 7 → caben 3

	res, err := svc.BulkImportCatalog(quotaItems(5, 0), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 3 || res.LimitSkipped != 2 {
		t.Fatalf("created=%d skipped=%d, want 3 y 2", res.Created, res.LimitSkipped)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("lo omitido por el plan no debe contarse como error de fila: %+v", res.Failed)
	}
	if res.PlanLimit == nil || res.PlanLimit.Max != 10 || res.PlanLimit.Used != 7 || res.PlanLimit.AllowedNew != 3 {
		t.Fatalf("plan_limit inesperado: %+v", res.PlanLimit)
	}
	if got := countLiveProducts(t, svc); got != 3 {
		t.Fatalf("productos en BD: %d, want 3", got)
	}
}

// Actualizar un producto que ya existe no consume cupo; un servicio nuevo tampoco.
func TestBulkImport_PlanQuotaDoesNotCountUpdatesNorServices(t *testing.T) {
	db := setupProductServiceTestDB(t)
	seed := NewProductService(db)
	if _, err := seed.BulkImportCatalog(quotaItems(2, 0), 0, 1); err != nil {
		t.Fatal(err)
	}

	svc := NewProductService(db)
	svc.SetImportQuota(NewProductQuota(2, 2)) // sin cupo
	items := quotaItems(2, 0)                 // los mismos 2 códigos: son actualizaciones
	items[0].SalePrice = 99
	items = append(items, BulkImportItem{RowNumber: 10, Name: "Servicio nuevo", Code: "SERV1", SalePrice: 5, CatalogType: "service"})
	items = append(items, BulkImportItem{RowNumber: 11, Name: "Bien nuevo", Code: "BIEN1", SalePrice: 5})

	res, err := svc.BulkImportCatalog(items, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 2 || res.Created != 1 || res.LimitSkipped != 1 {
		t.Fatalf("updated=%d created=%d skipped=%d, want 2, 1 (el servicio), 1 (el bien nuevo)", res.Updated, res.Created, res.LimitSkipped)
	}
}

// Sin tope en el plan (nil) se importa todo, como siempre.
func TestBulkImport_NoPlanQuotaImportsEverything(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	svc.SetImportQuota(NewProductQuota(0, 500)) // 0 = ilimitado
	res, err := svc.BulkImportCatalog(quotaItems(5, 0), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 5 || res.LimitSkipped != 0 || res.PlanLimit != nil {
		t.Fatalf("created=%d skipped=%d planLimit=%v", res.Created, res.LimitSkipped, res.PlanLimit)
	}
}

func TestNewProductQuota(t *testing.T) {
	if NewProductQuota(0, 5) != nil || NewProductQuota(-1, 5) != nil {
		t.Fatal("tope <= 0 debe ser sin límite (nil)")
	}
	if q := NewProductQuota(10, 15); q == nil || q.Remaining != 0 {
		t.Fatalf("ya excedido: cupo debe ser 0, got %+v", q)
	}
	if q := NewProductQuota(10, 4); q.Remaining != 6 {
		t.Fatalf("remaining: %+v", q)
	}
}
