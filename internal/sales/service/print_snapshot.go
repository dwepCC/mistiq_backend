package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"tukifac/pkg/branchlogo"
	"tukifac/pkg/database"
	"tukifac/pkg/taxregime"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// printSnapshotVersion versión del formato guardado en tenant_document_snapshots.data.
const printSnapshotVersion = 1

// PrintCompanyIdentity son los datos del emisor que identifican al documento y que deben quedar tal como estaban al emitirlo
// (los ajustes de presentación —diseño A4, tamaño de logo, qué mostrar en notas de venta— NO se congelan: son de estilo).
type PrintCompanyIdentity struct {
	RUC              string `json:"ruc"`
	BusinessName     string `json:"business_name"`
	TradeName        string `json:"trade_name,omitempty"`
	Address          string `json:"address,omitempty"`
	Phone            string `json:"phone,omitempty"`
	Email            string `json:"email,omitempty"`
	Website          string `json:"website,omitempty"`
	AdditionalNotes  string `json:"additional_notes,omitempty"`
	ShowIgvBreakdown bool   `json:"show_igv_breakdown"`
}

// PrintSnapshot es la «foto» congelada de lo que el PDF de un documento lee de otras tablas (ver TenantDocumentSnapshot).
type PrintSnapshot struct {
	Version       int                  `json:"v"`
	Client        *PrintClient         `json:"client,omitempty"` // cliente (ventas/cotizaciones) o proveedor (compras)
	Company       PrintCompanyIdentity `json:"company"`
	Branch        PrintBranch          `json:"branch"`
	SellerName    string               `json:"seller_name,omitempty"`
	PaymentWallet *PrintPaymentWallet  `json:"payment_wallet,omitempty"`
	BankAccounts  []PrintBankAccount   `json:"bank_accounts,omitempty"`
	TermsText     string               `json:"terms_text,omitempty"`
	// LogoHash referencia al logo impreso en tenant_snapshot_logos (vacío = el documento no llevaba logo propio embebido).
	LogoHash string `json:"logo_hash,omitempty"`
}

func snapshotFromPrintData(pd *PrintData) *PrintSnapshot {
	s := &PrintSnapshot{
		Version: printSnapshotVersion,
		Company: PrintCompanyIdentity{
			RUC:              pd.Company.RUC,
			BusinessName:     pd.Company.BusinessName,
			TradeName:        pd.Company.TradeName,
			Address:          pd.Company.Address,
			Phone:            pd.Company.Phone,
			Email:            pd.Company.Email,
			Website:          pd.Company.Website,
			AdditionalNotes:  pd.Company.AdditionalNotes,
			ShowIgvBreakdown: pd.Company.ShowIgvBreakdown,
		},
		Branch:        pd.Branch,
		SellerName:    pd.SellerName,
		PaymentWallet: pd.PaymentWallet,
		BankAccounts:  pd.BankAccounts,
	}
	if pd.Client != nil {
		c := *pd.Client
		s.Client = &c
	}
	if pd.Fiscal != nil && pd.Fiscal.ShowTermsConditions {
		s.TermsText = pd.Fiscal.TermsText
	}
	return s
}

// applySnapshot superpone la foto congelada sobre el print_data armado con datos vivos. Lo que la foto no tiene (documentos
// sin logo, etc.) se deja como está.
func applySnapshot(db *gorm.DB, pd *PrintData, s *PrintSnapshot) {
	if s.Client != nil {
		c := *s.Client
		pd.Client = &c
	}
	pd.Company.RUC = s.Company.RUC
	pd.Company.BusinessName = s.Company.BusinessName
	pd.Company.TradeName = s.Company.TradeName
	pd.Company.Address = s.Company.Address
	pd.Company.Phone = s.Company.Phone
	pd.Company.Email = s.Company.Email
	pd.Company.Website = s.Company.Website
	pd.Company.AdditionalNotes = s.Company.AdditionalNotes
	pd.Company.ShowIgvBreakdown = s.Company.ShowIgvBreakdown
	pd.Branch = s.Branch
	pd.SellerName = s.SellerName
	pd.PaymentWallet = s.PaymentWallet
	pd.BankAccounts = s.BankAccounts
	if s.TermsText != "" && pd.Fiscal != nil && pd.Fiscal.ShowTermsConditions {
		pd.Fiscal.TermsText = s.TermsText
	}
	if s.LogoHash != "" {
		if data := loadSnapshotLogo(db, s.LogoHash); data != "" {
			pd.Company.LogoURL = data
		}
	}
}

