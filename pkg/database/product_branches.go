package database

import "time"

// TenantProductBranch: sucursales donde se vende un producto. Sin filas para un producto =
// disponible en TODAS las sucursales (el comportamiento de siempre, así ningún tenant cambia hasta
// que asigne). Con filas = solo en esas, controle stock o no — antes un producto sin control de
// stock aparecía en todas las sucursales sin forma de separarlo.
//
// Distinto de TenantProduct.BranchID (una sola sucursal dueña, de la carta de Tukichef): ese campo
// se sigue respetando y se trata como una asignación de una sola sucursal (ver
// pkg/productbranch). Tabla creada en la migración de tenant v162.
type TenantProductBranch struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ProductID uint      `gorm:"not null;uniqueIndex:idx_product_branch" json:"product_id"`
	BranchID  uint      `gorm:"not null;uniqueIndex:idx_product_branch;index" json:"branch_id"`
	CreatedAt time.Time `json:"created_at"`
}
