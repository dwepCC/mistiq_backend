package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v103Product struct {
	ID                   uint `gorm:"primaryKey"`
	ShowInDigitalCatalog bool `gorm:"column:show_in_digital_catalog;default:false"`
	ShowInDigitalMenu    bool `gorm:"column:show_in_digital_menu;default:false"`
}

func (v103Product) TableName() string { return "tenant_products" }

type v103EcommerceSettings struct {
	ID                 uint    `gorm:"primaryKey"`
	Enabled            bool    `gorm:"default:false"`
	StoreName          string  `gorm:"size:150"`
	Tagline            string  `gorm:"size:255"`
	Description        string  `gorm:"type:text"`
	LogoURL            string  `gorm:"size:255"`
	BackgroundImageURL string  `gorm:"size:255"`
	WhatsAppNumber     *string `gorm:"column:whatsapp_number;size:30"`
	TemplateKey        string  `gorm:"size:30;default:'moderno'"`
	PrimaryColor       string  `gorm:"size:20;default:'#16a34a'"`
	SecondaryColor     string  `gorm:"size:20;default:'#0f172a'"`
	FontFamily         string  `gorm:"size:60;default:'Inter'"`
	CardStyle          string  `gorm:"size:30;default:'rounded'"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (v103EcommerceSettings) TableName() string { return "tenant_ecommerce_settings" }

type v103EcommerceSlider struct {
	ID        uint   `gorm:"primaryKey"`
	ImageURL  string `gorm:"size:255;not null"`
	LinkURL   string `gorm:"size:255"`
	SortOrder int    `gorm:"default:0"`
	Active    bool   `gorm:"default:true"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (v103EcommerceSlider) TableName() string { return "tenant_ecommerce_sliders" }

type v103EcommerceOrder struct {
	ID            uint    `gorm:"primaryKey"`
	CustomerName  string  `gorm:"size:150"`
	CustomerPhone string  `gorm:"size:30"`
	ItemsJSON     string  `gorm:"type:text;not null"`
	Total         float64 `gorm:"type:decimal(15,2);not null"`
	Status        string  `gorm:"size:20;default:'nuevo';index"`
	Notes         string  `gorm:"type:text"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (v103EcommerceOrder) TableName() string { return "tenant_ecommerce_orders" }

// V103Ecommerce: Catálogo Digital (tienda virtual pública con carrito y checkout por WhatsApp).
//
// Agrega a tenant_products dos canales de difusión independientes de is_restaurant:
// show_in_digital_catalog (Catálogo Digital, se implementa ahora) y show_in_digital_menu
// (reservado para el futuro Menú Digital de Tukichef, sin UI todavía).
//
// tenant_ecommerce_settings es fila única (id=1) con el diseño/config de la tienda, totalmente
// independiente del tema del panel (--p50…--p900). tenant_ecommerce_sliders son los banners del
// carrusel. tenant_ecommerce_orders es el registro de pedidos armados en la tienda pública (el
// checkout real ocurre por WhatsApp; esta tabla es solo historial/seguimiento).
//
// NOTA: originalmente registrada como versión 102, pero ese número ya existía en
// tenant_migration_history (fila histórica "finalize_orphan_table_orders", type=schema) de una
// migración v102 anterior ya eliminada del registro — dejaba el motor de migraciones incrementales
// creyendo que esta migración ya estaba aplicada y nunca ejecutaba Up(). Renumerada a 103.
type V103Ecommerce struct{}

func (V103Ecommerce) Version() int { return 103 }
func (V103Ecommerce) Name() string { return "ecommerce" }

func (V103Ecommerce) Up(db *gorm.DB) error {
	mig := db.Migrator()

	if mig.HasTable(&v103Product{}) {
		for _, col := range []string{"ShowInDigitalCatalog", "ShowInDigitalMenu"} {
			if mig.HasColumn(&v103Product{}, col) {
				continue
			}
			if err := mig.AddColumn(&v103Product{}, col); err != nil {
				return fmt.Errorf("add tenant_products.%s: %w", col, err)
			}
		}
	}

	for _, tbl := range []any{&v103EcommerceSettings{}, &v103EcommerceSlider{}, &v103EcommerceOrder{}} {
		if !mig.HasTable(tbl) {
			if err := mig.CreateTable(tbl); err != nil {
				return fmt.Errorf("create %T: %w", tbl, err)
			}
		}
	}

	if !mig.HasTable(&v103EcommerceSettings{}) {
		return nil
	}
	var count int64
	if err := db.Model(&v103EcommerceSettings{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count tenant_ecommerce_settings: %w", err)
	}
	if count == 0 {
		if err := db.Create(&v103EcommerceSettings{ID: 1}).Error; err != nil {
			return fmt.Errorf("seed tenant_ecommerce_settings: %w", err)
		}
	}

	return nil
}
