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

// buildListQuery arma el filtro común (búsqueda, tipo, documento, estado) de las listas.
func (s *ContactService) buildListQuery(params ContactListParams) *gorm.DB {
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
	return q
}

func (s *ContactService) List(params ContactListParams) ([]database.TenantContact, error) {
	var contacts []database.TenantContact
	err := s.buildListQuery(params).Preload("ContactPersons").
		Order("business_name ASC").
		Find(&contacts).Error
	return contacts, err
}

// ListPaged devuelve una página de contactos y el total que cumple el filtro.
// Se usa en la vista de lista; los selectores (POS/ventas) siguen usando List sin paginar.
func (s *ContactService) ListPaged(params ContactListParams, page, perPage int) ([]database.TenantContact, int64, error) {
	base := s.buildListQuery(params)

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var contacts []database.TenantContact
	err := base.Preload("ContactPersons").
		Order("business_name ASC").
		Limit(perPage).
		Offset((page - 1) * perPage).
		Find(&contacts).Error
	return contacts, total, err
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
	Type                            string
	DocType                         string
	DocNumber                       string
	BusinessName                    string
	TradeName                       string
	Address                         string
	Ubigeo                          string
	Phone                           string
	Email                           string
	PhotoURL                        string
	ContactPerson                   string
	Notes                           string
	ContactPersons                  []ContactPersonInput
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

// contactTypesThatCollideWith devuelve los valores de `type` que ya ocupan el mismo "rol" que
// contactType para un mismo documento. "both" cubre el rol de cliente Y de proveedor a la vez,
// así que colisiona con cualquiera de los dos; "customer"/"supplier" solo colisionan con su
// propio rol o con "both". Esto permite que un mismo documento tenga un registro de cliente y
// otro de proveedor por separado (son roles distintos), pero nunca dos con el mismo rol.
func contactTypesThatCollideWith(contactType string) []string {
	switch contactType {
	case "supplier":
		return []string{"supplier", "both"}
	case "both":
		return []string{"customer", "supplier", "both"}
	default: // "customer" y cualquier valor no reconocido caen en cliente, igual que el default del modelo
		return []string{"customer", "both"}
	}
}

func contactTypeLabel(t string) string {
	switch t {
	case "supplier":
		return "proveedor"
	case "both":
		return "cliente/proveedor"
	default:
		return "cliente"
	}
}

// findDuplicateContact busca un contacto ACTIVO existente con el mismo documento cuyo tipo
// colisione con contactType (ver contactTypesThatCollideWith). excludeID se usa desde Update
// para no chocar contra el propio registro que se está editando.
//
// Solo cuentan los activos a propósito: un contacto desactivado (la forma en que hoy se
// "retiran" duplicados viejos sin borrar su historial, mientras no hay UNIQUE INDEX en BD)
// no debe bloquear ediciones futuras del contacto que sí quedó vigente para ese documento —
// si no, apenas alguien desactive un duplicado, el que quedó activo se vuelve imposible de
// editar porque esta misma validación chocaría contra su gemelo dormido.
func (s *ContactService) findDuplicateContact(tx *gorm.DB, docType, docNumber, contactType string, excludeID uint) (*database.TenantContact, error) {
	var existing database.TenantContact
	q := tx.Where("doc_type = ? AND doc_number = ? AND type IN ? AND active = ?", docType, docNumber, contactTypesThatCollideWith(contactType), true)
	if excludeID != 0 {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

func duplicateContactError(existing *database.TenantContact) error {
	return fmt.Errorf("ya existe un %s con este documento: %s", contactTypeLabel(existing.Type), existing.BusinessName)
}

func (s *ContactService) Create(input ContactInput) (*database.TenantContact, error) {
	if input.DocNumber == "" || input.BusinessName == "" {
		return nil, errors.New("número de documento y razón social son requeridos")
	}
	if err := validateContactPersons(input.ContactPersons); err != nil {
		return nil, err
	}

	docType := normalizeContactDocType(input.DocType)
	if docType == "" {
		docType = "6" // RUC por defecto, igual que el default de la columna en BD
	}
	docNumber := strings.TrimSpace(input.DocNumber)
	contactType := input.Type
	if contactType == "" {
		contactType = "customer"
	}

	addr, ubi := database.NormalizeTenantContactAddressUbigeo(input.Address, input.Ubigeo)
	contact := &database.TenantContact{
		Type:                            contactType,
		DocType:                         docType,
		DocNumber:                       docNumber,
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

	err := s.db.Transaction(func(tx *gorm.DB) error {
		dup, err := s.findDuplicateContact(tx, docType, docNumber, contactType, 0)
		if err != nil {
			return err
		}
		if dup != nil {
			return duplicateContactError(dup)
		}
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

	docType := normalizeContactDocType(input.DocType)
	if docType == "" {
		docType = "6"
	}
	docNumber := strings.TrimSpace(input.DocNumber)
	contactType := input.Type
	if contactType == "" {
		contactType = "customer"
	}

	addr, ubi := database.NormalizeTenantContactAddressUbigeo(input.Address, input.Ubigeo)
	updates := map[string]interface{}{
		"type":                                contactType,
		"doc_type":                            docType,
		"doc_number":                          docNumber,
		"business_name":                       input.BusinessName,
		"trade_name":                          input.TradeName,
		"address":                             addr,
		"ubigeo":                              ubi,
		"phone":                               input.Phone,
		"email":                               input.Email,
		"photo_url":                           strings.TrimSpace(input.PhotoURL),
		"contact_person":                      input.ContactPerson,
		"notes":                               input.Notes,
		"es_agente_de_retencion":              boolOrDefault(input.EsAgenteDeRetencion, false),
		"es_agente_de_percepcion":             boolOrDefault(input.EsAgenteDePercepcion, false),
		"es_agente_de_percepcion_combustible": boolOrDefault(input.EsAgenteDePercepcionCombustible, false),
		"es_buen_contribuyente":               boolOrDefault(input.EsBuenContribuyente, false),
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		dup, err := s.findDuplicateContact(tx, docType, docNumber, contactType, id)
		if err != nil {
			return err
		}
		if dup != nil {
			return duplicateContactError(dup)
		}
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
