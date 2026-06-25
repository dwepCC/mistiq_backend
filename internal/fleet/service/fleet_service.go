package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type FleetService struct {
	db *gorm.DB
}

func NewFleetService(db *gorm.DB) *FleetService {
	return &FleetService{db: db}
}

type CarrierInput struct {
	DocType       string `json:"doc_type"`
	DocNumber     string `json:"doc_number"`
	BusinessName  string `json:"business_name"`
	FiscalAddress string `json:"fiscal_address"`
	MTCNumber     string `json:"mtc_number"`
	IsDefault     bool   `json:"is_default"`
	Active        bool   `json:"active"`
}

type DriverInput struct {
	DocType       string `json:"doc_type"`
	DocNumber     string `json:"doc_number"`
	FullName      string `json:"full_name"`
	LicenseNumber string `json:"license_number"`
	Phone         string `json:"phone"`
	CarrierID     *uint  `json:"carrier_id"`
	IsDefault     bool   `json:"is_default"`
	Active        bool   `json:"active"`
}

type VehicleInput struct {
	Plate            string `json:"plate"`
	Brand            string `json:"brand"`
	Model            string `json:"model"`
	HabilitationCert string `json:"habilitation_cert"`
	CarrierID        *uint  `json:"carrier_id"`
	IsDefault        bool   `json:"is_default"`
	Active           bool   `json:"active"`
}

