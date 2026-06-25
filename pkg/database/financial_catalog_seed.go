package database

import (
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/paymentmethod"
	"tukifac/pkg/taxpayment"

	"gorm.io/gorm"
)

// SeedFinancialCatalog siembra los tres dominios financieros de forma idempotente.
func SeedFinancialCatalog(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := SeedPaymentMethodsCatalog(tx); err != nil {
			return err
		}
		if err := SeedPaymentConditionsCatalog(tx); err != nil {
			return err
		}
		if err := SeedTaxPaymentTypesCatalog(tx); err != nil {
			return err
		}
		return nil
	})
}

// SeedPaymentMethodsIfEmpty alias histórico.
func SeedPaymentMethodsIfEmpty(db *gorm.DB) error {
	return SeedFinancialCatalog(db)
}

// SeedPaymentMethodsCatalog solo medios operativos de cobro.
func SeedPaymentMethodsCatalog(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if err := db.Model(&TenantPaymentMethod{}).Limit(1).Count(new(int64)).Error; err != nil {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		bankIDs, err := ensureDefaultBankAccounts(tx)
		if err != nil {
			return err
		}
		for _, e := range paymentmethod.OperationalCatalog {
			if err := ensureOperationalPaymentMethod(tx, e, bankIDs); err != nil {
				return err
			}
		}
		return linkOrphanBankAccountsToPaymentMethods(tx)
	})
}

func SeedPaymentConditionsCatalog(db *gorm.DB) error {
	catalog := []TenantPaymentCondition{
		{Code: paymentcondition.CodeCash, Name: paymentcondition.NameCash, Active: true},
		{Code: paymentcondition.CodeCredit, Name: paymentcondition.NameCredit, Active: true},
	}
	for _, row := range catalog {
		if err := ensurePaymentCondition(db, row); err != nil {
			return err
		}
	}
	return nil
}

func SeedTaxPaymentTypesCatalog(db *gorm.DB) error {
	return ensureTaxPaymentType(db, TenantTaxPaymentType{
		Code:   taxpayment.CodeDetraccionBN,
		Name:   taxpayment.NameDetraccionBN,
		Active: true,
	})
}

func ensurePaymentCondition(db *gorm.DB, row TenantPaymentCondition) error {
	code := strings.TrimSpace(row.Code)
	var existing TenantPaymentCondition
	err := db.Where("code = ?", code).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&row).Error
	}
	if err != nil {
		return err
	}
	updates := map[string]interface{}{"active": true}
	if strings.TrimSpace(existing.Name) == "" {
		updates["name"] = row.Name
	}
	return db.Model(&existing).Updates(updates).Error
}

func ensureTaxPaymentType(db *gorm.DB, row TenantTaxPaymentType) error {
	code := strings.TrimSpace(row.Code)
	var existing TenantTaxPaymentType
	err := db.Where("code = ?", code).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&row).Error
	}
	if err != nil {
		return err
	}
	updates := map[string]interface{}{"active": true}
	if strings.TrimSpace(existing.Name) == "" {
		updates["name"] = row.Name
	}
	return db.Model(&existing).Updates(updates).Error
}

func ensureOperationalPaymentMethod(tx *gorm.DB, e paymentmethod.OperationalEntry, bankIDs map[string]uint) error {
	code := strings.TrimSpace(e.Code)
	var existing TenantPaymentMethod
	err := tx.Where("code = ?", code).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	dest := paymentmethod.DestinationForCode(code)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		pm := TenantPaymentMethod{
			Name:            e.Name,
			Code:            code,
			DestinationType: dest,
			IsSystem:        e.IsSystem,
			SortOrder:       e.SortOrder,
			Active:          true,
		}
		if e.BankAccountKey != "" {
			if id, ok := bankIDs[e.BankAccountKey]; ok {
				pm.BankAccountID = &id
			}
		}
		return tx.Create(&pm).Error
	}
	updates := map[string]interface{}{
		"destination_type": dest,
		"is_system":        e.IsSystem,
		"active":           true,
	}
	if strings.TrimSpace(existing.Name) == "" {
		updates["name"] = e.Name
	}
	if existing.SortOrder == 0 && e.SortOrder > 0 {
		updates["sort_order"] = e.SortOrder
	}
	if e.BankAccountKey != "" && (existing.BankAccountID == nil || *existing.BankAccountID == 0) {
		if id, ok := bankIDs[e.BankAccountKey]; ok {
			updates["bank_account_id"] = id
		}
	}
	return tx.Model(&existing).Updates(updates).Error
}

