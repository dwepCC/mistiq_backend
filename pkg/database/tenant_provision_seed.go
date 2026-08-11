package database

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// TenantSeedInput datos del formulario de alta para seed inicial del tenant.
type TenantSeedInput struct {
	AdminEmail     string
	AdminPassword  string
	CompanyName    string
	RUC            string
	Address        string
	Ubigeo         string
	Phone          string
	Email          string
	Rubro          string // general | gastronomico
	TaxpayerRegime string // general | nrus — régimen tributario del contribuyente
}

// ProvisionTenantSeed inicializa BD tenant en una transacción (sucursal, empresa, POS, admin, series).
func ProvisionTenantSeed(db *gorm.DB, in TenantSeedInput) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := seedTenantRoles(tx); err != nil {
			return err
		}
		mainBranchID, err := seedMainBranch(tx, in)
		if err != nil {
			return err
		}
		walkInID, err := seedDefaultWalkInContact(tx, in)
		if err != nil {
			return err
		}
		if err := seedCompanyConfig(tx, in, mainBranchID, walkInID); err != nil {
			return err
		}
		if err := seedDocumentSeries(tx, mainBranchID); err != nil {
			return err
		}
		if err := SeedInventoryOperationTypes(tx); err != nil {
			return err
		}
		if err := SeedInventoryDocumentSeriesForBranch(tx, mainBranchID); err != nil {
			return err
		}
		if err := seedAdminUser(tx, in, mainBranchID); err != nil {
			return err
		}
		if err := SeedPaymentMethodsIfEmpty(tx); err != nil {
			return err
		}
		if err := seedGastronomicDefaultsIfNeeded(tx, in.Rubro, mainBranchID); err != nil {
			return err
		}
		return nil
	})
}

func seedGastronomicDefaultsIfNeeded(tx *gorm.DB, rubro string, branchID uint) error {
	if !IsGastronomicRubro(rubro) {
		return nil
	}
	return seedGastronomicDefaults(tx, branchID)
}

func seedTenantRoles(tx *gorm.DB) error {
	var roleCount int64
	if err := tx.Model(&TenantRole{}).Count(&roleCount).Error; err != nil {
		return err
	}
	if roleCount > 0 {
		return nil
	}
	roles := []TenantRole{
		{Name: "Administrador", Description: "Acceso completo al sistema", IsSystem: true},
		{Name: "Supervisor", Description: "Supervisión y reportes", IsSystem: true},
		{Name: "Cajero", Description: "Caja y movimientos", IsSystem: true},
		{Name: "Vendedor", Description: "Gestión de ventas y POS", IsSystem: true},
		{Name: "Almacenero", Description: "Gestión de inventario", IsSystem: true},
		{Name: "Contador", Description: "Gestión contable", IsSystem: true},
	}
	return tx.Create(&roles).Error
}

func seedMainBranch(tx *gorm.DB, in TenantSeedInput) (uint, error) {
	var existing TenantBranch
	err := tx.Where("is_main = ?", true).First(&existing).Error
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	addr, _ := NormalizeTenantContactAddressUbigeo(strings.TrimSpace(in.Address), strings.TrimSpace(in.Ubigeo))
	branch := TenantBranch{
		Name:    "Principal",
		Address: addr,
		Phone:   strings.TrimSpace(in.Phone),
		IsMain:  true,
		Active:  true,
	}
	if err := tx.Create(&branch).Error; err != nil {
		return 0, fmt.Errorf("sucursal principal: %w", err)
	}
	return branch.ID, nil
}

