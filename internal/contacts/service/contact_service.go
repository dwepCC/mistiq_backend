package service

import (
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/tenantstorage"

	"gorm.io/gorm"
)

type ContactService struct {
	db *gorm.DB
}

func NewContactService(db *gorm.DB) *ContactService {
	return &ContactService{db: db}
}

type ContactListParams struct {
	Query     string
	Type      string
	DocNumber string
	// Status: "active" (default), "inactive" o "all"
	Status string
}

func (s *ContactService) List(params ContactListParams) ([]database.TenantContact, error) {
	var contacts []database.TenantContact
	q := s.db.Model(&database.TenantContact{})
	if params.Query != "" {
		q = q.Where("business_name LIKE ? OR doc_number LIKE ? OR trade_name LIKE ?",
			"%"+params.Query+"%", "%"+params.Query+"%", "%"+params.Query+"%")
	}
	if params.Type != "" {
		q = q.Where("type = ? OR type = 'both'", params.Type)
	}
	if params.DocNumber != "" {
		q = q.Where("doc_number = ?", params.DocNumber)
	}
	switch strings.ToLower(strings.TrimSpace(params.Status)) {
	case "inactive":
		q = q.Where("active = ?", false)
	case "all":
		// sin filtro por estado
	default:
		q = q.Where("active = ?", true)
	}
	err := q.Preload("ContactPersons").
		Order("business_name ASC").
		Find(&contacts).Error
	return contacts, err
}

func (s *ContactService) GetByID(id uint) (*database.TenantContact, error) {
	var c database.TenantContact
	err := s.db.Preload("ContactPersons", func(db *gorm.DB) *gorm.DB {
		return db.Order("id ASC")
	}).First(&c, id).Error
	return &c, err
}

type ContactPersonInput struct {
	Name         string
	Phone        string
	Email        string
	Relationship string
}

type ContactInput struct {
	Type           string
	DocType        string
	DocNumber      string
	BusinessName   string
	TradeName      string
	Address        string
	Ubigeo         string
	Phone          string
	Email          string
	PhotoURL       string
	ContactPerson  string
	Notes          string
	ContactPersons []ContactPersonInput
	EsAgenteDeRetencion             *bool
	EsAgenteDePercepcion            *bool
	EsAgenteDePercepcionCombustible *bool
	EsBuenContribuyente             *bool
}

func validateContactPersons(persons []ContactPersonInput) error {
	for i, p := range persons {
		name := strings.TrimSpace(p.Name)
		phone := strings.TrimSpace(p.Phone)
		email := strings.TrimSpace(p.Email)
		rel := strings.TrimSpace(p.Relationship)
		if name == "" && phone == "" && email == "" && rel == "" {
			continue
		}
		if name == "" {
			return fmt.Errorf("el contacto adicional en la fila %d debe tener nombre", i+1)
		}
	}
	return nil
}