func normalizeDocType(v string, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func (s *FleetService) ListCarriers(q string, activeOnly bool) ([]database.TenantGreCarrier, error) {
	var list []database.TenantGreCarrier
	tx := s.db.Order("is_default DESC, business_name ASC")
	if activeOnly {
		tx = tx.Where("active = ?", true)
	}
	q = strings.TrimSpace(q)
	if q != "" {
		like := "%" + q + "%"
		tx = tx.Where("doc_number LIKE ? OR business_name LIKE ?", like, like)
	}
	if err := tx.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (s *FleetService) GetCarrier(id uint) (*database.TenantGreCarrier, error) {
	var row database.TenantGreCarrier
	if err := s.db.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *FleetService) CreateCarrier(in CarrierInput) (*database.TenantGreCarrier, error) {
	if err := validateCarrierInput(in); err != nil {
		return nil, err
	}
	row := database.TenantGreCarrier{
		DocType:       normalizeDocType(in.DocType, "6"),
		DocNumber:     strings.TrimSpace(in.DocNumber),
		BusinessName:  strings.TrimSpace(in.BusinessName),
		FiscalAddress: strings.TrimSpace(in.FiscalAddress),
		MTCNumber:     strings.TrimSpace(in.MTCNumber),
		IsDefault:     in.IsDefault,
		Active:        true,
	}
	return s.saveCarrier(&row, in.IsDefault)
}

func (s *FleetService) UpdateCarrier(id uint, in CarrierInput) (*database.TenantGreCarrier, error) {
	row, err := s.GetCarrier(id)
	if err != nil {
		return nil, err
	}
	if err := validateCarrierInput(in); err != nil {
		return nil, err
	}
	row.DocType = normalizeDocType(in.DocType, "6")
	row.DocNumber = strings.TrimSpace(in.DocNumber)
	row.BusinessName = strings.TrimSpace(in.BusinessName)
	row.FiscalAddress = strings.TrimSpace(in.FiscalAddress)
	row.MTCNumber = strings.TrimSpace(in.MTCNumber)
	row.IsDefault = in.IsDefault
	row.Active = in.Active
	return s.saveCarrier(row, in.IsDefault)
}

func (s *FleetService) ToggleCarrier(id uint) error {
	row, err := s.GetCarrier(id)
	if err != nil {
		return err
	}
	return s.db.Model(row).Update("active", !row.Active).Error
}

func (s *FleetService) saveCarrier(row *database.TenantGreCarrier, isDefault bool) (*database.TenantGreCarrier, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if isDefault {
			if err := tx.Model(&database.TenantGreCarrier{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		row.IsDefault = isDefault
		if row.ID == 0 {
			return tx.Create(row).Error
		}
		return tx.Save(row).Error
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

func validateCarrierInput(in CarrierInput) error {
	if strings.TrimSpace(in.DocNumber) == "" {
		return errors.New("número de documento es obligatorio")
	}
	if strings.TrimSpace(in.BusinessName) == "" {
		return errors.New("nombre o razón social es obligatorio")
	}
	docType := normalizeDocType(in.DocType, "6")
	if docType == "6" && len(strings.TrimSpace(in.DocNumber)) != 11 {
		return errors.New("RUC del transportista debe tener 11 dígitos")
	}
	return nil
}

func (s *FleetService) ListDrivers(q string, activeOnly bool, carrierID *uint) ([]database.TenantGreDriver, error) {
	var list []database.TenantGreDriver
	tx := s.db.Order("is_default DESC, full_name ASC")
	if activeOnly {
		tx = tx.Where("active = ?", true)
	}
	if carrierID != nil && *carrierID > 0 {
		tx = tx.Where("carrier_id = ? OR carrier_id IS NULL", *carrierID)
	}
	q = strings.TrimSpace(q)
	if q != "" {
		like := "%" + q + "%"
		tx = tx.Where("doc_number LIKE ? OR full_name LIKE ?", like, like)
	}
	if err := tx.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (s *FleetService) GetDriver(id uint) (*database.TenantGreDriver, error) {
	var row database.TenantGreDriver
	if err := s.db.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *FleetService) CreateDriver(in DriverInput) (*database.TenantGreDriver, error) {
	if err := validateDriverInput(in); err != nil {
		return nil, err
	}
	row := database.TenantGreDriver{
		DocType:       normalizeDocType(in.DocType, "1"),
		DocNumber:     strings.TrimSpace(in.DocNumber),
		FullName:      strings.TrimSpace(in.FullName),
		LicenseNumber: strings.TrimSpace(in.LicenseNumber),
		Phone:         strings.TrimSpace(in.Phone),
		CarrierID:     in.CarrierID,
		IsDefault:     in.IsDefault,
		Active:        true,
	}
	return s.saveDriver(&row, in.IsDefault)
}

func (s *FleetService) UpdateDriver(id uint, in DriverInput) (*database.TenantGreDriver, error) {
	row, err := s.GetDriver(id)
	if err != nil {
		return nil, err
	}
	if err := validateDriverInput(in); err != nil {
		return nil, err
	}
	row.DocType = normalizeDocType(in.DocType, "1")
	row.DocNumber = strings.TrimSpace(in.DocNumber)
	row.FullName = strings.TrimSpace(in.FullName)
	row.LicenseNumber = strings.TrimSpace(in.LicenseNumber)
	row.Phone = strings.TrimSpace(in.Phone)
	row.CarrierID = in.CarrierID
	row.Active = in.Active
	return s.saveDriver(row, in.IsDefault)
}

func (s *FleetService) ToggleDriver(id uint) error {
	row, err := s.GetDriver(id)
	if err != nil {
		return err
	}
	return s.db.Model(row).Update("active", !row.Active).Error
}

func (s *FleetService) saveDriver(row *database.TenantGreDriver, isDefault bool) (*database.TenantGreDriver, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if isDefault {
			if err := tx.Model(&database.TenantGreDriver{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		row.IsDefault = isDefault
		if row.ID == 0 {
			return tx.Create(row).Error
		}
		return tx.Save(row).Error
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

func validateDriverInput(in DriverInput) error {
	if strings.TrimSpace(in.DocNumber) == "" {
		return errors.New("número de documento del conductor es obligatorio")
	}
	if strings.TrimSpace(in.FullName) == "" {
		return errors.New("nombre del conductor es obligatorio")
	}
	if strings.TrimSpace(in.LicenseNumber) == "" {
		return errors.New("licencia de conducir es obligatoria")
	}
	return nil
}

func (s *FleetService) ListVehicles(q string, activeOnly bool, carrierID *uint) ([]database.TenantGreVehicle, error) {
	var list []database.TenantGreVehicle
	tx := s.db.Order("is_default DESC, plate ASC")
	if activeOnly {
		tx = tx.Where("active = ?", true)
	}
	if carrierID != nil && *carrierID > 0 {
		tx = tx.Where("carrier_id = ? OR carrier_id IS NULL", *carrierID)
	}
	q = strings.TrimSpace(q)
	if q != "" {
		like := "%" + strings.ToUpper(q) + "%"
		tx = tx.Where("UPPER(plate) LIKE ? OR UPPER(brand) LIKE ? OR UPPER(model) LIKE ?", like, like, like)
	}
	if err := tx.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (s *FleetService) GetVehicle(id uint) (*database.TenantGreVehicle, error) {
	var row database.TenantGreVehicle
	if err := s.db.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *FleetService) CreateVehicle(in VehicleInput) (*database.TenantGreVehicle, error) {
	if err := validateVehicleInput(in); err != nil {
		return nil, err
	}
	row := database.TenantGreVehicle{
		Plate:            strings.ToUpper(strings.TrimSpace(in.Plate)),
		Brand:            strings.TrimSpace(in.Brand),
		Model:            strings.TrimSpace(in.Model),
		HabilitationCert: strings.TrimSpace(in.HabilitationCert),
		CarrierID:        in.CarrierID,
		IsDefault:        in.IsDefault,
		Active:           true,
	}
	return s.saveVehicle(&row, in.IsDefault)
}

func (s *FleetService) UpdateVehicle(id uint, in VehicleInput) (*database.TenantGreVehicle, error) {
	row, err := s.GetVehicle(id)
	if err != nil {
		return nil, err
	}
	if err := validateVehicleInput(in); err != nil {
		return nil, err
	}
	row.Plate = strings.ToUpper(strings.TrimSpace(in.Plate))
	row.Brand = strings.TrimSpace(in.Brand)
	row.Model = strings.TrimSpace(in.Model)
	row.HabilitationCert = strings.TrimSpace(in.HabilitationCert)
	row.CarrierID = in.CarrierID
	row.Active = in.Active
	return s.saveVehicle(row, in.IsDefault)
}

func (s *FleetService) ToggleVehicle(id uint) error {
	row, err := s.GetVehicle(id)
	if err != nil {
		return err
	}
	return s.db.Model(row).Update("active", !row.Active).Error
}

func (s *FleetService) saveVehicle(row *database.TenantGreVehicle, isDefault bool) (*database.TenantGreVehicle, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if isDefault {
			if err := tx.Model(&database.TenantGreVehicle{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		row.IsDefault = isDefault
		if row.ID == 0 {
			return tx.Create(row).Error
		}
		return tx.Save(row).Error
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

func validateVehicleInput(in VehicleInput) error {
	if strings.TrimSpace(in.Plate) == "" {
		return errors.New("placa del vehículo es obligatoria")
	}
	return nil
}

type FleetDefaults struct {
	Carrier *database.TenantGreCarrier `json:"carrier,omitempty"`
	Driver  *database.TenantGreDriver  `json:"driver,omitempty"`
	Vehicle *database.TenantGreVehicle `json:"vehicle,omitempty"`
}

func (s *FleetService) GetDefaults() FleetDefaults {
	out := FleetDefaults{}
	var c database.TenantGreCarrier
	if s.db.Where("is_default = ? AND active = ?", true, true).First(&c).Error == nil {
		out.Carrier = &c
	}
	var d database.TenantGreDriver
	if s.db.Where("is_default = ? AND active = ?", true, true).First(&d).Error == nil {
		out.Driver = &d
	}
	var v database.TenantGreVehicle
	if s.db.Where("is_default = ? AND active = ?", true, true).First(&v).Error == nil {
		out.Vehicle = &v
	}
	return out
}