func seedDefaultWalkInContact(tx *gorm.DB, in TenantSeedInput) (uint, error) {
	var c TenantContact
	err := tx.Where("doc_type = ? AND doc_number = ?", "0", "99999999").First(&c).Error
	if err == nil {
		if !c.IsDefaultWalkIn {
			_ = tx.Model(&c).Update("is_default_walkin", true).Error
		}
		return c.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	addr, ubi := NormalizeTenantContactAddressUbigeo(strings.TrimSpace(in.Address), strings.TrimSpace(in.Ubigeo))
	def := TenantContact{
		Type:            "customer",
		DocType:         "0",
		DocNumber:       "99999999",
		BusinessName:    "Clientes Varios",
		TradeName:       "Público en general",
		Address:         addr,
		Ubigeo:          ubi,
		Phone:           strings.TrimSpace(in.Phone),
		Email:           strings.TrimSpace(in.Email),
		IsDefaultWalkIn: true,
		Active:          true,
	}
	if err := tx.Create(&def).Error; err != nil {
		return 0, fmt.Errorf("cliente POS por defecto: %w", err)
	}
	return def.ID, nil
}

func seedCompanyConfig(tx *gorm.DB, in TenantSeedInput, branchID, walkInID uint) error {
	var cfgCount int64
	if err := tx.Model(&TenantCompanyConfig{}).Count(&cfgCount).Error; err != nil {
		return err
	}
	addr, ubi := NormalizeTenantContactAddressUbigeo(strings.TrimSpace(in.Address), strings.TrimSpace(in.Ubigeo))
	bid, wid := branchID, walkInID
	if cfgCount > 0 {
		return tx.Model(&TenantCompanyConfig{}).Where("id > 0").Limit(1).
			Updates(map[string]interface{}{
				"business_name":              strings.TrimSpace(in.CompanyName),
				"ruc":                        strings.TrimSpace(in.RUC),
				"address":                    addr,
				"ubigeo":                     ubi,
				"phone":                      strings.TrimSpace(in.Phone),
				"email":                      strings.TrimSpace(in.Email),
				"currency":                   "PEN",
				"tax_rate":                   18.00,
				"default_branch_id":          bid,
				"default_walk_in_contact_id": wid,
			}).Error
	}
	cfg := TenantCompanyConfig{
		DefaultBranchID:        &bid,
		DefaultWalkInContactID: &wid,
		BusinessName:           strings.TrimSpace(in.CompanyName),
		RUC:                    strings.TrimSpace(in.RUC),
		Address:                addr,
		Ubigeo:                 ubi,
		Phone:                  strings.TrimSpace(in.Phone),
		Email:                  strings.TrimSpace(in.Email),
		Currency:               "PEN",
		TaxRate:                18.00,
		SunatEnvMode:           "demo",
		TaxpayerRegime:         in.TaxpayerRegime,
	}
	return tx.Create(&cfg).Error
}

func seedDocumentSeries(tx *gorm.DB, branchID uint) error {
	var seriesCount int64
	if err := tx.Model(&TenantDocumentSeries{}).Count(&seriesCount).Error; err != nil {
		return err
	}
	if seriesCount > 0 {
		return nil
	}
	series := []TenantDocumentSeries{
		{BranchID: branchID, DocType: "FACTURA", SunatCode: "01", Category: "venta", Series: "F001", Correlative: 1, Active: true},
		{BranchID: branchID, DocType: "BOLETA", SunatCode: "03", Category: "venta", Series: "B001", Correlative: 1, Active: true},
		{BranchID: branchID, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta", Series: "NV001", Correlative: 1, Active: true},
		// La cotización no es documento SUNAT, pero necesita serie y correlativo propios.
		// V073 la siembra para tenants existentes; aquí, porque esa migración corre antes
		// de que exista la sucursal y no alcanza a crearla en un tenant nuevo.
		{BranchID: branchID, DocType: "Cotización", SunatCode: "QT", Category: "cotizacion", Series: "COT01", Correlative: 1, Active: true},
		{BranchID: branchID, DocType: "NOTA_CREDITO", SunatCode: "07", Category: "nota_credito", Series: "FC01", Correlative: 1, Active: true},
		{BranchID: branchID, DocType: "NOTA_CREDITO", SunatCode: "07", Category: "nota_credito", Series: "BC01", Correlative: 1, Active: true},
		{BranchID: branchID, DocType: "NOTA_DEBITO", SunatCode: "08", Category: "nota_debito", Series: "FD01", Correlative: 1, Active: true},
		{BranchID: branchID, DocType: "GUIA_REMISION", SunatCode: "09", Category: "guia_remision", Series: "T001", Correlative: 1, Active: true},
		{BranchID: branchID, DocType: "GUIA_TRANSPORTISTA", SunatCode: "31", Category: "guia_transportista", Series: "V001", Correlative: 1, Active: true},
		{BranchID: branchID, DocType: "RETENCION", SunatCode: "20", Category: "retencion", Series: "R001", Correlative: 1, Active: true},
		{BranchID: branchID, DocType: "PERCEPCION", SunatCode: "40", Category: "percepcion", Series: "P001", Correlative: 1, Active: true},
	}
	return tx.Create(&series).Error
}

func seedAdminUser(tx *gorm.DB, in TenantSeedInput, mainBranchID uint) error {
	var userCount int64
	if err := tx.Model(&TenantUser{}).Count(&userCount).Error; err != nil {
		return err
	}
	if userCount > 0 {
		return nil
	}
	var adminRole TenantRole
	if err := tx.Where("name = ?", "Administrador").First(&adminRole).Error; err != nil {
		return fmt.Errorf("rol Administrador: %w", err)
	}
	home := mainBranchID
	user := &TenantUser{
		RoleID:       adminRole.ID,
		HomeBranchID: &home,
		Name:         "Administrador",
		Email:        strings.TrimSpace(in.AdminEmail),
		Phone:        strings.TrimSpace(in.Phone),
		Active:       true,
	}
	if err := user.SetPassword(in.AdminPassword); err != nil {
		return err
	}
	if err := tx.Create(user).Error; err != nil {
		return err
	}
	if IsGastronomicRubro(in.Rubro) {
		if err := seedGastronomicAdminStaff(tx, user.ID); err != nil {
			return err
		}
	}
	return nil
}
