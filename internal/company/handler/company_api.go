package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tukifac/internal/company/service"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/middleware"
	"tukifac/pkg/saas"
	"tukifac/pkg/taxregime"
	"tukifac/pkg/tenantstorage"
	"tukifac/pkg/uploadlimits"

	"github.com/gofiber/fiber/v3"
)

// GET /api/company/config
func (h *CompanyHandler) GetConfigAPI(c fiber.Ctx) error {
	cfg, err := service.NewCompanyService(db(c)).GetConfig()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// El logo viaja embebido: así cualquier dispositivo lo tiene al iniciar sesión y no
	// depende de poder descargar /uploads por su cuenta.
	if ruc, rucErr := tenantstorage.ResolveTenantRUC(c); rucErr == nil {
		attachLogoDataURL(ruc, cfg)
	}
	return c.JSON(cfg)
}

// PUT /api/company/config
func (h *CompanyHandler) UpdateConfigAPI(c fiber.Ctx) error {
	var patch service.CompanyConfigPatch
	// Unmarshal directo: Bind de Fiber a veces no rellena *string en patches parciales.
	if err := json.Unmarshal(c.Body(), &patch); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	svc := service.NewCompanyService(db(c))
	if err := svc.ApplyConfigPatch(patch); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
		if patch.LogoURL != nil {
			if ruc := tenantstorage.SanitizeRUC(t.RUC); ruc != "" {
				c.Locals("tenant_ruc", ruc)
			}
		}
	}
	// Solo sincronizar logo con Lycet cuando el usuario envió un logo (acción explícita).
	if svc.IsSunatEnabled() && patch.LogoURL != nil {
		logoBase64 := extractBase64FromDataURL(*patch.LogoURL)
		if logoBase64 != "" {
			syncSvc := svc
			if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
				syncSvc = svc.WithSaaSContext(t.ID, t.Slug)
			}
			_ = syncSvc.SyncFacturadorConfigWithFiles("", "", logoBase64, "", "", "", "")
		}
		if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
			_ = database.CentralDB.Model(&database.Tenant{}).Where("id = ?", t.ID).Update("logo_url", strings.TrimSpace(*patch.LogoURL)).Error
		}
	}
	cfg, _ := svc.GetConfig()
	return c.JSON(fiber.Map{"success": true, "data": cfg})
}