func logoHash(dataURL string) string {
	sum := sha256.Sum256([]byte(dataURL))
	return hex.EncodeToString(sum[:])
}

// saveSnapshotLogo guarda el logo (solo si ya viene embebido como data:) una vez por contenido y devuelve su hash.
func saveSnapshotLogo(db *gorm.DB, dataURL string) string {
	dataURL = strings.TrimSpace(dataURL)
	if !strings.HasPrefix(dataURL, "data:") {
		return ""
	}
	h := logoHash(dataURL)
	row := database.TenantSnapshotLogo{Hash: h, Data: dataURL, CreatedAt: time.Now()}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return ""
	}
	return h
}

func loadSnapshotLogo(db *gorm.DB, hash string) string {
	var row database.TenantSnapshotLogo
	if err := db.Where("hash = ?", hash).First(&row).Error; err != nil {
		return ""
	}
	return row.Data
}

// LoadPrintSnapshot devuelve la foto guardada de un documento, o nil si todavía no tiene.
func LoadPrintSnapshot(db *gorm.DB, kind string, docID uint) *PrintSnapshot {
	if docID == 0 {
		return nil
	}
	var row database.TenantDocumentSnapshot
	if err := db.Where("doc_kind = ? AND doc_id = ?", kind, docID).First(&row).Error; err != nil {
		return nil
	}
	var s PrintSnapshot
	if err := json.Unmarshal([]byte(row.Data), &s); err != nil {
		return nil
	}
	return &s
}

