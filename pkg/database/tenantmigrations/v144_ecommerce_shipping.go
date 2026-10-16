package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// v144Order, v144Dispatch y v144Carrier son espejos locales de las columnas nuevas de
// database.TenantEcommerceOrder / TenantEcommerceDispatch / TenantCarrier (mismo criterio que
// v142: la migración no depende de los modelos vivos, que pueden seguir cambiando).
type v144Order struct {
	ID                 uint `gorm:"primaryKey"`
	CarrierID          *uint
	DestinationUbigeo  string  `gorm:"size:6"`
	DestinationAgency  string  `gorm:"size:150"`
	DestinationAddress string  `gorm:"size:255"`
	RecipientName      string  `gorm:"size:150"`
	RecipientDocType   string  `gorm:"size:2"`
	RecipientDocNumber string  `gorm:"size:20"`
	RecipientPhone     string  `gorm:"size:30"`
	ShippingCost       float64 `gorm:"type:decimal(15,2);default:0"`
	PackageCount       *int
	WeightKg           *float64
}

func (v144Order) TableName() string { return "tenant_ecommerce_orders" }

type v144Dispatch struct {
	ID              uint `gorm:"primaryKey"`
	CarrierID       *uint
	AgencyArrivedAt *time.Time
	PickupDeadline  *time.Time
	ReturnedAt      *time.Time
}

func (v144Dispatch) TableName() string { return "tenant_ecommerce_dispatches" }

type v144Carrier struct {
	ID                 uint   `gorm:"primaryKey"`
	Name               string `gorm:"size:150;not null"`
	Kind               string `gorm:"size:20;not null;default:'AGENCIA'"`
	Active             bool   `gorm:"default:true"`
	DispatchDays       string `gorm:"size:20"`
	PickupDeadlineDays int    `gorm:"default:15"`
	WarnDaysBefore     int    `gorm:"default:5"`
	TrackingURL        string `gorm:"size:255"`
	ShowInStore        bool   `gorm:"default:true"`
	SortOrder          int    `gorm:"default:0"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

func (v144Carrier) TableName() string { return "tenant_carriers" }

// V144EcommerceShipping habilita la gestión de envíos por agencia (Shalom, Olva…): catálogo de
// transportistas con sus reglas (días de salida, plazo de recojo), destino/agencia/destinatario en
// el pedido, y llegada a agencia + fecha límite de recojo en el despacho.
type V144EcommerceShipping struct{}

func (V144EcommerceShipping) Version() int { return 144 }
func (V144EcommerceShipping) Name() string { return "ecommerce_shipping" }

func (V144EcommerceShipping) Up(db *gorm.DB) error {
	mig := db.Migrator()

	if !mig.HasTable(&v144Carrier{}) {
		if err := mig.CreateTable(&v144Carrier{}); err != nil {
			return fmt.Errorf("tenant_carriers: %w", err)
		}
	}

	if mig.HasTable(&v144Order{}) {
		for _, col := range []string{
			"CarrierID", "DestinationUbigeo", "DestinationAgency", "DestinationAddress",
			"RecipientName", "RecipientDocType", "RecipientDocNumber", "RecipientPhone",
			"ShippingCost", "PackageCount", "WeightKg",
		} {
			if mig.HasColumn(&v144Order{}, col) {
				continue
			}
			if err := mig.AddColumn(&v144Order{}, col); err != nil {
				return fmt.Errorf("tenant_ecommerce_orders.%s: %w", col, err)
			}
		}
		if !migrationHasIndex(db, "tenant_ecommerce_orders", "idx_tenant_ecommerce_orders_carrier_id") {
			if err := db.Exec(`CREATE INDEX idx_tenant_ecommerce_orders_carrier_id ON tenant_ecommerce_orders (carrier_id)`).Error; err != nil {
				return fmt.Errorf("idx carrier_id pedidos: %w", err)
			}
		}
	}

	if mig.HasTable(&v144Dispatch{}) {
		for _, col := range []string{"CarrierID", "AgencyArrivedAt", "PickupDeadline", "ReturnedAt"} {
			if mig.HasColumn(&v144Dispatch{}, col) {
				continue
			}
			if err := mig.AddColumn(&v144Dispatch{}, col); err != nil {
				return fmt.Errorf("tenant_ecommerce_dispatches.%s: %w", col, err)
			}
		}
		if !migrationHasIndex(db, "tenant_ecommerce_dispatches", "idx_tenant_ecommerce_dispatches_pickup_deadline") {
			if err := db.Exec(`CREATE INDEX idx_tenant_ecommerce_dispatches_pickup_deadline ON tenant_ecommerce_dispatches (pickup_deadline)`).Error; err != nil {
				return fmt.Errorf("idx pickup_deadline: %w", err)
			}
		}
	}
	return nil
}
