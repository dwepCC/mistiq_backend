package service

import (
	"errors"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ErrGalleryFull se devuelve al intentar pasar del tope de imágenes adicionales de un producto.
var ErrGalleryFull = errors.New("el producto ya tiene el máximo de imágenes en la galería")

// ListGallery imágenes adicionales del producto, en orden.
func (s *ProductService) ListGallery(productID uint) ([]database.TenantProductImage, error) {
	var rows []database.TenantProductImage
	err := s.db.Where("product_id = ?", productID).Order("sort_order ASC, id ASC").Find(&rows).Error
	return rows, err
}

// AddGalleryImage registra una imagen adicional (al final). Respeta el tope de la galería.
func (s *ProductService) AddGalleryImage(productID uint, url string) (*database.TenantProductImage, error) {
	var n int64
	if err := s.db.Model(&database.TenantProductImage{}).Where("product_id = ?", productID).Count(&n).Error; err != nil {
		return nil, err
	}
	if n >= database.MaxProductGalleryImages {
		return nil, ErrGalleryFull
	}
	row := &database.TenantProductImage{ProductID: productID, URL: url, SortOrder: int(n) + 1}
	if err := s.db.Create(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

// DeleteGalleryImage elimina una imagen de la galería y devuelve su URL (para borrar el archivo).
func (s *ProductService) DeleteGalleryImage(productID, imageID uint) (string, error) {
	var row database.TenantProductImage
	if err := s.db.Where("id = ? AND product_id = ?", imageID, productID).First(&row).Error; err != nil {
		return "", errors.New("imagen no encontrada")
	}
	if err := s.db.Delete(&row).Error; err != nil {
		return "", err
	}
	return row.URL, nil
}

// PromoteGalleryImage intercambia la imagen principal del producto con una de la galería: la elegida
// pasa a ser la principal y la anterior principal ocupa su lugar en la galería. Devuelve la nueva
// imagen principal.
func (s *ProductService) PromoteGalleryImage(productID, imageID uint) (string, error) {
	var newMain string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var row database.TenantProductImage
		if err := tx.Where("id = ? AND product_id = ?", imageID, productID).First(&row).Error; err != nil {
			return errors.New("imagen no encontrada")
		}
		var p database.TenantProduct
		if err := tx.Select("id", "image_url").First(&p, productID).Error; err != nil {
			return errors.New("producto no encontrado")
		}
		previous := p.ImageURL
		newMain = row.URL
		if err := tx.Model(&database.TenantProduct{}).Where("id = ?", productID).Update("image_url", row.URL).Error; err != nil {
			return err
		}
		if previous == "" {
			// No había principal: la elegida se mueve y deja de estar en la galería.
			return tx.Delete(&row).Error
		}
		return tx.Model(&row).Update("url", previous).Error
	})
	if err != nil {
		return "", err
	}
	return newMain, nil
}

// SetCategoryImage guarda la URL de la imagen de la categoría ("" la quita) y devuelve la anterior.
func (s *ProductService) SetCategoryImage(id uint, url string) (previous string, err error) {
	var cat database.TenantCategory
	if err := s.db.First(&cat, id).Error; err != nil {
		return "", errors.New("categoría no encontrada")
	}
	previous = cat.ImageURL
	if err := s.db.Model(&database.TenantCategory{}).Where("id = ?", id).Update("image_url", url).Error; err != nil {
		return "", err
	}
	return previous, nil
}