// PUT /api/company/receipt-wallet — QR Yape/Plin y cuentas bancarias en comprobantes locales.
func (h *CompanyHandler) UpdateReceiptWalletAPI(c fiber.Ctx) error {
	var body struct {
		WalletProvider        string          `json:"wallet_provider"`
		WalletPhone           string          `json:"wallet_phone"`
		WalletQrURL           string          `json:"wallet_qr_url"`
		WalletShowOnA4        bool            `json:"wallet_show_on_a4"`
		WalletShowOnTicket    bool            `json:"wallet_show_on_ticket"`
		ReceiptBankAccountIDs json.RawMessage `json:"receipt_bank_account_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	bankIDs, err := service.ParseReceiptBankAccountIDsJSON(body.ReceiptBankAccountIDs)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	svc := service.NewCompanyService(db(c))
	if err := svc.SaveReceiptWallet(
		body.WalletProvider, body.WalletPhone, body.WalletQrURL,
		body.WalletShowOnA4, body.WalletShowOnTicket,
		bankIDs,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	cfg, _ := svc.GetConfig()
	return c.JSON(fiber.Map{"success": true, "data": cfg})
}

// UploadReceiptWalletQRAPI POST /api/company/receipt-wallet/qr — imagen en uploads/tenants/{RUC}/receipts/.
func (h *CompanyHandler) UploadReceiptWalletQRAPI(c fiber.Ctx) error {
	ruc, err := tenantstorage.ResolveTenantRUC(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	file, err := c.FormFile("image")
	if err != nil || file == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "envía un archivo en el campo 'image'"})
	}
	if file.Size > uploadlimits.MaxFileBytes {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "la imagen no debe superar 10 MB"})
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	if !allowed[ext] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "formato no permitido. Usa JPG, PNG o WebP"})
	}

	dir := tenantstorage.TenantUploadDir(ruc, "receipts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("no se pudo crear carpeta %s: %v", dir, err),
		})
	}
	filename := "wallet-qr" + ext
	savePath := filepath.Join(dir, filename)
	if err := c.SaveFile(file, savePath); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("error guardando QR en %s: %v", savePath, err),
		})
	}
	imageURL := tenantstorage.TenantUploadPublicURL(ruc, "receipts", filename)
	svc := service.NewCompanyService(db(c))
	if err := svc.UpdateWalletQrURL(imageURL); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "wallet_qr_url": imageURL})
}

// UploadCompanyLogoAPI POST /api/company/logo — imagen en uploads/tenants/{RUC}/company/.
func (h *CompanyHandler) UploadCompanyLogoAPI(c fiber.Ctx) error {
	ruc, err := tenantstorage.ResolveTenantRUC(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	file, err := c.FormFile("image")
	if err != nil || file == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "envía un archivo en el campo 'image'"})
	}
	if file.Size > uploadlimits.MaxFileBytes {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "la imagen no debe superar 10 MB"})
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	if !allowed[ext] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "formato no permitido. Usa JPG, PNG o WebP"})
	}

	dir := tenantstorage.TenantUploadDir(ruc, "company")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("no se pudo crear carpeta %s: %v", dir, err),
		})
	}
	filename := "logo" + ext
	savePath := filepath.Join(dir, filename)
	for _, oldExt := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		if oldExt == ext {
			continue
		}
		_ = os.Remove(filepath.Join(dir, "logo"+oldExt))
	}
	if err := c.SaveFile(file, savePath); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("error guardando logo en %s: %v", savePath, err),
		})
	}
	imageURL := tenantstorage.TenantUploadPublicURL(ruc, "company", filename)
	// ?v= evita caché del navegador al reemplazar el mismo archivo logo.*
	storedURL := fmt.Sprintf("%s?v=%d", imageURL, time.Now().UnixMilli())
	svc := service.NewCompanyService(db(c))
	if err := svc.UpdateLogoURL(storedURL); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if svc.IsSunatEnabled() {
		logoBytes, readErr := os.ReadFile(savePath)
		if readErr == nil && len(logoBytes) > 0 {
			logoBase64 := base64.StdEncoding.EncodeToString(logoBytes)
			syncSvc := svc
			if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
				syncSvc = svc.WithSaaSContext(t.ID, t.Slug)
				_ = database.CentralDB.Model(&database.Tenant{}).Where("id = ?", t.ID).Update("logo_url", storedURL).Error
			}
			_ = syncSvc.SyncFacturadorConfigWithFiles("", "", logoBase64, "", "", "", "")
		}
	}
	cfg, _ := svc.GetConfig()
	attachLogoDataURL(ruc, cfg)
	return c.JSON(fiber.Map{"success": true, "logo_url": storedURL, "data": cfg})
}

// DeleteCompanyLogoAPI DELETE /api/company/logo — quita logo del tenant.
func (h *CompanyHandler) DeleteCompanyLogoAPI(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	if err := svc.UpdateLogoURL(""); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
		_ = database.CentralDB.Model(&database.Tenant{}).Where("id = ?", t.ID).Update("logo_url", "").Error
	}
	cfg, _ := svc.GetConfig()
	return c.JSON(fiber.Map{"success": true, "data": cfg})
}

// extractBase64FromDataURL obtiene el payload base64 de un data URL (ej. "data:image/png;base64,iVBORw...").
func extractBase64FromDataURL(dataURL string) string {
	if dataURL == "" || !strings.HasPrefix(dataURL, "data:") {
		return dataURL
	}
	i := strings.Index(dataURL, ",")
	if i == -1 {
		return ""
	}
	return strings.TrimSpace(dataURL[i+1:])
}

// GET /api/company/sunat — el tenant solo recibe los campos que puede editar (no credenciales SOL ni ambiente).
func (h *CompanyHandler) GetSunatAPI(c fiber.Ctx) error {
	cfg, err := service.NewCompanyService(db(c)).GetConfig()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"sunat_enabled":    cfg.SunatEnabled,
		"tax_rate":         cfg.TaxRate,
		"igv_regime":       cfg.IgvRegime,
		"tax_benefit_zone": cfg.TaxBenefitZone,
		// Régimen tributario del contribuyente + capacidades resueltas. Los
		// frontends consumen `capabilities` y no reimplementan reglas del régimen.
		"taxpayer_regime": taxregime.Normalize(cfg.TaxpayerRegime),
		"capabilities":    taxregime.CapabilitiesFor(cfg.TaxpayerRegime),
	})
}

// GET /api/company/invoicing — metadatos fiscales (sin secretos; modo real en facturador SSOT).
func (h *CompanyHandler) GetInvoicingAPI(c fiber.Ctx) error {
	cfg, err := service.NewCompanyService(db(c)).GetConfig()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	sendMode := cfg.SendMode
	if sendMode == "" {
		sendMode = "sunat_direct"
	}
	return c.JSON(fiber.Map{
		"send_mode":         sendMode,
		"fiscal_enabled":    cfg.SunatEnabled,
		"connection_status": cfg.FiscalConnectionStatus,
	})
}

// PUT /api/company/invoicing
func (h *CompanyHandler) UpdateInvoicingAPI(c fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error": "El modo de facturación solo se configura desde el panel central",
	})
}

// PUT /api/company/sunat — el tenant solo puede editar: IGV, régimen, zona beneficio.
// sunat_enabled se controla desde el panel central; el tenant no puede activar/desactivar la facturación electrónica.
func (h *CompanyHandler) UpdateSunatAPI(c fiber.Ctx) error {
	var body struct {
		TaxRate        float64 `json:"tax_rate"`
		IgvRegime      string  `json:"igv_regime"`
		TaxBenefitZone bool    `json:"tax_benefit_zone"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	svc := service.NewCompanyService(db(c))
	if err := svc.SaveSunatConfigTenant(body.TaxRate, body.IgvRegime, body.TaxBenefitZone); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/company/sync-facturador — envía la configuración SUNAT del tenant al backend facturador (Lycet).
// Body opcional: { "certificate_base64", "private_key_base64", "logo_base64", "sol_user", "sol_pass" }.
// Si envías sol_user/sol_pass se usan para esta sincronización en lugar de lo guardado en BD.
func (h *CompanyHandler) SyncFacturadorAPI(c fiber.Ctx) error {
	var body struct {
		CertificateBase64   string `json:"certificate_base64"`
		PrivateKeyBase64    string `json:"private_key_base64"`
		PfxBase64           string `json:"pfx_base64"`
		CertificatePassword string `json:"certificate_password"`
		LogoBase64          string `json:"logo_base64"`
		SolUser             string `json:"sol_user"`
		SolPass             string `json:"sol_pass"`
	}
	_ = c.Bind().JSON(&body)
	svc := service.NewCompanyService(db(c))
	if t, ok := c.Locals("tenant").(*database.Tenant); ok && t != nil {
		svc = svc.WithSaaSContext(t.ID, t.Slug)
	}
	if body.CertificateBase64 != "" || body.PrivateKeyBase64 != "" || body.LogoBase64 != "" || body.PfxBase64 != "" {
		if err := svc.SyncFacturadorConfigWithFiles(body.CertificateBase64, body.PrivateKeyBase64, body.LogoBase64, body.SolUser, body.SolPass, body.CertificatePassword, body.PfxBase64); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	} else {
		if err := svc.SyncFacturadorConfigWithFiles("", "", "", body.SolUser, body.SolPass, "", ""); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	}
	return c.JSON(fiber.Map{"success": true, "message": "Configuración sincronizada con el facturador"})
}

// GET /api/company/branches
func (h *CompanyHandler) ListBranchesAPI(c fiber.Ctx) error {
	branches, err := service.NewCompanyService(db(c)).ListBranches()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": branches})
}

// POST /api/company/branches
func (h *CompanyHandler) CreateBranchAPI(c fiber.Ctx) error {
	var body struct {
		Name               string `json:"name"`
		Address            string `json:"address"`
		Phone              string `json:"phone"`
		FiscalDomicileCode string `json:"fiscal_domicile_code"`
		IsMain             bool   `json:"is_main"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if middleware.EnforceCreateQuota(c, db(c), saas.QuotaBranches) {
		return nil
	}
	b, err := service.NewCompanyService(db(c)).CreateBranch(body.Name, body.Address, body.Phone, body.FiscalDomicileCode, body.IsMain)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": b})
}

// PUT /api/company/branches/:id
func (h *CompanyHandler) UpdateBranchAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Name               string `json:"name"`
		Address            string `json:"address"`
		Phone              string `json:"phone"`
		FiscalDomicileCode string `json:"fiscal_domicile_code"`
		IsMain             bool   `json:"is_main"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := service.NewCompanyService(db(c)).UpdateBranch(uint(id), body.Name, body.Address, body.Phone, body.FiscalDomicileCode, body.IsMain); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/company/branches/:id
func (h *CompanyHandler) DeleteBranchAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewCompanyService(db(c)).DeleteBranch(uint(id)); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// filterActiveSeries descarta las series desactivadas. Se aplica a todo consumidor que
// vaya a emitir un documento; la pantalla de configuración se salta este filtro.
func filterActiveSeries(series []service.SeriesListItem) []service.SeriesListItem {
	out := make([]service.SeriesListItem, 0, len(series))
	for _, s := range series {
		if s.Active {
			out = append(out, s)
		}
	}
	return out
}

// isTruthyFlag interpreta el valor de un flag booleano de query string. Cualquier valor
// no reconocido es false, para que el filtrado por defecto sea el comportamiento seguro.
func isTruthyFlag(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// queryFlag lee un query param booleano tolerando "1", "true", "yes" y mayúsculas.
func queryFlag(c fiber.Ctx, name string) bool {
	return isTruthyFlag(c.Query(name))
}

// GET /api/company/series?branch_id=1&category=venta&include_inactive=1
func (h *CompanyHandler) ListSeriesAPI(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	branchID, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	series, err := svc.ListSeriesEnriched(uint(branchID))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Por defecto solo series activas: una serie desactivada no debe poder elegirse al
	// emitir una venta, un comprobante o cualquier otro documento. Solo la pantalla de
	// configuración pide include_inactive=1, porque necesita verlas para reactivarlas.
	if !queryFlag(c, "include_inactive") {
		series = filterActiveSeries(series)
	}
	// Filtro opcional por categoría (vacío en BD = venta para series 00/01/03)
	category := strings.TrimSpace(strings.ToLower(c.Query("category")))
	if category != "" {
		filtered := series[:0]
		for _, s := range series {
			cat := strings.TrimSpace(strings.ToLower(s.Category))
			if cat == "" {
				code := strings.TrimSpace(s.SunatCode)
				if category == "venta" && (code == "" || code == "00" || code == "01" || code == "03") {
					filtered = append(filtered, s)
				}
				continue
			}
			if cat == category {
				filtered = append(filtered, s)
			}
		}
		series = filtered
	}
	return c.JSON(fiber.Map{"data": series})
}

// GET /api/company/series/document-types?context=restaurant
func (h *CompanyHandler) ListSeriesDocumentTypesAPI(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	restaurant := strings.TrimSpace(strings.ToLower(c.Query("context"))) == "restaurant"
	types := docseries.ListFormDocumentTypes(svc.IsSunatEnabled(), restaurant)
	return c.JSON(fiber.Map{
		"data":            types,
		"category_labels": docseries.CategoryLabels(),
	})
}

// POST /api/company/series
func (h *CompanyHandler) CreateSeriesAPI(c fiber.Ctx) error {
	var body struct {
		BranchID    uint   `json:"branch_id"`
		DocType     string `json:"doc_type"`
		Series      string `json:"series"`
		Correlative *uint  `json:"correlative"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := service.NewCompanyService(db(c)).CreateSeries(body.BranchID, body.DocType, body.Series, body.Correlative); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true})
}

// PUT /api/company/series/:id
func (h *CompanyHandler) UpdateSeriesAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Series      string `json:"series"`
		Active      bool   `json:"active"`
		DocType     string `json:"doc_type"`
		Correlative *uint  `json:"correlative"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	var corr *uint
	if body.Correlative != nil {
		corr = body.Correlative
	}
	if err := service.NewCompanyService(db(c)).UpdateSeries(uint(id), body.Series, body.Active, body.DocType, corr); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/company/series/:id
func (h *CompanyHandler) DeleteSeriesAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewCompanyService(db(c)).DeleteSeries(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}
