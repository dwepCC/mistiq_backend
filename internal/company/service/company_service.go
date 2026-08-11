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

// CompanyConfigPatch campos opcionales para PUT /api/company/config (solo actualiza lo enviado).
type CompanyConfigPatch struct {
	TradeName                      *string  `json:"trade_name"`
	Address                        *string  `json:"address"`
	Ubigeo                         *string  `json:"ubigeo"`
	Country                        *string  `json:"country"`
	Phone                          *string  `json:"phone"`
	Email                          *string  `json:"email"`
	Website                        *string  `json:"website"`
	Currency                       *string  `json:"currency"`
	TaxRate                        *float64 `json:"tax_rate"`
	AdditionalNotes                *string  `json:"additional_notes"`
	TermsAndConditions             *string  `json:"terms_and_conditions"`
	ShowTermsConditions            *bool    `json:"show_terms_conditions"`
	DetractionBNAccount            *string  `json:"detraction_bn_account"`
	DetractionDefaultPaymentMethod *string  `json:"detraction_default_payment_method"`
	ColorTheme                     *string  `json:"color_theme"`
	LogoURL                        *string  `json:"logo_url"`
}

func (s *CompanyService) ApplyConfigPatch(patch CompanyConfigPatch) error {
	var existing database.TenantCompanyConfig
	if err := s.db.First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("configure primero los datos generales de la empresa")
	}
	updates := map[string]interface{}{}
	if patch.TradeName != nil {
		updates["trade_name"] = strings.TrimSpace(*patch.TradeName)
	}
	if patch.Address != nil {
		updates["address"] = strings.TrimSpace(*patch.Address)
	}
	if patch.Ubigeo != nil {
		updates["ubigeo"] = strings.TrimSpace(*patch.Ubigeo)
	}
	if patch.Country != nil {
		updates["country"] = strings.TrimSpace(*patch.Country)
	}
	if patch.Phone != nil {
		updates["phone"] = strings.TrimSpace(*patch.Phone)
	}
	if patch.Email != nil {
		updates["email"] = strings.TrimSpace(*patch.Email)
	}
	if patch.Website != nil {
		updates["website"] = strings.TrimSpace(*patch.Website)
	}
	if patch.Currency != nil {
		if c := strings.TrimSpace(*patch.Currency); c != "" {
			updates["currency"] = c
		}
	}
	if patch.TaxRate != nil && *patch.TaxRate > 0 {
		updates["tax_rate"] = *patch.TaxRate
	}
	if patch.AdditionalNotes != nil {
		updates["additional_notes"] = strings.TrimSpace(*patch.AdditionalNotes)
	}
	if patch.TermsAndConditions != nil {
		updates["terms_and_conditions"] = strings.TrimSpace(*patch.TermsAndConditions)
	}
	if patch.ShowTermsConditions != nil {
		updates["show_terms_conditions"] = *patch.ShowTermsConditions
	}
	if patch.DetractionBNAccount != nil {
		updates["detraction_bn_account"] = strings.TrimSpace(*patch.DetractionBNAccount)
	}
	if patch.DetractionDefaultPaymentMethod != nil {
		updates["detraction_default_payment_method"] = normalizeDetractionPaymentMethod(*patch.DetractionDefaultPaymentMethod)
	}
	if patch.ColorTheme != nil {
		if theme := strings.TrimSpace(*patch.ColorTheme); theme != "" {
			updates["color_theme"] = theme
		}
	}
	if patch.LogoURL != nil {
		if logo := strings.TrimSpace(*patch.LogoURL); logo != "" {
			updates["logo_url"] = logo
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return s.db.Model(&existing).Updates(updates).Error
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
		"currency":         input.Currency,
		"tax_rate":         input.TaxRate,
		"additional_notes": strings.TrimSpace(input.AdditionalNotes),
		"terms_and_conditions":  strings.TrimSpace(input.TermsAndConditions),
		"show_terms_conditions": input.ShowTermsConditions,
		"detraction_bn_account":            strings.TrimSpace(input.DetractionBNAccount),
		"detraction_default_payment_method": normalizeDetractionPaymentMethod(input.DetractionDefaultPaymentMethod),
	}
	// color_theme solo desde panel tenant; Tukichef y otros clientes no deben vaciarlo.
	if strings.TrimSpace(input.ColorTheme) != "" {
		updates["color_theme"] = input.ColorTheme
	}
	if logoURL := strings.TrimSpace(input.LogoURL); logoURL != "" {
		updates["logo_url"] = logoURL
	}
	return s.db.Model(&existing).Updates(updates).Error
}

// UpdateLogoURL persiste la ruta pública del logo (/uploads/tenants/{RUC}/company/...).
func (s *CompanyService) UpdateLogoURL(url string) error {
	var existing database.TenantCompanyConfig
	if err := s.db.First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("configure primero los datos generales de la empresa")
	}
	return s.db.Model(&existing).Update("logo_url", strings.TrimSpace(url)).Error
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
	if err := s.db.Create(b).Error; err != nil {
		return nil, err
	}
	if err := database.SeedInventoryDocumentSeriesForBranch(s.db, b.ID); err != nil {
		return nil, err
	}
	return b, nil
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

