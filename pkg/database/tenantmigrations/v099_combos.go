package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v099Product struct {
	ID       uint `gorm:"primaryKey"`
	HasCombo bool `gorm:"column:has_combo;default:false"`
}

func (v099Product) TableName() string { return "tenant_products" }

type v099ComboGroup struct {
	ID            uint   `gorm:"primaryKey"`
	ProductID     uint   `gorm:"not null;index"`
	Name          string `gorm:"size:120;not null"`
	SelectionType string `gorm:"size:20;default:'fixed';index"`
	MinSelect     int    `gorm:"default:1"`
	MaxSelect     int    `gorm:"default:1"`
	AllowQuantity bool   `gorm:"default:false"`
	SortOrder     int    `gorm:"default:0"`
	Active        bool   `gorm:"default:true"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

func (v099ComboGroup) TableName() string { return "tenant_combo_groups" }

type v099ComboGroupItem struct {
	ID                uint    `gorm:"primaryKey"`
	GroupID           uint    `gorm:"not null;index"`
	ProductID         uint    `gorm:"not null;index"`
	PreparationAreaID *uint   `gorm:"index"`
	DefaultQuantity   float64 `gorm:"type:decimal(15,3);default:1"`
	MaxQuantity       float64 `gorm:"type:decimal(15,3);default:1"`
	ExtraPrice        float64 `gorm:"type:decimal(15,2);default:0"`
	IsDefault         bool    `gorm:"default:false"`
	SortOrder         int     `gorm:"default:0"`
	Active            bool    `gorm:"default:true"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletedAt         gorm.DeletedAt `gorm:"index"`
}

func (v099ComboGroupItem) TableName() string { return "tenant_combo_group_items" }

type v099Comanda struct {
	ID                uint   `gorm:"primaryKey"`
	PreparationAreaID *uint  `gorm:"column:preparation_area_id;index"`
	ComboParentKey    string `gorm:"column:combo_parent_key;size:64;index"`
	ComboJSON         string `gorm:"column:combo_json;type:text"`
}

func (v099Comanda) TableName() string { return "tenant_comandas" }

// V099Combos: combos/promociones como producto compuesto (tenant_products.has_combo) con
// grupos de selección (fixed | single | multiple) e items componentes.
//
// El precio del combo es fijo (tenant_products.sale_price); los componentes solo aportan
// extra_price opcional. En cocina el combo explota en una comanda por componente, ruteada
// por preparation_area_id, agrupadas por combo_parent_key; en la venta sigue siendo una
// sola línea facturable.
//
// Agrega también tenant_comandas.preparation_area_id (hasta ahora el área solo vivía como
// slug denormalizado) y lo backfillea desde tenant_preparation_areas.slug.
type V099Combos struct{}

func (V099Combos) Version() int { return 99 }
func (V099Combos) Name() string { return "combos" }

func (V099Combos) Up(db *gorm.DB) error {
	mig := db.Migrator()

	if !mig.HasTable(&v099Product{}) {
		return nil
	}

	if !mig.HasColumn(&v099Product{}, "HasCombo") {
		if err := mig.AddColumn(&v099Product{}, "HasCombo"); err != nil {
			return fmt.Errorf("add tenant_products.has_combo: %w", err)
		}
	}

	for _, tbl := range []any{&v099ComboGroup{}, &v099ComboGroupItem{}} {
		if !mig.HasTable(tbl) {
			if err := mig.CreateTable(tbl); err != nil {
				return fmt.Errorf("create %T: %w", tbl, err)
			}
		}
	}

	if !mig.HasTable(&v099Comanda{}) {
		return nil
	}
	for _, col := range []string{"PreparationAreaID", "ComboParentKey", "ComboJSON"} {
		if mig.HasColumn(&v099Comanda{}, col) {
			continue
		}
		if err := mig.AddColumn(&v099Comanda{}, col); err != nil {
			return fmt.Errorf("add tenant_comandas.%s: %w", col, err)
		}
	}

	return backfillComandaPreparationAreaID(db)
}

// backfillComandaPreparationAreaID resuelve el id del área desde el slug ya guardado en
// cada comanda. Idempotente: solo toca filas con preparation_area_id nulo.
func backfillComandaPreparationAreaID(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_preparation_areas") {
		return nil
	}
	err := db.Exec(`
		UPDATE tenant_comandas
		SET preparation_area_id = (
			SELECT a.id FROM tenant_preparation_areas a
			WHERE a.slug = tenant_comandas.preparation_area
			  AND a.deleted_at IS NULL
			LIMIT 1
		)
		WHERE preparation_area_id IS NULL
		  AND preparation_area <> ''
		  AND EXISTS (
			SELECT 1 FROM tenant_preparation_areas a
			WHERE a.slug = tenant_comandas.preparation_area
			  AND a.deleted_at IS NULL
		  )
	`).Error
	if err != nil {
		return fmt.Errorf("backfill tenant_comandas.preparation_area_id: %w", err)
	}
	return nil
}
