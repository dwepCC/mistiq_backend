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
		"currency":         input.Currency,
		"tax_rate":         input.TaxRate,
		"additional_notes": strings.TrimSpace(input.AdditionalNotes),
		"detraction_bn_account":            strings.TrimSpace(input.DetractionBNAccount),
		"detraction_default_payment_method": normalizeDetractionPaymentMethod(input.DetractionDefaultPaymentMethod),
	}
	// color_theme solo desde panel tenant; Tukichef y otros clientes no deben vaciarlo.
	if strings.TrimSpace(input.ColorTheme) != "" {
		updates["color_theme"] = input.ColorTheme
	}
	return s.db.Model(&existing).Updates(updates).Error
}

func normalizeDetractionPaymentMethod(raw string) string {
	code := strings.TrimSpace(raw)
	if code == "" {
		return "001"
	}
	return code
}

// SaveReceiptWallet guarda QR Yape/Plin y cuentas bancarias visibles en comprobantes.
func (s *CompanyService) SaveReceiptWallet(provider, phone, qrURL string, showOnA4, showOnTicket bool, bankAccountIDs []uint) error {
	var existing database.TenantCompanyConfig
	if err := s.db.First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("configure primero los datos generales de la empresa")
	}
	provider = strings.TrimSpace(strings.ToLower(provider))
	phone = strings.TrimSpace(phone)
	qrURL = strings.TrimSpace(qrURL)
	if provider != "" && (phone == "" || qrURL == "") {
		return errors.New("indique número y QR si elige Yape o Plin")
	}
	if provider != "" && provider != "yape" && provider != "plin" {
		return errors.New("billetera inválida (use yape o plin)")
	}
	const maxInlineDataURL = 120_000
	if strings.HasPrefix(qrURL, "data:") && len(qrURL) > maxInlineDataURL {
		return errors.New("el QR es demasiado grande: use el botón Subir QR (se guardará como archivo en el servidor)")
	}
	return s.db.Model(&existing).Updates(map[string]interface{}{
		"wallet_provider":            provider,
		"wallet_phone":               phone,
		"wallet_qr_url":              qrURL,
		"wallet_show_on_a4":          showOnA4,
		"wallet_show_on_ticket":      showOnTicket,
		"receipt_bank_account_ids":   EncodeReceiptBankAccountIDs(bankAccountIDs),
	}).Error
}

// UpdateWalletQrURL persiste solo la ruta pública del QR (/uploads/tenants/{RUC}/receipts/...).
func (s *CompanyService) UpdateWalletQrURL(url string) error {
	var existing database.TenantCompanyConfig
	if err := s.db.First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("configure primero los datos generales de la empresa")
	}
	return s.db.Model(&existing).Update("wallet_qr_url", strings.TrimSpace(url)).Error
}