func (s *ContactService) replaceContactPersons(tx *gorm.DB, contactID uint, persons []ContactPersonInput) error {
	if err := tx.Where("contact_id = ?", contactID).Delete(&database.TenantContactPerson{}).Error; err != nil {
		return err
	}
	for _, p := range persons {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		row := database.TenantContactPerson{
			ContactID:    contactID,
			Name:         name,
			Phone:        strings.TrimSpace(p.Phone),
			Email:        strings.TrimSpace(p.Email),
			Relationship: strings.TrimSpace(p.Relationship),
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *ContactService) Create(input ContactInput) (*database.TenantContact, error) {
	if input.DocNumber == "" || input.BusinessName == "" {
		return nil, errors.New("número de documento y razón social son requeridos")
	}
	if err := validateContactPersons(input.ContactPersons); err != nil {
		return nil, err
	}

	addr, ubi := database.NormalizeTenantContactAddressUbigeo(input.Address, input.Ubigeo)
	contact := &database.TenantContact{
		Type:                            input.Type,
		DocType:                         input.DocType,
		DocNumber:                       input.DocNumber,
		BusinessName:                    input.BusinessName,
		TradeName:                       input.TradeName,
		Address:                         addr,
		Ubigeo:                          ubi,
		Phone:                           input.Phone,
		Email:                           input.Email,
		PhotoURL:                        strings.TrimSpace(input.PhotoURL),
		ContactPerson:                   input.ContactPerson,
		Notes:                           input.Notes,
		EsAgenteDeRetencion:             boolOrDefault(input.EsAgenteDeRetencion, false),
		EsAgenteDePercepcion:            boolOrDefault(input.EsAgenteDePercepcion, false),
		EsAgenteDePercepcionCombustible: boolOrDefault(input.EsAgenteDePercepcionCombustible, false),
		EsBuenContribuyente:             boolOrDefault(input.EsBuenContribuyente, false),
		Active:                          true,
	}
	if contact.Type == "" {
		contact.Type = "customer"
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(contact).Error; err != nil {
			return err
		}
		return s.replaceContactPersons(tx, contact.ID, input.ContactPersons)
	})
	if err != nil {
		return nil, err
	}
	return s.GetByID(contact.ID)
}

func (s *ContactService) Update(id uint, input ContactInput) error {
	if err := validateContactPersons(input.ContactPersons); err != nil {
		return err
	}
	addr, ubi := database.NormalizeTenantContactAddressUbigeo(input.Address, input.Ubigeo)
	updates := map[string]interface{}{
		"type":                               input.Type,
		"doc_type":                           input.DocType,
		"doc_number":                         input.DocNumber,
		"business_name":                      input.BusinessName,
		"trade_name":                         input.TradeName,
		"address":                            addr,
		"ubigeo":                             ubi,
		"phone":                              input.Phone,
		"email":                              input.Email,
		"photo_url":                          strings.TrimSpace(input.PhotoURL),
		"contact_person":                     input.ContactPerson,
		"notes":                              input.Notes,
		"es_agente_de_retencion":             boolOrDefault(input.EsAgenteDeRetencion, false),
		"es_agente_de_percepcion":            boolOrDefault(input.EsAgenteDePercepcion, false),
		"es_agente_de_percepcion_combustible": boolOrDefault(input.EsAgenteDePercepcionCombustible, false),
		"es_buen_contribuyente":              boolOrDefault(input.EsBuenContribuyente, false),
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.TenantContact{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		return s.replaceContactPersons(tx, id, input.ContactPersons)
	})
}

func (s *ContactService) UpdatePhotoURL(id uint, photoURL string) error {
	return s.db.Model(&database.TenantContact{}).Where("id = ?", id).Update("photo_url", photoURL).Error
}

func (s *ContactService) Delete(id uint) error {
	var c database.TenantContact
	if err := s.db.First(&c, id).Error; err != nil {
		return err
	}
	oldPhoto := strings.TrimSpace(c.PhotoURL)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("contact_id = ?", id).Delete(&database.TenantContactPerson{}).Error; err != nil {
			return err
		}
		return tx.Delete(&database.TenantContact{}, id).Error
	})
	if err == nil && oldPhoto != "" {
		_ = tenantstorage.DeleteUploadByPublicURL(oldPhoto)
	}
	return err
}

func (s *ContactService) ToggleActive(id uint) error {
	var c database.TenantContact
	if err := s.db.First(&c, id).Error; err != nil {
		return err
	}
	return s.db.Model(&c).Update("active", !c.Active).Error
}

func (s *ContactService) SearchByDoc(docNumber string) (*database.TenantContact, error) {
	var c database.TenantContact
	err := s.db.Where("doc_number = ? AND active = ?", docNumber, true).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

func (s *ContactService) GetDefaultClient() (*database.TenantContact, error) {
	var c database.TenantContact
	err := s.db.Where("doc_type = ? AND doc_number = ? AND active = ? AND (type = ? OR type = ?)",
		"0", "99999999", true, "customer", "both").
		First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.EnsureDefaultClient()
	}
	return &c, err
}

func (s *ContactService) EnsureDefaultClient() (*database.TenantContact, error) {
	var c database.TenantContact
	err := s.db.Where("doc_type = ? AND doc_number = ?", "0", "99999999").First(&c).Error
	if err == nil {
		addr, ubi := database.NormalizeTenantContactAddressUbigeo(c.Address, c.Ubigeo)
		if strings.TrimSpace(c.Address) == "" || strings.TrimSpace(c.Ubigeo) == "" {
			s.db.Model(&c).Updates(map[string]interface{}{"address": addr, "ubigeo": ubi})
			c.Address, c.Ubigeo = addr, ubi
		}
		if c.Active {
			return &c, nil
		}
		s.db.Model(&c).Update("active", true)
		return &c, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	defAddr, defUbi := database.NormalizeTenantContactAddressUbigeo("", "")
	contact := &database.TenantContact{
		Type:         "customer",
		DocType:      "0",
		DocNumber:    "99999999",
		BusinessName: "Cliente por defecto (Varios)",
		Address:      defAddr,
		Ubigeo:       defUbi,
		Active:       true,
	}
	if err := s.db.Create(contact).Error; err != nil {
		return nil, err
	}
	return contact, nil
}

func boolOrDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}
