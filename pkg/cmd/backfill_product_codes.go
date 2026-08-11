package cmd

import (
	"flag"
	"fmt"

	"tukifac/pkg/database"
	"tukifac/pkg/database/engine"
	"tukifac/pkg/database/tenantbackfills"
)

// RunBackfillProductCodes revisa (o aplica) el backfill de códigos de producto.
//
// La corrección vive en tenantbackfills.V034ProductCodes, que también corre desde el panel
// central y desde el cron. Aquí solo se añade lo que esos dos no dan: un informe previo de
// cuánto se va a tocar, útil antes de escribir sobre datos de producción.
func RunBackfillProductCodes(args []string) int {
	fs := flag.NewFlagSet("backfill-product-codes", flag.ExitOnError)
	slug := fs.String("tenant", "", "solo este tenant (por slug); vacío = todos")
	dryRun := fs.Bool("dry-run", false, "solo informar, sin escribir")
	activeOnly := fs.Bool("active-only", false, "omitir tenants no activos")
	_ = fs.Parse(args)

	if !*dryRun {
		summary := engine.RunBackfillFleet(engine.BackfillOptions{
			Version:    tenantbackfills.V034ProductCodes{}.Version(),
			TenantSlug: *slug,
			ActiveOnly: *activeOnly,
		})
		fmt.Printf("backfill-product-codes ok=%d error=%d\n", len(summary.Success), len(summary.Failed))
		for _, f := range summary.Failed {
			fmt.Printf("  ✗ %-24s %v\n", f.Slug, f.Err)
		}
		if len(summary.Failed) > 0 {
			return 1
		}
		return 0
	}

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

	fmt.Printf("backfill-product-codes mode=dry-run tenants=%d\n", len(tenants))
	var totalProducts, totalItems, totalOrphans, failed int
	for _, t := range tenants {
		products, items, orphans, err := auditTenantCodes(t.DBName)
		if err != nil {
			failed++
			fmt.Printf("  ✗ %-24s %v\n", t.Slug, err)
			continue
		}
		totalProducts += products
		totalItems += items
		totalOrphans += orphans
		if products == 0 && items == 0 {
			continue // nada que corregir: no ensuciar la salida
		}
		fmt.Printf("  · %-24s productos=%d líneas=%d (de ellas sin producto=%d)\n",
			t.Slug, products, items, orphans)
	}

	fmt.Printf("\nproductos a corregir=%d líneas a corregir=%d (sin producto del catálogo=%d)\n",
		totalProducts, totalItems, totalOrphans)
	fmt.Println("(dry-run: no se escribió nada)")
	if failed > 0 {
		return 1
	}
	return 0
}

// auditTenantCodes cuenta lo que el backfill corregiría en un tenant, sin escribir.
func auditTenantCodes(dbName string) (products, items, orphans int, err error) {
	db, err := database.OpenTenantDBForMigration(dbName)
	if err != nil {
		return 0, 0, 0, err
	}
	defer database.CloseTenantDB(db)

	if !db.Migrator().HasTable("tenant_products") {
		return 0, 0, 0, nil
	}
	var n int64
	if err := db.Table("tenant_products").
		Where("code IS NULL OR TRIM(code) = ''").
		Count(&n).Error; err != nil {
		return 0, 0, 0, fmt.Errorf("contar productos: %w", err)
	}
	products = int(n)

	if !db.Migrator().HasTable("tenant_sale_items") {
		return products, 0, 0, nil
	}
	// El backfill no deja ninguna línea sin código, así que todas estas se corrigen.
	if err := db.Table("tenant_sale_items").
		Where("code IS NULL OR TRIM(code) = ''").
		Count(&n).Error; err != nil {
		return 0, 0, 0, fmt.Errorf("contar líneas: %w", err)
	}
	items = int(n)

	// Subconjunto informativo: las que reciben un código propio por no tener producto del cual
	// copiarlo. No cambia qué se corrige, solo de dónde sale el código.
	if err := db.Table("tenant_sale_items AS si").
		Where("si.code IS NULL OR TRIM(si.code) = ''").
		Where("si.product_id IS NULL OR NOT EXISTS (SELECT 1 FROM tenant_products p WHERE p.id = si.product_id)").
		Count(&n).Error; err != nil {
		return 0, 0, 0, fmt.Errorf("contar líneas sin producto: %w", err)
	}
	orphans = int(n)

	return products, items, orphans, nil
}