// SaveSunatConfigTenant guarda solo los campos que el tenant puede editar: IGV, régimen, zona beneficio.
// sunat_enabled se controla desde el panel central; el tenant no puede activar/desactivar la facturación electrónica.
func (s *CompanyService) SaveSunatConfigTenant(taxRate float64, igvRegime string, taxBenefitZone bool) error {
	var existing database.TenantCompanyConfig
	if err := s.db.First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("configure primero los datos generales de la empresa")
	}
	switch taxRate {
	case 18, 10.5:
	default:
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
// PFX o PEM se convierten en Go a certificate_base64 (PEM combinado) como espera Lycet.
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

func (s *CompanyService) CreateBranch(name, address, phone, fiscalDomicileCode string, isMain bool) (*database.TenantBranch, error) {
	if name == "" {
		return nil, errors.New("el nombre de la sucursal es requerido")
	}
	if isMain {
		s.db.Model(&database.TenantBranch{}).Update("is_main", false)
	}
	b := &database.TenantBranch{
		Name:               name,
		Address:            address,
		Phone:              phone,
		FiscalDomicileCode: strings.TrimSpace(fiscalDomicileCode),
		IsMain:             isMain,
		Active:             true,
	}
	err := s.db.Create(b).Error
	return b, err
}

func (s *CompanyService) UpdateBranch(id uint, name, address, phone, fiscalDomicileCode string, isMain bool) error {
	if isMain {
		s.db.Model(&database.TenantBranch{}).Where("id != ?", id).Update("is_main", false)
	}
	return s.db.Model(&database.TenantBranch{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":                 name,
		"address":              address,
		"phone":                phone,
		"fiscal_domicile_code": strings.TrimSpace(fiscalDomicileCode),
		"is_main":              isMain,
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
	if err := docseries.ValidateSeriesConfig(category, sunatCode, seriesName); err != nil {
		return err
	}
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

func (s *CompanyService) seriesUsage(id uint) (salesCount int64, ser *database.TenantDocumentSeries, err error) {
	var row database.TenantDocumentSeries
	if err = s.db.First(&row, id).Error; err != nil {
		return 0, nil, err
	}
	if err = s.db.Model(&database.TenantSale{}).Where("series_id = ?", id).Count(&salesCount).Error; err != nil {
		return 0, nil, err
	}
	return salesCount, &row, nil
}

func (s *CompanyService) isSeriesLocked(id uint) (bool, int64, error) {
	salesCount, ser, err := s.seriesUsage(id)
	if err != nil {
		return false, 0, err
	}
	return ser.Correlative > 1 || salesCount > 0, salesCount, nil
}

// SeriesListItem enriquece la serie con metadatos de uso documentario.
type SeriesListItem struct {
	database.TenantDocumentSeries
	Locked     bool  `json:"locked"`
	SalesCount int64 `json:"sales_count"`
	CanDelete  bool  `json:"can_delete"`
}

func (s *CompanyService) ListSeriesEnriched(branchID uint) ([]SeriesListItem, error) {
	series, err := s.ListSeries(branchID)
	if err != nil {
		return nil, err
	}
	out := make([]SeriesListItem, 0, len(series))
	for _, row := range series {
		var salesCount int64
		_ = s.db.Model(&database.TenantSale{}).Where("series_id = ?", row.ID).Count(&salesCount).Error
		locked := row.Correlative > 1 || salesCount > 0
		out = append(out, SeriesListItem{
			TenantDocumentSeries: row,
			Locked:               locked,
			SalesCount:           salesCount,
			CanDelete:            !locked,
		})
	}
	return out, nil
}

func (s *CompanyService) DeleteSeries(id uint) error {
	locked, salesCount, err := s.isSeriesLocked(id)
	if err != nil {
		return err
	}
	if locked || salesCount > 0 {
		return errors.New("no se puede eliminar una serie con documentos emitidos o numeración iniciada")
	}
	return s.db.Delete(&database.TenantDocumentSeries{}, id).Error
}

func (s *CompanyService) UpdateSeries(id uint, seriesName string, active bool, docType, sunatCode, category string, correlative *uint) error {
	locked, _, err := s.isSeriesLocked(id)
	if err != nil {
		return err
	}
	if locked {
		var existing database.TenantDocumentSeries
		if err := s.db.First(&existing, id).Error; err != nil {
			return err
		}
		// Solo permitir cambiar estado activo cuando la serie ya tiene uso.
		if seriesName != "" && docseries.NormalizeSeriesCode(seriesName) != existing.Series {
			return errors.New("no se puede modificar la serie: ya tiene documentos emitidos o numeración iniciada")
		}
		if docType != "" && docType != existing.DocType {
			return errors.New("no se puede modificar el tipo de documento: la serie ya está en uso")
		}
		if sunatCode != "" && sunatCode != existing.SunatCode {
			return errors.New("no se puede modificar el código SUNAT: la serie ya está en uso")
		}
		if category != "" && category != existing.Category {
			return errors.New("no se puede modificar la categoría: la serie ya está en uso")
		}
		if correlative != nil && *correlative != existing.Correlative {
			return errors.New("no se puede modificar el correlativo: la serie ya está en uso")
		}
		return s.db.Model(&database.TenantDocumentSeries{}).Where("id = ?", id).Update("active", active).Error
	}

	seriesName = docseries.NormalizeSeriesCode(seriesName)
	if seriesName != "" {
		if err := s.assertSeriesCodeUnique(seriesName, id); err != nil {
			return err
		}
	}
	var existing database.TenantDocumentSeries
	if err := s.db.First(&existing, id).Error; err != nil {
		return err
	}
	finalName := existing.Series
	if seriesName != "" {
		finalName = seriesName
	}
	finalCat := existing.Category
	if category != "" {
		finalCat = category
	}
	finalSunat := existing.SunatCode
	if sunatCode != "" {
		finalSunat = sunatCode
	}
	if err := docseries.ValidateSeriesConfig(finalCat, finalSunat, finalName); err != nil {
		return err
	}
	updates := map[string]interface{}{"active": active}
	if seriesName != "" {
		updates["series"] = seriesName
	}
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
