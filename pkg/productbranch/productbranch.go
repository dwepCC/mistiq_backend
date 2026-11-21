// Package productbranch resuelve en qué sucursales se vende un producto (tenant_product_branches).
//
// Regla única, usada por el listado, la validación de la venta y las entradas de stock:
//   - Producto SIN filas en tenant_product_branches: disponible en todas las sucursales (lo de
//     siempre; para listados filtrados por sucursal sigue aplicando la regla previa de stock, ver
//     ListFilterSQL).
//   - Producto CON filas: solo en esas sucursales, controle stock o no.
package productbranch

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

const tableName = "tenant_product_branches"

// ErrNotMigrated: la BD del tenant todavía no tiene la tabla (migración v162 pendiente).
var ErrNotMigrated = errors.New("asignación de sucursales no disponible: falta aplicar la migración v162 en este tenant")

// readyPools: pools de conexión cuya BD ya tiene la tabla. Solo se cachea el resultado positivo
// (una vez creada la tabla no desaparece); mientras falte se vuelve a preguntar.
var readyPools sync.Map

// Ready: true si la BD tiene tenant_product_branches. Sin la tabla (tenant aún sin migrar a v162)
// todo el paquete se comporta como "sin asignación" — igual que antes de esta función — en vez de
// romper ventas, compras, inventario y el listado de productos con "no such table".
func Ready(db *gorm.DB) bool {
	sqlDB, err := db.DB()
	if err == nil {
		if _, ok := readyPools.Load(sqlDB); ok {
			return true
		}
	}
	if !db.Migrator().HasTable(tableName) {
		return false
	}
	if err == nil {
		readyPools.Store(sqlDB, struct{}{})
	}
	return true
}

// BranchIDs devuelve las sucursales asignadas a cada producto. Un producto ausente del mapa (o con
// lista vacía) está disponible en todas.
func BranchIDs(db *gorm.DB, productIDs []uint) (map[uint][]uint, error) {
	out := make(map[uint][]uint, len(productIDs))
	if len(productIDs) == 0 || !Ready(db) {
		return out, nil
	}
	var rows []database.TenantProductBranch
	if err := db.Where("product_id IN ?", productIDs).Order("branch_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ProductID] = append(out[r.ProductID], r.BranchID)
	}
	return out, nil
}

// IsAvailable: true si el producto se vende en branchID (sin asignación = todas).
func IsAvailable(db *gorm.DB, productID, branchID uint) (bool, error) {
	if productID == 0 || branchID == 0 || !Ready(db) {
		return true, nil
	}
	var total, match int64
	if err := db.Model(&database.TenantProductBranch{}).Where("product_id = ?", productID).Count(&total).Error; err != nil {
		return false, err
	}
	if total == 0 {
		return true, nil
	}
	if err := db.Model(&database.TenantProductBranch{}).
		Where("product_id = ? AND branch_id = ?", productID, branchID).Count(&match).Error; err != nil {
		return false, err
	}
	return match > 0, nil
}

// EnsureAvailable devuelve un error legible si el producto no se vende en branchID.
func EnsureAvailable(db *gorm.DB, productID, branchID uint, productName string) error {
	ok, err := IsAvailable(db, productID, branchID)
	if err != nil {
		return err
	}
	if !ok {
		if productName == "" {
			productName = fmt.Sprintf("#%d", productID)
		}
		return fmt.Errorf("«%s» no está disponible en esta sucursal (revisa «Disponible en» en el producto)", productName)
	}
	return nil
}

// AssignIfRestricted agrega branchID a un producto que YA tiene sucursales asignadas y no la
// incluye. Se llama cuando entra stock a una sucursal (compra, transferencia, ajuste): si no, ese
// stock quedaría invisible en la sucursal que lo recibió. Un producto sin asignación (= todas) no
// se toca: agregarle una fila lo restringiría a esa sola sucursal.
func AssignIfRestricted(db *gorm.DB, productID, branchID uint) error {
	if productID == 0 || branchID == 0 {
		return nil
	}
	ok, err := IsAvailable(db, productID, branchID)
	if err != nil || ok {
		return err
	}
	return db.Create(&database.TenantProductBranch{ProductID: productID, BranchID: branchID}).Error
}

// Set reemplaza las sucursales del producto. Lista vacía = disponible en todas (borra las filas).
func Set(db *gorm.DB, productID uint, branchIDs []uint) error {
	if productID == 0 {
		return errors.New("producto requerido")
	}
	ids := normalize(branchIDs)
	if !Ready(db) {
		if len(ids) == 0 {
			return nil // "todas" es justamente lo que ya rige sin la tabla
		}
		return ErrNotMigrated
	}
	if err := db.Where("product_id = ?", productID).Delete(&database.TenantProductBranch{}).Error; err != nil {
		return err
	}
	for _, bid := range ids {
		if err := db.Create(&database.TenantProductBranch{ProductID: productID, BranchID: bid}).Error; err != nil {
			return err
		}
	}
	return nil
}

// Add agrega sucursales a la asignación existente sin quitar ninguna. Si el producto no tenía
// asignación (= todas), queda restringido a las sucursales indicadas.
func Add(db *gorm.DB, productID uint, branchIDs []uint) error {
	if !Ready(db) {
		return ErrNotMigrated
	}
	for _, bid := range normalize(branchIDs) {
		var n int64
		if err := db.Model(&database.TenantProductBranch{}).
			Where("product_id = ? AND branch_id = ?", productID, bid).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if err := db.Create(&database.TenantProductBranch{ProductID: productID, BranchID: bid}).Error; err != nil {
			return err
		}
	}
	return nil
}

// ValidateBranchIDs comprueba que todas las sucursales existan.
func ValidateBranchIDs(db *gorm.DB, branchIDs []uint) error {
	ids := normalize(branchIDs)
	if len(ids) == 0 {
		return nil
	}
	var n int64
	if err := db.Model(&database.TenantBranch{}).Where("id IN ?", ids).Count(&n).Error; err != nil {
		return err
	}
	if int(n) != len(ids) {
		return errors.New("alguna de las sucursales indicadas no existe")
	}
	return nil
}

// ListFilterSQL: condición para listar productos vendibles en una sucursal. Lleva un `?` (la
// sucursal) antes de los parámetros de `legacy`, que es la regla previa y aplica solo a productos
// sin asignación. Usar solo si Ready(db); si no, filtrar únicamente con `legacy`.
func ListFilterSQL(productIDExpr, legacy string) string {
	return `(EXISTS (SELECT 1 FROM tenant_product_branches pb WHERE pb.product_id = ` + productIDExpr + ` AND pb.branch_id = ?)
		OR (NOT EXISTS (SELECT 1 FROM tenant_product_branches pb2 WHERE pb2.product_id = ` + productIDExpr + `) AND ` + legacy + `))`
}

func normalize(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
