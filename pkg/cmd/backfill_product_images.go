package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/imageprocess"
	"tukifac/pkg/tenantstorage"
)

// RunBackfillProductImages reprocesa (reduce peso/dimensiones) las imágenes de producto que YA
// están en disco, subidas antes de que UploadImageAPI empezara a optimizar al vuelo (ver
// internal/products/handler/product_handler.go). Puramente sobre el filesystem: no toca la BD —
// el nombre/extensión del archivo no cambia (JPEG sigue JPEG, PNG sigue PNG), así que image_url
// sigue apuntando al mismo lugar.
//
// A diferencia de los demás backfill-* de este archivo, este NO usa tenantbackfills/engine
// (esos corrigen filas de BD tenant por tenant); acá basta recorrer uploads/tenants/{ruc}/
// products/ de cada tenant listado en la BD central.
func RunBackfillProductImages(args []string) int {
	fs := flag.NewFlagSet("backfill-product-images", flag.ExitOnError)
	slug := fs.String("tenant", "", "solo este tenant (por slug); vacío = todos")
	dryRun := fs.Bool("dry-run", false, "solo informar, sin escribir")
	_ = fs.Parse(args)

	tenants, err := database.ListTenantsForMigration(false)
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

	mode := "aplicando"
	if *dryRun {
		mode = "dry-run"
	}
	fmt.Printf("backfill-product-images mode=%s tenants=%d\n", mode, len(tenants))

	var totalFiles, totalOptimized int
	var totalBytesBefore, totalBytesAfter int64
	var failed int
	for _, t := range tenants {
		ruc := tenantstorage.SanitizeRUC(t.RUC)
		if ruc == "" {
			continue
		}
		dir := tenantstorage.TenantUploadDir(ruc, "products")
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // tenant sin ninguna imagen de producto subida — nada que hacer
			}
			failed++
			fmt.Printf("  ✗ %-24s no se pudo leer %s: %v\n", t.Slug, dir, err)
			continue
		}

		var files, optimized int
		var bytesBefore, bytesAfter int64
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
				continue // .webp u otro: Optimize no lo procesa, se deja tal cual (igual que en el upload)
			}
			full := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(full)
			if err != nil {
				failed++
				fmt.Printf("  ✗ %-24s %s: %v\n", t.Slug, e.Name(), err)
				continue
			}
			files++
			bytesBefore += int64(len(data))
			result, ok := imageprocess.Optimize(data, ext)
			if !ok {
				bytesAfter += int64(len(data)) // se queda como está — cuenta igual para el total
				continue
			}
			optimized++
			bytesAfter += int64(len(result.Data))
			if !*dryRun {
				if err := os.WriteFile(full, result.Data, 0644); err != nil {
					failed++
					fmt.Printf("  ✗ %-24s %s: no se pudo escribir: %v\n", t.Slug, e.Name(), err)
				}
			}
		}
		totalFiles += files
		totalOptimized += optimized
		totalBytesBefore += bytesBefore
		totalBytesAfter += bytesAfter
		if files == 0 {
			continue
		}
		fmt.Printf("  · %-24s archivos=%d optimizados=%d %s → %s\n",
			t.Slug, files, optimized, humanBytes(bytesBefore), humanBytes(bytesAfter))
	}

	savedBytes := totalBytesBefore - totalBytesAfter
	fmt.Printf("\ntotal archivos=%d optimizados=%d ahorro=%s (%s → %s)\n",
		totalFiles, totalOptimized, humanBytes(savedBytes), humanBytes(totalBytesBefore), humanBytes(totalBytesAfter))
	if *dryRun {
		fmt.Println("(dry-run: no se escribió nada)")
	}
	if failed > 0 {
		fmt.Printf("%d error(es)\n", failed)
		return 1
	}
	return 0
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
