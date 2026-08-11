package docusage

import (
	"errors"
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CatalogPackageView para Billing Hub.
type CatalogPackageView struct {
	ID           uint    `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	DocumentsQty int     `json:"documents_qty"`
	Price        float64 `json:"price"`
	Currency     string  `json:"currency"`
}

// ListActiveCatalogForHub catálogo simplificado.
func ListActiveCatalogForHub() ([]CatalogPackageView, error) {
	rows, err := ListActiveCatalog()
	if err != nil {
		return nil, err
	}
	out := make([]CatalogPackageView, 0, len(rows))
	for _, r := range rows {
		out = append(out, CatalogPackageView{
			ID: r.ID, Name: r.Name, Description: r.Description,
			DocumentsQty: r.DocumentsQty, Price: r.Price, Currency: r.Currency,
		})
	}
	return out, nil
}

// ListActiveCatalog paquetes disponibles para compra.
func ListActiveCatalog() ([]database.SaasDocumentPackage, error) {
	var rows []database.SaasDocumentPackage
	err := database.CentralDB.Where("is_active = ?", true).Order("sort_order asc, id asc").Find(&rows).Error
	return rows, err
}

// ListCatalogAdmin todos los paquetes (central).
func ListCatalogAdmin() ([]database.SaasDocumentPackage, error) {
	var rows []database.SaasDocumentPackage
	err := database.CentralDB.Order("sort_order asc, id asc").Find(&rows).Error
	return rows, err
}

// UpsertPackageInput payload del panel central.
//
// Los tags JSON son obligatorios: el panel envía snake_case y encoding/json empareja sin
// distinguir mayúsculas pero NO ignora los guiones bajos, así que "documents_qty" jamás
// llegaba a DocumentsQty. Quedaba en 0 y la validación rechazaba el formulario aunque
// estuviera completo; is_active y sort_order se perdían igual, en silencio.
type UpsertPackageInput struct {
	ID           uint    `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	DocumentsQty int     `json:"documents_qty"`
	Price        float64 `json:"price"`
	Currency     string  `json:"currency"`
	IsActive     bool    `json:"is_active"`
	SortOrder    int     `json:"sort_order"`
}

func UpsertCatalogPackage(in UpsertPackageInput) (*database.SaasDocumentPackage, error) {
	if in.Name == "" || in.DocumentsQty <= 0 {
		return nil, errors.New("nombre y cantidad de documentos son obligatorios")
	}
	if in.Currency == "" {
		in.Currency = "PEN"
	}
	if in.ID > 0 {
		var row database.SaasDocumentPackage
		if database.CentralDB.First(&row, in.ID).Error != nil {
			return nil, ErrPackageNotFound
		}
		updates := map[string]interface{}{
			"name": in.Name, "description": in.Description, "documents_qty": in.DocumentsQty,
			"price": in.Price, "currency": in.Currency, "is_active": in.IsActive, "sort_order": in.SortOrder,
		}
		if err := database.CentralDB.Model(&row).Updates(updates).Error; err != nil {
			return nil, err
		}
		database.CentralDB.First(&row, in.ID)
		return &row, nil
	}
	row := &database.SaasDocumentPackage{
		Name: in.Name, Description: in.Description, DocumentsQty: in.DocumentsQty,
		Price: in.Price, Currency: in.Currency, IsActive: in.IsActive, SortOrder: in.SortOrder,
	}
	if err := database.CentralDB.Create(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

type PurchasePackageInput struct {
	TenantID    uint
	PackageID   uint
	Amount      float64
	Reference   string
	ReceiptURL  string
	SubmittedBy *uint
}

// PurchaseDocumentPackage solicitud con comprobante (pending_review).
func PurchaseDocumentPackage(in PurchasePackageInput) (*database.SaasTenantDocumentPackage, error) {
	cycle, sub, err := CurrentBillingCycle(in.TenantID)
	if err != nil {
		return nil, err
	}
	if cycle.IsUnlimitedDocuments {
		return nil, ErrUnlimitedCannotBuyPkg
	}
	var pkg database.SaasDocumentPackage
	if database.CentralDB.Where("id = ? AND is_active = ?", in.PackageID, true).First(&pkg).Error != nil {
		return nil, ErrPackageNotFound
	}
	amount := in.Amount
	if amount <= 0 {
		amount = pkg.Price
	}
	row := &database.SaasTenantDocumentPackage{
		TenantID: in.TenantID, SubscriptionID: sub.ID, BillingCycleID: cycle.ID,
		PackageID: pkg.ID, DocumentsQty: pkg.DocumentsQty, RemainingDocuments: 0,
		Status: database.SaasDocPkgPendingReview, Amount: amount,
		Reference: in.Reference, ReceiptURL: in.ReceiptURL, SubmittedBy: in.SubmittedBy,
		ExpiresAt: cycle.PeriodEnd,
	}
	if err := database.CentralDB.Create(row).Error; err != nil {
		return nil, err
	}
	logEvent(in.TenantID, &sub.ID, "document_package_requested", "tenant", in.SubmittedBy,
		fmt.Sprintf("Paquete %s (%d docs)", pkg.Name, pkg.DocumentsQty), metaJSON(map[string]interface{}{
			"tenant_package_id": row.ID, "package_id": pkg.ID,
		}))
	return row, nil
}

// ApproveTenantPackage uso inmediato tras aprobación.
func ApproveTenantPackage(tenantPackageID uint, adminID uint, notes string) error {
	return database.CentralDB.Transaction(func(tx *gorm.DB) error {
		var row database.SaasTenantDocumentPackage
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, tenantPackageID).Error; err != nil {
			return errors.New("solicitud no encontrada")
		}
		if row.Status != database.SaasDocPkgPendingReview {
			return ErrPackageAlreadyReviewed
		}
		now := nowLima()
		if err := tx.Model(&row).Updates(map[string]interface{}{
			"status": database.SaasDocPkgApproved, "remaining_documents": row.DocumentsQty,
			"approved_at": now, "approved_by": adminID,
		}).Error; err != nil {
			return err
		}
		sid := row.SubscriptionID
		logEventTx(tx, row.TenantID, &sid, "document_package_approved", "admin", &adminID, notes,
			metaJSON(map[string]interface{}{"tenant_package_id": row.ID, "documents_qty": row.DocumentsQty}))
		invalidateTenantCache(row.TenantID)
		return nil
	})
}

func RejectTenantPackage(tenantPackageID uint, adminID uint, reason string) error {
	return database.CentralDB.Transaction(func(tx *gorm.DB) error {
		var row database.SaasTenantDocumentPackage
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, tenantPackageID).Error; err != nil {
			return errors.New("solicitud no encontrada")
		}
		if row.Status != database.SaasDocPkgPendingReview {
			return ErrPackageAlreadyReviewed
		}
		now := nowLima()
		if err := tx.Model(&row).Updates(map[string]interface{}{
			"status": database.SaasDocPkgRejected, "rejected_at": now, "rejected_reason": reason,
		}).Error; err != nil {
			return err
		}
		sid := row.SubscriptionID
		logEventTx(tx, row.TenantID, &sid, "document_package_rejected", "admin", &adminID, reason, "")
		return nil
	})
}

// ListPendingPackages admin.
func ListPendingPackages() ([]database.SaasTenantDocumentPackage, error) {
	var rows []database.SaasTenantDocumentPackage
	err := database.CentralDB.Where("status = ?", database.SaasDocPkgPendingReview).
		Order("created_at asc").Find(&rows).Error
	return rows, err
}

func ListTenantPackages(tenantID uint) ([]database.SaasTenantDocumentPackage, error) {
	var rows []database.SaasTenantDocumentPackage
	err := database.CentralDB.Where("tenant_id = ?", tenantID).Order("created_at desc").Limit(50).Find(&rows).Error
	return rows, err
}
