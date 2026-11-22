package service

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DefaultWarehouseCode código del Almacén 1: el operativo, del que salen todas las ventas.
const DefaultWarehouseCode = "ALM1"

// inOperativeWarehouse condición SQL del kardex para el Almacén 1. Un movimiento sin almacén (NULL) es del Almacén 1:
// así los movimientos anteriores a los almacenes y los que generan los pedidos no necesitan cambios.
const inOperativeWarehouse = "(warehouse_id IS NULL OR warehouse_id = (SELECT id FROM equip_warehouses WHERE code = 'ALM1'))"

// ensureWarehousesTx siembra los almacenes iniciales si faltan: «Almacén 1 (oficina)» y, solo la primera vez,
// «Almacén 2 (Cerro Colorado)». Idempotente.
func ensureWarehousesTx(db *gorm.DB) error {
	var total int64
	if err := db.Model(&database.EquipWarehouse{}).Count(&total).Error; err != nil {
		return err
	}
	var main int64
	if err := db.Model(&database.EquipWarehouse{}).Where("code = ?", DefaultWarehouseCode).Count(&main).Error; err != nil {
		return err
	}
	if main == 0 {
		// OnConflict: dos peticiones simultáneas pueden sembrar a la vez; la clave única del código evita duplicados.
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.EquipWarehouse{Code: DefaultWarehouseCode, Name: "Almacén 1 (oficina)", IsDefault: true, Active: true}).Error; err != nil {
			return err
		}
	}
	if total == 0 {
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.EquipWarehouse{Code: "ALM2", Name: "Almacén 2 (Cerro Colorado)", Active: true, Notes: "Depósito: recibe lo importado y abastece al Almacén 1"}).Error; err != nil {
			return err
		}
	}
	return nil
}

// EnsureWarehouses expone la siembra (la usan las pantallas antes de listar).
func (s *Service) EnsureWarehouses() error { return ensureWarehousesTx(s.db) }

func defaultWarehouseID(db *gorm.DB) (uint, error) {
	if err := ensureWarehousesTx(db); err != nil {
		return 0, err
	}
	var w database.EquipWarehouse
	if err := db.Where("code = ?", DefaultWarehouseCode).First(&w).Error; err != nil {
		return 0, err
	}
	return w.ID, nil
}

// stockAtTx stock actual de los productos en un almacén (el Almacén 1 incluye los movimientos sin almacén).
func stockAtTx(tx *gorm.DB, warehouseID uint, ids []uint) (map[uint]int, error) {
	def, err := defaultWarehouseID(tx)
	if err != nil {
		return nil, err
	}
	q := tx.Model(&database.EquipStockMovement{}).Select("product_id, COALESCE(SUM(quantity), 0) AS total").Where("product_id IN ?", ids)
	if warehouseID == def {
		q = q.Where("(warehouse_id IS NULL OR warehouse_id = ?)", def)
	} else {
		q = q.Where("warehouse_id = ?", warehouseID)
	}
	var rows []struct {
		ProductID uint
		Total     int
	}
	if err := q.Group("product_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[uint]int{}
	for _, r := range rows {
		out[r.ProductID] = r.Total
	}
	return out, nil
}

// WarehouseInput alta o edición de un almacén.
type WarehouseInput struct {
	Name   string `json:"name"`
	Notes  string `json:"notes"`
	Active *bool  `json:"active"`
}

func (s *Service) ListWarehouses(includeInactive bool) ([]database.EquipWarehouse, error) {
	if err := ensureWarehousesTx(s.db); err != nil {
		return nil, err
	}
	var rows []database.EquipWarehouse
	q := s.db.Order("is_default DESC, id ASC")
	if !includeInactive {
		q = q.Where("active = ?", true)
	}
	return rows, q.Find(&rows).Error
}

func (s *Service) CreateWarehouse(in WarehouseInput) (*database.EquipWarehouse, error) {
	if err := ensureWarehousesTx(s.db); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, invalid("el nombre del almacén es obligatorio")
	}
	var dup int64
	if err := s.db.Model(&database.EquipWarehouse{}).Where("LOWER(name) = ?", strings.ToLower(name)).Count(&dup).Error; err != nil {
		return nil, err
	}
	if dup > 0 {
		return nil, invalid("ya existe un almacén con ese nombre")
	}
	var n int64
	_ = s.db.Model(&database.EquipWarehouse{}).Count(&n).Error
	w := database.EquipWarehouse{Code: fmt.Sprintf("ALM%d", n+1), Name: name, Active: true, Notes: strings.TrimSpace(in.Notes)}
	for { // el código debe ser único aunque se hayan borrado o reordenado almacenes
		var taken int64
		_ = s.db.Model(&database.EquipWarehouse{}).Where("code = ?", w.Code).Count(&taken).Error
		if taken == 0 {
			break
		}
		n++
		w.Code = fmt.Sprintf("ALM%d", n+1)
	}
	return &w, s.db.Create(&w).Error
}

