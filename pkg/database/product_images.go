package database

import "time"

// TenantProductImage imagen adicional de la galería de un producto (se muestran en el detalle del
// producto del Catálogo Digital). La imagen principal sigue siendo TenantProduct.ImageURL: aquí solo
// viven las demás.
type TenantProductImage struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ProductID uint      `gorm:"not null;index" json:"product_id"`
	URL       string    `gorm:"size:255;not null" json:"url"`
	SortOrder int       `gorm:"default:0" json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
}

func (TenantProductImage) TableName() string { return "tenant_product_images" }

// MaxProductGalleryImages tope de imágenes adicionales por producto.
const MaxProductGalleryImages = 8
