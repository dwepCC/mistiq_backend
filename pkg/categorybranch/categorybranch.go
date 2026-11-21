// Package categorybranch resuelve en qué sucursales se muestra una categoría (tenant_categories.branch_id, V192).
//
// Regla única, usada por los listados, el formulario de producto, la tienda virtual y la importación:
//   - branch_id NULL: la categoría es de TODAS las sucursales (lo de siempre; toda categoría anterior a V192 queda así).
//   - branch_id = N: solo de la sucursal N.
//
// Un producto con categoría de sucursal solo puede venderse en esa sucursal (ver CheckProductCompatible): así la categoría
// nunca queda oculta justo donde el producto se vende.
package categorybranch

import (
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

const columnProbe = "col:tenant_categories.branch_id"

// ErrNotMigrated: la BD del tenant todavía no tiene la columna (migración V192 pendiente).
var ErrNotMigrated = errors.New("categorías por sucursal no disponible: falta aplicar la migración V192 en este tenant")

// Ready: true si la BD tiene tenant_categories.branch_id. Sin la columna (tenant aún sin migrar) todo el paquete se comporta
// como "todas las categorías en todas las sucursales" — igual que antes — en vez de romper listados y altas.
func Ready(db *gorm.DB) bool {
	return database.CachedSchemaProbe(db, columnProbe, func() bool {
		return db.Migrator().HasColumn(&database.TenantCategory{}, "branch_id")
	})
}

// VisibleIn restringe una consulta de categorías a las visibles en la sucursal: las de todas (NULL) y las de esa sucursal.
// branchID 0 = sin filtro (vista de administración completa). Sin la migración V192 no filtra.
func VisibleIn(db *gorm.DB, branchID uint) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB {
		if branchID == 0 || !Ready(db) {
			return q
		}
		return q.Where("(tenant_categories.branch_id IS NULL OR tenant_categories.branch_id = ?)", branchID)
	}
}

// VisibleInAny: igual, para un usuario con varias sucursales asignadas (las de todas y las de cualquiera de ellas).
func VisibleInAny(db *gorm.DB, branchIDs []uint) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB {
		if len(branchIDs) == 0 || !Ready(db) {
			return q
		}
		return q.Where("(tenant_categories.branch_id IS NULL OR tenant_categories.branch_id IN ?)", branchIDs)
	}
}

func branchName(db *gorm.DB, id uint) string {
	var name string
	if err := db.Model(&database.TenantBranch{}).Select("name").Where("id = ?", id).Limit(1).Scan(&name).Error; err != nil || strings.TrimSpace(name) == "" {
		return fmt.Sprintf("#%d", id)
	}
	return name
}

// ValidateBranch comprueba que la sucursal exista (nil = todas, siempre válido).
func ValidateBranch(db *gorm.DB, branchID *uint) error {
	if branchID == nil {
		return nil
	}
	if *branchID == 0 {
		return errors.New("sucursal inválida")
	}
	var n int64
	if err := db.Model(&database.TenantBranch{}).Where("id = ?", *branchID).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return errors.New("la sucursal indicada no existe")
	}
	return nil
}

func sameScope(a, b *uint) bool {
	if a == nil || b == nil {
		return true // «todas» choca con cualquier sucursal: el mismo nombre en ambas confundiría
	}
	return *a == *b
}

// EnsureNameFree rechaza un nombre ya usado en el mismo ámbito: dentro de la misma sucursal, o entre una categoría de todas y
// una de sucursal. El mismo nombre en sucursales DISTINTAS sí se permite: es justo lo que se busca («Bebidas» de cada local).
// excludeID: la propia categoría al editarla. Sin la migración V192 el ámbito no existe y basta con que el nombre sea único.
func EnsureNameFree(db *gorm.DB, name string, branchID *uint, excludeID uint) error {
	name = strings.TrimSpace(name)
	if name == "" || !Ready(db) {
		return nil // sin V192 no hay ámbito: se conserva el comportamiento anterior (nombre libre)
	}
	var cats []database.TenantCategory
	q := db.Where("LOWER(name) = ?", strings.ToLower(name))
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	if err := q.Find(&cats).Error; err != nil {
		return err
	}
	for _, c := range cats {
		if sameScope(c.BranchID, branchID) {
			where := "todas las sucursales"
			if c.BranchID != nil {
				where = "la sucursal «" + branchName(db, *c.BranchID) + "»"
			}
			return fmt.Errorf("ya existe una categoría «%s» en %s", c.Name, where)
		}
	}
	return nil
}

// EffectiveBranches sucursales donde se vende un producto (vacío = todas): las de tenant_product_branches y, si no tiene
// ninguna, su sucursal dueña legacy (TenantProduct.BranchID — los platos de la carta de Tukichef son de UNA sucursal aunque no
// tengan filas en esa tabla; ver pkg/productbranch).
func EffectiveBranches(db *gorm.DB, productID uint) ([]uint, error) {
	var ids []uint
	if err := db.Model(&database.TenantProductBranch{}).Where("product_id = ?", productID).Pluck("branch_id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		return ids, nil
	}
	var owner uint
	if err := db.Model(&database.TenantProduct{}).Where("id = ?", productID).Select("branch_id").Scan(&owner).Error; err != nil {
		return nil, err
	}
	if owner > 0 {
		return []uint{owner}, nil
	}
	return nil, nil
}

