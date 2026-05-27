package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/facturador"

	"gorm.io/gorm"
)

type CompanyService struct {
	db         *gorm.DB
	tenantID   uint
	tenantSlug string
}

func NewCompanyService(db *gorm.DB) *CompanyService {
	return &CompanyService{db: db}
}

// WithSaaSContext adjunta tenant central para sync fiscal con facturador.
func (s *CompanyService) WithSaaSContext(tenantID uint, tenantSlug string) *CompanyService {
	cp := *s
	cp.tenantID = tenantID
	cp.tenantSlug = strings.TrimSpace(tenantSlug)
	return &cp
}

func (s *CompanyService) GetConfig() (*database.TenantCompanyConfig, error) {
	var cfg database.TenantCompanyConfig
	if err := s.db.First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &database.TenantCompanyConfig{Currency: "PEN", TaxRate: 18, IgvRegime: "standard"}, nil
		}
		return nil, err
	}
	// Garantizar defaults si campos nuevos están vacíos (registros antiguos)
	if cfg.TaxRate == 0 {
		cfg.TaxRate = 18
	}
	if cfg.IgvRegime == "" {
		cfg.IgvRegime = "standard"
	}
	if strings.TrimSpace(cfg.SendMode) == "" {
		cfg.SendMode = "sunat_direct"
	}
	return &cfg, nil
}

func (s *CompanyService) SaveConfig(input database.TenantCompanyConfig) error {
	var existing database.TenantCompanyConfig
	if err := s.db.First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.Create(&input).Error
	}
	updates := map[string]interface{}{
		// Razón Social y RUC no se actualizan desde el panel tenant; solo desde el panel central.
		"trade_name": input.TradeName,
		"address":    input.Address,
		"ubigeo":     input.Ubigeo,
		"country":    input.Country,
		"phone":      input.Phone,
		"email":      input.Email,
		"website":    input.Website,
		"logo_url":   input.LogoURL,
		"currency":   input.Currency,
		"tax_rate":   input.TaxRate,
	}
	// color_theme solo desde panel tenant; Tukichef y otros clientes no deben vaciarlo.
	if strings.TrimSpace(input.ColorTheme) != "" {
		updates["color_theme"] = input.ColorTheme
	}
	return s.db.Model(&existing).Updates(updates).Error
}

// SaveSunatConfigTenant guarda solo los campos que el tenant puede editar: IGV, régimen, zona beneficio.
// sunat_enabled se controla desde el panel central; el tenant no puede activar/desactivar la facturación electrónica.
func (s *CompanyService) SaveSunatConfigTenant(taxRate float64, igvRegime string, taxBenefitZone bool) error {
	var existing database.TenantCompanyConfig
	if err := s.db.First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("configure primero los datos generales de la empresa")
	}
	if taxRate <= 0 {
		taxRate = 18
	}
	if igvRegime == "" {
		igvRegime = "standard"
	}
	return s.db.Model(&existing).Updates(map[string]interface{}{
		"tax_rate":         taxRate,
		"igv_regime":       igvRegime,
		"tax_benefit_zone": taxBenefitZone,
	}).Error
}

func (s *CompanyService) SyncFacturadorConfig() error {
	return s.syncFacturador("", "", "", "", "", "", "")
}

// SyncFacturadorConfigWithFiles envía configuración al facturador.
// Certificados: PFX (.pfx/.p12) o PEM (combinado o clave + cert) se normalizan al formato Greenter antes del envío.
func (s *CompanyService) SyncFacturadorConfigWithFiles(certificateBase64, privateKeyBase64, logoBase64, solUserOverride, solPassOverride, certPassword, pfxBase64 string) error {
	return s.syncFacturador(certificateBase64, privateKeyBase64, logoBase64, solUserOverride, solPassOverride, certPassword, pfxBase64)
}

func (s *CompanyService) syncFacturador(certificateBase64, privateKeyBase64, logoBase64, solUserOverride, solPassOverride, certPassword, pfxBase64 string) error {
	combined, err := facturador.PrepareGreenterCertificateBase64(pfxBase64, certPassword, privateKeyBase64, certificateBase64)
	if err != nil {
		return err
	}
	if combined != "" {
		certificateBase64 = combined
	}
	var cfg database.TenantCompanyConfig
	if err := s.db.First(&cfg).Error; err != nil {
		return errors.New("configure primero los datos de la empresa y SUNAT")
	}
	provider := strings.TrimSpace(cfg.FiscalProvider)
	sendMode := cfg.SendMode
	_, err = s.SyncFiscalToFacturador(FiscalSyncInput{
		SendMode:       sendMode,
		Provider:       provider,
		ConnectionType: cfg.FiscalConnectionType,
		SOLUser:        solUserOverride,
		SOLPass:        solPassOverride,
		CertificateB64: certificateBase64,
		CertPassword:   certPassword,
		LogoB64:        logoBase64,
		Enabled:        cfg.SunatEnabled,
	})
	return err
}

