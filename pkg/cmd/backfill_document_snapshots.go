package cmd

import (
	"flag"
	"fmt"

	quotationsvc "tukifac/internal/quotations/service"
	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// RunBackfillDocumentSnapshots congela la «foto» de impresión (ver database.TenantDocumentSnapshot) de los documentos que
// todavía no la tienen: ventas (nota de venta, boleta, factura, notas de crédito/débito, guías), cotizaciones y compras.
//
// Sin este comando, un documento anterior a la migración V190 recibe su foto la primera vez que se imprime. Correrlo cierra esa
// ventana: desde ese momento, editar el cliente, el proveedor, la empresa o la sucursal ya no cambia ningún PDF existente.
// IMPORTANTE: la foto toma los datos VIGENTES al correrlo (los valores anteriores a una edición ya hecha no se pueden recuperar).
// Es idempotente y reanudable: solo procesa documentos sin foto.
func RunBackfillDocumentSnapshots(args []string) int {
	fs := flag.NewFlagSet("backfill-document-snapshots", flag.ExitOnError)
	slug := fs.String("tenant", "", "solo este tenant (por slug); vacío = todos")
	dryRun := fs.Bool("dry-run", false, "solo contar los documentos sin foto, sin escribir")
	activeOnly := fs.Bool("active-only", false, "omitir tenants no activos")
	batch := fs.Int("batch", 200, "documentos por lote")
	_ = fs.Parse(args)

	tenants, err := database.ListTenantsForMigration(*activeOnly)
	if err != nil {
		fmt.Printf("✗ no se pudo listar tenants: %v\n", err)
		return 1
	}
	if *slug != "" {
		filtered := tenants[:0]
		for _, t := range tenants {
			if t.Slug == *slug {
				filtered = append(filtered, t)
			}
		}
		tenants = filtered
		if len(tenants) == 0 {
			fmt.Printf("✗ tenant %q no encontrado\n", *slug)
			return 1
		}
	}

	mode := "apply"
	if *dryRun {
		mode = "dry-run"
	}
	fmt.Printf("backfill-document-snapshots mode=%s tenants=%d\n", mode, len(tenants))
	var totalSales, totalQuotations, totalPurchases, failed int
	for _, t := range tenants {
		raw, err := database.OpenTenantDBForMigration(t.DBName)
		if err != nil {
			failed++
			fmt.Printf("  ✗ %-24s %v\n", t.Slug, err)
			continue
		}
		db := branchscope.System(raw, "backfill-document-snapshots")
		if !db.Migrator().HasTable(&database.TenantDocumentSnapshot{}) {
			database.CloseTenantDB(raw)
			fmt.Printf("  · %-24s sin tabla de fotos (falta la migración V190), se omite\n", t.Slug)
			continue
		}
		s, q, p, perr := backfillTenantSnapshots(db, *dryRun, *batch)
		database.CloseTenantDB(raw)
		if perr != nil {
			failed++
			fmt.Printf("  ✗ %-24s %v\n", t.Slug, perr)
			continue
		}
		totalSales += s
		totalQuotations += q
		totalPurchases += p
		if s+q+p > 0 {
			fmt.Printf("  · %-24s ventas=%d cotizaciones=%d compras=%d\n", t.Slug, s, q, p)
		}
	}
	fmt.Printf("\nventas=%d cotizaciones=%d compras=%d errores=%d\n", totalSales, totalQuotations, totalPurchases, failed)
	if *dryRun {
		fmt.Println("(dry-run: no se escribió nada)")
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// backfillTenantSnapshots procesa un tenant y devuelve cuántos documentos de cada tipo tenían (o recibieron) su foto.
// Pagina por id (no por «los que faltan») para que un documento que no se pueda armar no se reintente en bucle.
func backfillTenantSnapshots(db *gorm.DB, dryRun bool, batch int) (sales, quotations, purchases int, err error) {
	if batch <= 0 {
		batch = 200
	}
	missing := func(table, kind string) *gorm.DB {
		return db.Table(table).
			Where("NOT EXISTS (SELECT 1 FROM tenant_document_snapshots s WHERE s.doc_kind = ? AND s.doc_id = "+table+".id)", kind)
	}
	// each recorre los ids sin foto de una tabla, en lotes crecientes; en dry-run solo cuenta.
	each := func(table, kind string, handle func(id uint)) (n int, err error) {
		if dryRun {
			var c int64
			err = missing(table, kind).Count(&c).Error
			return int(c), err
		}
		var last uint
		for {
			var ids []uint
			if err = missing(table, kind).Where(table+".id > ?", last).Order(table + ".id ASC").Limit(batch).Pluck(table+".id", &ids).Error; err != nil {
				return
			}
			if len(ids) == 0 {
				return
			}
			for _, id := range ids {
				handle(id)
				n++
				last = id
			}
		}
	}
	// Ventas: BuildPrintDataForSale congela la foto como parte de armar el print_data.
	if sales, err = each("tenant_sales", database.SnapshotKindSale, func(id uint) { _, _ = salessvc.BuildPrintDataForSale(db, id) }); err != nil {
		return
	}
	if quotations, err = each("tenant_quotations", database.SnapshotKindQuotation, func(id uint) { _, _ = quotationsvc.BuildPrintDataForQuotation(db, id) }); err != nil {
		return
	}
	purchases, err = each("tenant_purchases", database.SnapshotKindPurchase, func(id uint) {
		var p database.TenantPurchase
		if db.Unscoped().First(&p, id).Error == nil {
			salessvc.EnsurePurchaseSnapshot(db, &p)
		}
	})
	return
}