func ensureDefaultBankAccounts(tx *gorm.DB) (map[string]uint, error) {
	out := make(map[string]uint, len(paymentmethod.BankAccountCatalog))
	for key, tmpl := range paymentmethod.BankAccountCatalog {
		var existing TenantBankAccount
		err := tx.Where("payment_method = ? AND deleted_at IS NULL", tmpl.PaymentMethod).First(&existing).Error
		if err == nil {
			out[key] = existing.ID
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		row := TenantBankAccount{
			Name:          tmpl.Name,
			Type:          tmpl.Type,
			PaymentMethod: tmpl.PaymentMethod,
			Currency:      tmpl.Currency,
			Active:        true,
		}
		if err := tx.Create(&row).Error; err != nil {
			return nil, err
		}
		out[key] = row.ID
	}
	return out, nil
}

func linkOrphanBankAccountsToPaymentMethods(tx *gorm.DB) error {
	var methods []TenantPaymentMethod
	if err := tx.Where("destination_type = ? OR (code <> ? AND bank_account_id IS NOT NULL)", "bank_account", "cash").
		Where("bank_account_id IS NULL OR bank_account_id = 0").
		Where("code NOT IN ?", []string{"credito", "credit", taxpayment.CodeDetraccionBN}).
		Find(&methods).Error; err != nil {
		return err
	}
	for i := range methods {
		m := &methods[i]
		key := strings.TrimSpace(m.Code)
		var acc TenantBankAccount
		if err := tx.Where("payment_method = ? AND deleted_at IS NULL", key).First(&acc).Error; err != nil {
			continue
		}
		id := acc.ID
		if err := tx.Model(m).Updates(map[string]interface{}{
			"bank_account_id":  id,
			"destination_type": "bank_account",
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// EnsureDetractionTaxPaymentType garantiza detraccion_bn en tenant_tax_payment_types.
func EnsureDetractionTaxPaymentType(db *gorm.DB) error {
	return ensureTaxPaymentType(db, TenantTaxPaymentType{
		Code: taxpayment.CodeDetraccionBN, Name: taxpayment.NameDetraccionBN, Active: true,
	})
}

// EnsureCreditPaymentCondition garantiza credit en tenant_payment_conditions.
func EnsureCreditPaymentCondition(db *gorm.DB) error {
	return ensurePaymentCondition(db, TenantPaymentCondition{
		Code: paymentcondition.CodeCredit, Name: paymentcondition.NameCredit, Active: true,
	})
}

// Legacy aliases — redirigen al dominio correcto.
func EnsureDetractionPaymentMethod(db *gorm.DB) error { return EnsureDetractionTaxPaymentType(db) }
func EnsureCreditPaymentMethod(db *gorm.DB) error     { return EnsureCreditPaymentCondition(db) }

// BackfillPaymentMethodKinds obsoleto tras v075; no-op.
func BackfillPaymentMethodKinds(db *gorm.DB) error { return nil }

// SplitFinancialDomainsFromLegacy migra credito/detraccion fuera de tenant_payment_methods.
func SplitFinancialDomainsFromLegacy(db *gorm.DB) error {
	if err := db.AutoMigrate(&TenantPaymentCondition{}, &TenantTaxPaymentType{}); err != nil {
		return fmt.Errorf("auto migrate financial domains: %w", err)
	}

	var legacy []TenantPaymentMethod
	if err := db.Unscoped().Find(&legacy).Error; err != nil {
		return err
	}
	for _, row := range legacy {
		code := strings.TrimSpace(strings.ToLower(row.Code))
		switch code {
		case "credito", "credit":
			if err := ensurePaymentCondition(db, TenantPaymentCondition{
				Code: paymentcondition.CodeCredit,
				Name: firstNonEmpty(row.Name, paymentcondition.NameCredit),
				Active: row.Active,
			}); err != nil {
				return err
			}
			if err := db.Unscoped().Delete(&row).Error; err != nil {
				return err
			}
		case taxpayment.CodeDetraccionBN:
			if err := ensureTaxPaymentType(db, TenantTaxPaymentType{
				Code: taxpayment.CodeDetraccionBN,
				Name: firstNonEmpty(row.Name, taxpayment.NameDetraccionBN),
				Active: row.Active,
			}); err != nil {
				return err
			}
			if err := db.Unscoped().Delete(&row).Error; err != nil {
				return err
			}
		}
	}

	mig := db.Migrator()
	pm := &TenantPaymentMethod{}
	if mig.HasColumn(pm, "Kind") {
		_ = mig.DropColumn(pm, "Kind")
	}

	return SeedFinancialCatalog(db)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return b
}

// FinancialCatalogAuditResult reporta faltantes en un tenant.
type FinancialCatalogAuditResult struct {
	MissingMethods      []string `json:"missing_methods"`
	MissingConditions   []string `json:"missing_conditions"`
	MissingTaxTypes     []string `json:"missing_tax_types"`
	OrphanMethodRows    []string `json:"orphan_method_rows"`
	UnlinkedBankMethods []string `json:"unlinked_bank_methods"`
	OK                  bool     `json:"ok"`
}

func AuditFinancialCatalog(db *gorm.DB) (FinancialCatalogAuditResult, error) {
	out := FinancialCatalogAuditResult{}
	requiredMethods := []string{"cash", "yape", "plin", "transferencia", "tarjeta"}
	for _, code := range requiredMethods {
		var n int64
		db.Model(&TenantPaymentMethod{}).Where("code = ?", code).Count(&n)
		if n == 0 {
			out.MissingMethods = append(out.MissingMethods, code)
		}
	}
	for _, code := range []string{paymentcondition.CodeCash, paymentcondition.CodeCredit} {
		var n int64
		db.Model(&TenantPaymentCondition{}).Where("code = ?", code).Count(&n)
		if n == 0 {
			out.MissingConditions = append(out.MissingConditions, code)
		}
	}
	var taxN int64
	db.Model(&TenantTaxPaymentType{}).Where("code = ?", taxpayment.CodeDetraccionBN).Count(&taxN)
	if taxN == 0 {
		out.MissingTaxTypes = append(out.MissingTaxTypes, taxpayment.CodeDetraccionBN)
	}
	var orphans []TenantPaymentMethod
	db.Where("code IN ?", []string{"credito", "credit", taxpayment.CodeDetraccionBN}).Find(&orphans)
	for _, o := range orphans {
		out.OrphanMethodRows = append(out.OrphanMethodRows, o.Code)
	}
	var unlinked []TenantPaymentMethod
	db.Where("code IN ?", []string{"yape", "plin", "transferencia", "tarjeta"}).
		Where("bank_account_id IS NULL OR bank_account_id = 0").Find(&unlinked)
	for _, u := range unlinked {
		out.UnlinkedBankMethods = append(out.UnlinkedBankMethods, u.Code)
	}
	out.OK = len(out.MissingMethods) == 0 && len(out.MissingConditions) == 0 &&
		len(out.MissingTaxTypes) == 0 && len(out.OrphanMethodRows) == 0 && len(out.UnlinkedBankMethods) == 0
	return out, nil
}

func RepairFinancialCatalog(db *gorm.DB) error {
	if err := SplitFinancialDomainsFromLegacy(db); err != nil {
		return err
	}
	return SeedFinancialCatalog(db)
}