// IsSunatEnabled indica si la empresa tiene activa la conexión con SUNAT.
func (s *CompanyService) IsSunatEnabled() bool {
	var cfg database.TenantCompanyConfig
	if err := s.db.Select("sunat_enabled").First(&cfg).Error; err != nil {
		return false
	}
	return cfg.SunatEnabled
}

// Sucursales
func (s *CompanyService) ListBranches() ([]database.TenantBranch, error) {
	var branches []database.TenantBranch
	err := s.db.Order("is_main DESC, name ASC").Find(&branches).Error
	return branches, err
}

func (s *CompanyService) GetBranch(id uint) (*database.TenantBranch, error) {
	var b database.TenantBranch
	err := s.db.First(&b, id).Error
	return &b, err
}

func (s *CompanyService) CreateBranch(name, address, phone string, isMain bool) (*database.TenantBranch, error) {
	if name == "" {
		return nil, errors.New("el nombre de la sucursal es requerido")
	}
	if isMain {
		s.db.Model(&database.TenantBranch{}).Update("is_main", false)
	}
	b := &database.TenantBranch{Name: name, Address: address, Phone: phone, IsMain: isMain, Active: true}
	err := s.db.Create(b).Error
	return b, err
}

func (s *CompanyService) UpdateBranch(id uint, name, address, phone string, isMain bool) error {
	if isMain {
		s.db.Model(&database.TenantBranch{}).Where("id != ?", id).Update("is_main", false)
	}
	return s.db.Model(&database.TenantBranch{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":    name,
		"address": address,
		"phone":   phone,
		"is_main": isMain,
	}).Error
}

func (s *CompanyService) DeleteBranch(id uint) error {
	return s.db.Delete(&database.TenantBranch{}, id).Error
}

// Series y correlativos
func (s *CompanyService) ListSeries(branchID uint) ([]database.TenantDocumentSeries, error) {
	var series []database.TenantDocumentSeries
	q := s.db.Model(&database.TenantDocumentSeries{})
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	err := q.Order("branch_id ASC, doc_type ASC").Find(&series).Error
	return series, err
}

func (s *CompanyService) assertSeriesCodeUnique(seriesName string, excludeID uint) error {
	code := docseries.NormalizeSeriesCode(seriesName)
	if code == "" {
		return errors.New("código de serie inválido")
	}
	q := s.db.Model(&database.TenantDocumentSeries{}).Where("series = ?", code)
	if excludeID > 0 {
		q = q.Where("id != ?", excludeID)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return docseries.ErrSeriesDuplicate
	}
	return nil
}

func (s *CompanyService) CreateSeries(branchID uint, docType, sunatCode, category, seriesName string) error {
	if seriesName == "" || docType == "" {
		return errors.New("serie y tipo de documento son requeridos")
	}
	if category == "" {
		category = "venta"
	}
	if sunatCode == "" {
		sunatCode = "01"
	}
	seriesName = docseries.NormalizeSeriesCode(seriesName)
	if err := s.assertSeriesCodeUnique(seriesName, 0); err != nil {
		return err
	}
	return s.db.Create(&database.TenantDocumentSeries{
		BranchID:    branchID,
		DocType:     docType,
		SunatCode:   sunatCode,
		Category:    category,
		Series:      seriesName,
		Correlative: 1,
		Active:      true,
	}).Error
}

func (s *CompanyService) UpdateSeries(id uint, seriesName string, active bool, docType, sunatCode, category string, correlative *uint) error {
	seriesName = docseries.NormalizeSeriesCode(seriesName)
	if seriesName != "" {
		if err := s.assertSeriesCodeUnique(seriesName, id); err != nil {
			return err
		}
	}
	updates := map[string]interface{}{"series": seriesName, "active": active}
	if docType != "" {
		updates["doc_type"] = docType
	}
	if sunatCode != "" {
		updates["sunat_code"] = sunatCode
	}
	if category != "" {
		updates["category"] = category
	}
	if correlative != nil {
		updates["correlative"] = *correlative
	}
	return s.db.Model(&database.TenantDocumentSeries{}).Where("id = ?", id).Updates(updates).Error
}
