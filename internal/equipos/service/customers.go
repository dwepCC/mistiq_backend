package service

import (
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// CustomerInput datos editables de un cliente.
type CustomerInput struct {
	Name       string `json:"name"`
	DocType    string `json:"doc_type"`
	DocNumber  string `json:"doc_number"`
	ContactDNI string `json:"contact_dni"`
	Phone      string `json:"phone"`
	Notes      string `json:"notes"`
}

// validateDoc valida el documento según su tipo y devuelve el número limpio.
func validateDoc(docType, number string) (string, error) {
	number = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(number), " ", ""))
	switch docType {
	case "RUC":
		if !onlyDigits(number) || len(number) != 11 {
			return "", invalid("el RUC debe tener 11 dígitos")
		}
	case "DNI":
		if !onlyDigits(number) || len(number) != 8 {
			return "", invalid("el DNI debe tener 8 dígitos")
		}
	case "CE":
		if len(number) < 6 || len(number) > 15 {
			return "", invalid("el documento de extranjería debe tener entre 6 y 15 caracteres")
		}
	default:
		return "", invalid("tipo de documento inválido (RUC, DNI o CE)")
	}
	return number, nil
}

func (in *CustomerInput) normalize() error {
	in.Name = collapse(in.Name)
	in.DocType = strings.ToUpper(strings.TrimSpace(in.DocType))
	in.Notes = strings.TrimSpace(in.Notes)
	if in.Name == "" {
		return invalid("el nombre del cliente es obligatorio")
	}
	num, err := validateDoc(in.DocType, in.DocNumber)
	if err != nil {
		return err
	}
	in.DocNumber = num
	in.ContactDNI = strings.TrimSpace(in.ContactDNI)
	if in.ContactDNI != "" && (!onlyDigits(in.ContactDNI) || len(in.ContactDNI) != 8) {
		return invalid("el DNI de contacto debe tener 8 dígitos")
	}
	in.Phone = strings.TrimSpace(in.Phone)
	return nil
}

// CustomerRow cliente con sus totales de cuenta (para el buscador y el listado).
type CustomerRow struct {
	database.EquipCustomer
	Orders  int     `json:"orders"`
	Balance float64 `json:"balance"` // por cobrar
	Credit  float64 `json:"credit"`  // saldo a favor
}

// ListCustomers busca por nombre, documento o teléfono (sin paginar: para buscadores).
func (s *Service) ListCustomers(q string, limit int) ([]CustomerRow, error) {
	rows, _, err := s.ListCustomersPaged(q, 1, limit)
	return rows, err
}

// ListCustomersPaged lista clientes con paginación y total.
func (s *Service) ListCustomersPaged(q string, page, limit int) ([]CustomerRow, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if page < 1 {
		page = 1
	}
	tx := s.db.Model(&database.EquipCustomer{})
	if q = strings.TrimSpace(q); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		tx = tx.Where("LOWER(name) LIKE ? OR doc_number LIKE ? OR contact_dni LIKE ? OR phone LIKE ?", like, "%"+q+"%", "%"+q+"%", "%"+q+"%")
	}
	tx = tx.Session(&gorm.Session{})
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []database.EquipCustomer
	if err := tx.Order("name ASC").Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]CustomerRow, 0, len(rows))
	ids := make([]uint, 0, len(rows))
	for _, c := range rows {
		out = append(out, CustomerRow{EquipCustomer: c})
		ids = append(ids, c.ID)
	}
	if len(ids) == 0 {
		return out, total, nil
	}
	type agg struct {
		CustomerID uint
		N          int
		Balance    float64
	}
	var orders []agg
	if err := s.db.Model(&database.EquipOrder{}).
		Select("customer_id, COUNT(*) AS n, COALESCE(SUM(balance_amount), 0) AS balance").
		Where("customer_id IN ? AND status <> ?", ids, "anulado").Group("customer_id").Scan(&orders).Error; err != nil {
		return nil, 0, err
	}
	type cred struct {
		CustomerID uint
		Credit     float64
	}
	var credits []cred
	if err := s.db.Model(&database.EquipPayment{}).
		Select("customer_id, COALESCE(SUM(unallocated_amount), 0) AS credit").
		Where("customer_id IN ? AND status = ?", ids, "vigente").Group("customer_id").Scan(&credits).Error; err != nil {
		return nil, 0, err
	}
	byID := map[uint]*CustomerRow{}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	for _, a := range orders {
		if r := byID[a.CustomerID]; r != nil {
			r.Orders, r.Balance = a.N, round2(a.Balance)
		}
	}
	for _, c := range credits {
		if r := byID[c.CustomerID]; r != nil {
			r.Credit = round2(c.Credit)
		}
	}
	return out, total, nil
}

func (s *Service) CreateCustomer(in CustomerInput) (*database.EquipCustomer, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	var existing database.EquipCustomer
	if found, err := findOne(s.db, &existing, "doc_type = ? AND doc_number = ?", in.DocType, in.DocNumber); err != nil {
		return nil, err
	} else if found {
		return nil, invalid("ya existe un cliente con %s %s: %s", in.DocType, in.DocNumber, existing.Name)
	}
	phone, kind, _ := classifyPhone(in.Phone)
	num := in.DocNumber
	c := &database.EquipCustomer{Name: in.Name, DocType: in.DocType, DocNumber: &num, ContactDNI: in.ContactDNI, Phone: phone, PhoneKind: kind, Notes: in.Notes}
	if err := s.db.Create(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) UpdateCustomer(id uint, in CustomerInput) (*database.EquipCustomer, error) {
	var c database.EquipCustomer
	if err := s.db.First(&c, id).Error; err != nil {
		return nil, invalid("cliente no encontrado")
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	var other database.EquipCustomer
	if found, err := findOne(s.db, &other, "doc_type = ? AND doc_number = ? AND id <> ?", in.DocType, in.DocNumber, id); err != nil {
		return nil, err
	} else if found {
		return nil, invalid("otro cliente ya usa %s %s: %s", in.DocType, in.DocNumber, other.Name)
	}
	phone, kind, _ := classifyPhone(in.Phone)
	num := in.DocNumber
	c.Name, c.DocType, c.DocNumber, c.ContactDNI, c.Phone, c.PhoneKind, c.Notes = in.Name, in.DocType, &num, in.ContactDNI, phone, kind, in.Notes
	if err := s.db.Save(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}