func storePrintSnapshot(db *gorm.DB, kind string, docID uint, s *PrintSnapshot) {
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	row := database.TenantDocumentSnapshot{DocKind: kind, DocID: docID, Version: s.Version, Data: string(b), CreatedAt: time.Now()}
	// Una sola foto por documento: si dos impresiones llegan a la vez, gana la primera.
	_ = db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// DeletePrintSnapshot descarta la foto de un documento (p. ej. una cotización que se edita: su contenido cambia a propósito).
func DeletePrintSnapshot(db *gorm.DB, kind string, docID uint) {
	if docID == 0 {
		return
	}
	_ = db.Where("doc_kind = ? AND doc_id = ?", kind, docID).Delete(&database.TenantDocumentSnapshot{}).Error
}

// FreezePrintData congela (primera vez) o aplica (siguientes) la foto del documento sobre el print_data ya armado con datos vivos.
//
//   - Sin foto: se guarda la actual (cliente, empresa, sucursal, vendedor, cuentas, logo) y el PDF sale igual que siempre.
//   - Con foto: esos datos se toman de ella, no de los registros actuales, así que editar el cliente, la empresa o la sucursal
//     después de emitir no cambia el PDF del documento ya emitido.
//
// Debe llamarse ANTES de calcular el QR (lleva RUC y documento del cliente). Los documentos anteriores a esta función reciben su
// foto la primera vez que se imprimen (o con el comando backfill-document-snapshots).
func FreezePrintData(db *gorm.DB, kind string, docID uint, pd *PrintData) {
	if docID == 0 || pd == nil {
		return
	}
	if s := LoadPrintSnapshot(db, kind, docID); s != nil {
		applySnapshot(db, pd, s)
		return
	}
	s := snapshotFromPrintData(pd)
	s.LogoHash = saveSnapshotLogo(db, pd.Company.LogoURL)
	storePrintSnapshot(db, kind, docID, s)
}

// BuildPurchaseSnapshot arma la foto de una compra con datos vivos: el proveedor y el emisor (empresa + sucursal + logo).
func BuildPurchaseSnapshot(db *gorm.DB, p *database.TenantPurchase) *PrintSnapshot {
	pd := &PrintData{}
	if p.ContactID != nil && *p.ContactID > 0 {
		var contact database.TenantContact
		// Unscoped: un proveedor eliminado después sigue siendo el de esa compra.
		if db.Unscoped().First(&contact, *p.ContactID).Error == nil {
			addr, _ := database.NormalizeTenantContactAddressUbigeo(contact.Address, contact.Ubigeo)
			pd.Client = &PrintClient{
				DocType:      contact.DocType,
				DocNumber:    contact.DocNumber,
				BusinessName: contact.BusinessName,
				Address:      addr,
				Email:        strings.TrimSpace(contact.Email),
				Phone:        strings.TrimSpace(contact.Phone),
			}
		}
	}
	var company database.TenantCompanyConfig
	if db.First(&company).Error == nil {
		pd.Company = PrintCompany{
			RUC:              company.RUC,
			BusinessName:     company.BusinessName,
			TradeName:        company.TradeName,
			Address:          company.Address,
			Phone:            strings.TrimSpace(company.Phone),
			Email:            strings.TrimSpace(company.Email),
			Website:          strings.TrimSpace(company.Website),
			LogoURL:          company.LogoURL,
			ShowIgvBreakdown: taxregime.For(company.TaxpayerRegime).ShowIgvBreakdown,
		}
	}
	var branch database.TenantBranch
	if db.First(&branch, p.BranchID).Error == nil {
		pd.Branch = PrintBranch{Name: branch.Name, Address: branch.Address}
		if addr := strings.TrimSpace(branch.Address); addr != "" {
			pd.Company.Address = addr
		}
		if ph := strings.TrimSpace(branch.Phone); ph != "" {
			pd.Company.Phone = ph
		}
		// Mismo criterio de logo que el comprobante de venta (BuildPrintData): el de la sucursal, con respaldo al de la empresa, embebido.
		branchDataURL := branchlogo.ResolveBranchDataURL(company.RUC, branch.ID, branch.LogoURL)
		companyDataURL := branchlogo.ResolveCompanyDataURL(company.RUC, company.LogoURL)
		pd.Company.LogoURL = branchlogo.ResolveURL(branch.LogoURL, pd.Company.LogoURL)
		if dataURL := branchlogo.ResolvePrintLogo(branch.LogoURL, branchDataURL, company.LogoURL, companyDataURL); dataURL != "" {
			pd.Company.LogoURL = dataURL
		}
	}
	s := snapshotFromPrintData(pd)
	s.LogoHash = saveSnapshotLogo(db, pd.Company.LogoURL)
	return s
}

// EnsurePurchaseSnapshot devuelve la foto de la compra (la crea con datos vivos si todavía no existe) y el logo ya resuelto.
func EnsurePurchaseSnapshot(db *gorm.DB, p *database.TenantPurchase) (*PrintSnapshot, string) {
	s := LoadPrintSnapshot(db, database.SnapshotKindPurchase, p.ID)
	if s == nil {
		s = BuildPurchaseSnapshot(db, p)
		storePrintSnapshot(db, database.SnapshotKindPurchase, p.ID, s)
		// Si otro proceso guardó primero, se usa la suya.
		if stored := LoadPrintSnapshot(db, database.SnapshotKindPurchase, p.ID); stored != nil {
			s = stored
		}
	}
	logo := ""
	if s.LogoHash != "" {
		logo = loadSnapshotLogo(db, s.LogoHash)
	}
	return s, logo
}
