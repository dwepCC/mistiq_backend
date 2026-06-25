package salecontext

import (
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// Repository persiste y carga contexto fiscal de ventas.
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Save persiste perfil, referencias y obligaciones (reemplaza hijos existentes).
func (r *Repository) Save(
	profile database.TenantSaleFiscalProfile,
	references []database.TenantSaleFiscalReference,
	obligations []database.TenantSaleFiscalObligation,
) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&profile).Error; err != nil {
			return err
		}
		if err := tx.Where("sale_id = ?", profile.SaleID).Delete(&database.TenantSaleFiscalReference{}).Error; err != nil {
			return err
		}
		if err := tx.Where("sale_id = ?", profile.SaleID).Delete(&database.TenantSaleFiscalObligation{}).Error; err != nil {
			return err
		}
		if len(references) > 0 {
			if err := tx.Create(&references).Error; err != nil {
				return err
			}
		}
		if len(obligations) > 0 {
			if err := tx.Create(&obligations).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// LoadBySaleID carga contexto fiscal completo.
func (r *Repository) LoadBySaleID(saleID uint) (*database.TenantSaleFiscalProfile, []database.TenantSaleFiscalReference, []database.TenantSaleFiscalObligation, error) {
	var profile database.TenantSaleFiscalProfile
	err := r.db.First(&profile, "sale_id = ?", saleID).Error
	if err != nil {
		return nil, nil, nil, err
	}
	var refs []database.TenantSaleFiscalReference
	_ = r.db.Where("sale_id = ?", saleID).Order("sort_order ASC, id ASC").Find(&refs).Error
	var obligations []database.TenantSaleFiscalObligation
	_ = r.db.Where("sale_id = ?", saleID).Order("id ASC").Find(&obligations).Error
	return &profile, refs, obligations, nil
}

// ContactFromModel convierte TenantContact a snapshot.
func ContactFromModel(c *database.TenantContact) *ContactSnapshot {
	if c == nil || c.ID == 0 {
		return nil
	}
	return &ContactSnapshot{
		DocType:              c.DocType,
		DocNumber:            c.DocNumber,
		EsAgenteDeRetencion:  c.EsAgenteDeRetencion,
		EsAgenteDePercepcion: c.EsAgenteDePercepcion,
	}
}

func normalizeReferenceInput(ref FiscalReferenceInput) (database.TenantSaleFiscalReference, bool) {
	kind := strings.TrimSpace(ref.ReferenceKind)
	full := strings.TrimSpace(ref.ReferencedFullNumber)
	if kind == "" && full == "" {
		return database.TenantSaleFiscalReference{}, false
	}
	series := strings.TrimSpace(ref.ReferencedSeries)
	number := strings.TrimSpace(ref.ReferencedNumber)
	if full == "" && series != "" && number != "" {
		full = series + "-" + number
	}
	if full == "" {
		return database.TenantSaleFiscalReference{}, false
	}
	if series == "" || number == "" {
		if parts := strings.SplitN(full, "-", 2); len(parts) == 2 {
			if series == "" {
				series = parts[0]
			}
			if number == "" {
				number = parts[1]
			}
		}
	}
	sunatType := strings.TrimSpace(ref.ReferencedSunatType)
	if sunatType == "" {
		switch kind {
		case RefKindGuiaRemitente:
			sunatType = "09"
		case RefKindGuiaTransportista:
			sunatType = "31"
		}
	}
	return database.TenantSaleFiscalReference{
		ReferenceKind:        kind,
		ReferencedSunatType:  sunatType,
		ReferencedSeries:     series,
		ReferencedNumber:     number,
		ReferencedFullNumber: full,
		SortOrder:            ref.SortOrder,
	}, true
}