func (s *Service) UpdateWarehouse(id uint, in WarehouseInput) (*database.EquipWarehouse, error) {
	var w database.EquipWarehouse
	if found, err := findOne(s.db, &w, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("almacén no encontrado")
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		var dup int64
		if err := s.db.Model(&database.EquipWarehouse{}).Where("LOWER(name) = ? AND id <> ?", strings.ToLower(name), id).Count(&dup).Error; err != nil {
			return nil, err
		}
		if dup > 0 {
			return nil, invalid("ya existe un almacén con ese nombre")
		}
		w.Name = name
	}
	w.Notes = strings.TrimSpace(in.Notes)
	if in.Active != nil {
		if !*in.Active && w.IsDefault {
			return nil, invalid("el Almacén 1 es el operativo y no se puede desactivar")
		}
		w.Active = *in.Active
	}
	return &w, s.db.Save(&w).Error
}

// WarehouseStockRow stock de un producto en cada almacén.
type WarehouseStockRow struct {
	ProductID   uint         `json:"product_id"`
	Code        string       `json:"code"`
	Name        string       `json:"name"`
	Kind        string       `json:"kind"`
	ByWarehouse map[uint]int `json:"by_warehouse"`
	Total       int          `json:"total"`
}

// StockByWarehouse stock actual de cada producto activo repartido por almacén (el Almacén 1 incluye los movimientos
// sin almacén). Los inactivos con stock también se informan.
func (s *Service) StockByWarehouse() ([]WarehouseStockRow, error) {
	def, err := defaultWarehouseID(s.db)
	if err != nil {
		return nil, err
	}
	var products []database.EquipProduct
	if err := s.db.Order("kind ASC, code ASC").Find(&products).Error; err != nil {
		return nil, err
	}
	var rows []struct {
		ProductID   uint
		WarehouseID uint
		Total       int
	}
	// La misma expresión (con el id como literal) en SELECT y GROUP BY: con un parámetro en el SELECT, MySQL en modo
	// only_full_group_by la trata como distinta de la del GROUP BY y rechaza la consulta.
	wh := fmt.Sprintf("COALESCE(warehouse_id, %d)", def)
	if err := s.db.Model(&database.EquipStockMovement{}).
		Select("product_id, " + wh + " AS warehouse_id, COALESCE(SUM(quantity), 0) AS total").
		Group("product_id, " + wh).Scan(&rows).Error; err != nil {
		return nil, err
	}
	by := map[uint]map[uint]int{}
	for _, r := range rows {
		if by[r.ProductID] == nil {
			by[r.ProductID] = map[uint]int{}
		}
		by[r.ProductID][r.WarehouseID] = r.Total
	}
	out := make([]WarehouseStockRow, 0, len(products))
	for _, p := range products {
		m := by[p.ID]
		total := 0
		for _, q := range m {
			total += q
		}
		if !p.Active && total == 0 {
			continue
		}
		if m == nil {
			m = map[uint]int{}
		}
		out = append(out, WarehouseStockRow{ProductID: p.ID, Code: p.Code, Name: p.Name, Kind: p.Kind, ByWarehouse: m, Total: total})
	}
	return out, nil
}

// activeWarehouseTx valida que el almacén exista y esté activo.
func activeWarehouseTx(tx *gorm.DB, id uint, label string) (*database.EquipWarehouse, error) {
	var w database.EquipWarehouse
	if found, err := findOne(tx, &w, "id = ?", id); err != nil {
		return nil, err
	} else if !found || !w.Active {
		return nil, invalid("el almacén %s no existe o está inactivo", label)
	}
	return &w, nil
}