func (s *CompanyService) assertSeriesCodeUnique(branchID uint, category, seriesName string, excludeID uint) error {
	code := docseries.NormalizeSeriesCode(seriesName)
	if code == "" {
		return errors.New("código de serie inválido")
	}
	q := s.db.Model(&database.TenantDocumentSeries{}).Where("series = ?", code)
	// Series de almacén: código único dentro de la sucursal (puede repetirse en otra sucursal).
	if strings.TrimSpace(strings.ToLower(category)) == "almacen" {
		if branchID == 0 {
			return errors.New("sucursal requerida para series de almacén")
		}
		q = q.Where("branch_id = ?", branchID)
	}
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

func (s *CompanyService) CreateSeries(branchID uint, docType, seriesName string, correlative *uint) error {
	if seriesName == "" || docType == "" {
		return errors.New("serie y tipo de documento son requeridos")
	}
	var err error
	var documentCode, category string
	docType, documentCode, category, err = docseries.NormalizeSeriesDocumentInput(docType)
	if err != nil {
		return err
	}
	seriesName = docseries.NormalizeSeriesCode(seriesName)
	if err := docseries.ValidateSeriesConfig(docType, category, documentCode, seriesName); err != nil {
		return err
	}
	if err := s.assertSeriesCodeUnique(branchID, category, seriesName, 0); err != nil {
		return err
	}
	startCorrelative := uint(1)
	if correlative != nil {
		if *correlative == 0 {
			return errors.New("el correlativo inicial debe ser mayor a 0")
		}
		startCorrelative = *correlative
	}
	return s.db.Create(&database.TenantDocumentSeries{
		BranchID:    branchID,
		DocType:     docType,
		SunatCode:   documentCode,
		Category:    category,
		Series:      seriesName,
		Correlative: startCorrelative,
		Active:      true,
	}).Error
}

func (s *CompanyService) seriesUsageSvc() *SeriesUsageService {
	return NewSeriesUsageService(s.db)
}

// SeriesListItem enriquece la serie con metadatos de uso documentario (API única para el frontend).
type SeriesListItem struct {
	database.TenantDocumentSeries
	Locked      bool   `json:"locked"`
	CanDelete   bool   `json:"can_delete"`
	UsageTable  string `json:"usage_table"`
	UsageCount  int64  `json:"usage_count"`
	UsageReason string `json:"usage_reason"`
}

func (s *CompanyService) ListSeriesEnriched(branchID uint) ([]SeriesListItem, error) {
	series, err := s.ListSeries(branchID)
	if err != nil {
		return nil, err
	}
	usageSvc := s.seriesUsageSvc()
	out := make([]SeriesListItem, 0, len(series))
	for _, row := range series {
		inUse, info, err := usageSvc.IsSeriesInUse(row.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, SeriesListItem{
			TenantDocumentSeries: row,
			Locked:               inUse,
			CanDelete:            !inUse,
			UsageTable:           info.Table,
			UsageCount:           info.Count,
			UsageReason:          info.Reason,
		})
	}
	return out, nil
}

func (s *CompanyService) DeleteSeries(id uint) error {
	inUse, info, err := s.seriesUsageSvc().IsSeriesInUse(id)
	if err != nil {
		return err
	}
	if inUse {
		return errors.New(LockMessageWhenInUse(info))
	}
	return s.db.Delete(&database.TenantDocumentSeries{}, id).Error
}

func (s *CompanyService) UpdateSeries(id uint, seriesName string, active bool, docType string, correlative *uint) error {
	inUse, usageInfo, err := s.seriesUsageSvc().IsSeriesInUse(id)
	if err != nil {
		return err
	}
	lockMsg := LockMessageWhenInUse(usageInfo)
	if inUse {
		var existing database.TenantDocumentSeries
		if err := s.db.First(&existing, id).Error; err != nil {
			return err
		}
		if seriesName != "" && docseries.NormalizeSeriesCode(seriesName) != existing.Series {
			return errors.New(lockMsg)
		}
		if docType != "" {
			incoming, err := docseries.ResolveDocumentType(docType)
			if err != nil {
				return err
			}
			current, err := docseries.ResolveDocumentType(existing.DocType)
			if err != nil {
				return err
			}
			if incoming.ID != current.ID {
				return errors.New(lockMsg)
			}
		}
		if correlative != nil && *correlative != existing.Correlative {
			return errors.New(lockMsg)
		}
		return s.db.Model(&database.TenantDocumentSeries{}).Where("id = ?", id).Update("active", active).Error
	}

	var existing database.TenantDocumentSeries
	if err := s.db.First(&existing, id).Error; err != nil {
		return err
	}
	finalName := existing.Series
	if seriesName != "" {
		finalName = seriesName
	}
	effectiveDocType := existing.DocType
	if docType != "" {
		effectiveDocType = docType
	}
	finalDocType, finalDocumentCode, finalCat, err := docseries.NormalizeSeriesDocumentInput(effectiveDocType)
	if err != nil {
		return err
	}
	if err := docseries.ValidateSeriesConfig(finalDocType, finalCat, finalDocumentCode, finalName); err != nil {
		return err
	}
	if seriesName != "" {
		normalized := docseries.NormalizeSeriesCode(seriesName)
		if err := s.assertSeriesCodeUnique(existing.BranchID, finalCat, normalized, id); err != nil {
			return err
		}
		finalName = normalized
	}
	updates := map[string]interface{}{"active": active}
	if seriesName != "" {
		updates["series"] = finalName
	}
	if docType != "" {
		updates["doc_type"] = finalDocType
		updates["sunat_code"] = finalDocumentCode
		updates["category"] = finalCat
	}
	if correlative != nil {
		updates["correlative"] = *correlative
	}
	return s.db.Model(&database.TenantDocumentSeries{}).Where("id = ?", id).Updates(updates).Error
}