// CheckProductCompatible: una categoría de sucursal solo admite productos que se vendan únicamente en esa sucursal. Un producto
// disponible en todas (sin asignación) o también en otra sucursal no puede usarla: en esa otra sucursal quedaría sin categoría.
// productBranchIDs = sucursales finales del producto (vacío = todas). categoryID nil/0 o categoría de todas: siempre válido.
func CheckProductCompatible(db *gorm.DB, categoryID *uint, productBranchIDs []uint) error {
	if categoryID == nil || *categoryID == 0 || !Ready(db) {
		return nil
	}
	var cat database.TenantCategory
	if err := db.Select("id", "name", "branch_id").First(&cat, *categoryID).Error; err != nil {
		return nil // categoría inexistente: otra validación se encarga
	}
	if cat.BranchID == nil {
		return nil
	}
	x := *cat.BranchID
	name := branchName(db, x)
	if len(productBranchIDs) == 0 {
		return fmt.Errorf("la categoría «%s» es exclusiva de la sucursal «%s»: el producto debe venderse solo en esa sucursal (campo «Disponible en») o usar una categoría de todas las sucursales", cat.Name, name)
	}
	for _, id := range productBranchIDs {
		if id != x {
			return fmt.Errorf("la categoría «%s» es exclusiva de la sucursal «%s»: el producto también se vende en otra sucursal. Déjelo solo en «%s» o use una categoría de todas las sucursales", cat.Name, name, name)
		}
	}
	return nil
}

// CheckScopeChange valida pasar una categoría a otro ámbito: cada producto que la usa debe seguir siendo compatible. Ampliar a
// «todas» (newBranch nil) siempre se permite. Devuelve cuántos productos lo impiden, con un mensaje accionable.
func CheckScopeChange(db *gorm.DB, categoryID uint, newBranch *uint) error {
	if newBranch == nil || !Ready(db) {
		return nil
	}
	var products []database.TenantProduct
	if err := db.Select("id", "name").Where("category_id = ?", categoryID).Find(&products).Error; err != nil {
		return err
	}
	blocking := 0
	example := ""
	for _, p := range products {
		ids, err := EffectiveBranches(db, p.ID)
		if err != nil {
			return err
		}
		ok := len(ids) > 0
		for _, id := range ids {
			if id != *newBranch {
				ok = false
			}
		}
		if !ok {
			blocking++
			if example == "" {
				example = p.Name
			}
		}
	}
	if blocking > 0 {
		return fmt.Errorf(
			"no se puede limitar a la sucursal «%s»: %d producto(s) que usan esta categoría se venden en otras sucursales o en todas (p. ej. «%s»). Asígneles solo esa sucursal o cámbieles la categoría primero",
			branchName(db, *newBranch), blocking, example,
		)
	}
	return nil
}

// WidenForBranch amplía a «todas» la categoría de sucursal de un producto cuando entra stock del producto a OTRA sucursal
// (compra, transferencia, ajuste): si no, ese producto quedaría sin categoría visible en la sucursal que lo recibió. Mismo
// espíritu que productbranch.AssignIfRestricted. No hace nada si la categoría ya es de todas o es de esa misma sucursal.
func WidenForBranch(db *gorm.DB, productID, branchID uint) error {
	if productID == 0 || branchID == 0 || !Ready(db) {
		return nil
	}
	return db.Exec(
		`UPDATE tenant_categories SET branch_id = NULL
		 WHERE branch_id IS NOT NULL AND branch_id <> ?
		   AND id = (SELECT category_id FROM tenant_products WHERE id = ?)`,
		branchID, productID,
	).Error
}

// FindByName busca una categoría por nombre visible desde la sucursal: primero la de esa sucursal, después la de todas. Para
// la importación masiva y cualquier búsqueda por nombre. branchID 0 = solo las de todas. ok=false si no hay ninguna visible.
func FindByName(db *gorm.DB, name string, branchID uint) (cat database.TenantCategory, ok bool, err error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return cat, false, nil
	}
	if !Ready(db) {
		e := db.Where("LOWER(name) = ?", key).First(&cat).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return cat, false, nil
		}
		return cat, e == nil, e
	}
	if branchID > 0 {
		e := db.Where("LOWER(name) = ? AND branch_id = ?", key, branchID).First(&cat).Error
		if e == nil {
			return cat, true, nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return cat, false, e
		}
	}
	e := db.Where("LOWER(name) = ? AND branch_id IS NULL", key).First(&cat).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return cat, false, nil
	}
	return cat, e == nil, e
}
