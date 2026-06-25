package engine

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"

	"gorm.io/gorm"
)

// DriftReport resultado de validar esquema vs versión declarada.
type DriftReport struct {
	DeclaredVersion int
	ProvenVersion   int
	Issues          []string
}

func (r DriftReport) HasDrift() bool {
	return len(r.Issues) > 0
}

// DetectSchemaDrift verifica historial y objetos críticos hasta declaredVersion.
func DetectSchemaDrift(db *gorm.DB, declaredVersion int) DriftReport {
	report := DriftReport{DeclaredVersion: declaredVersion}
	if db == nil {
		return report
	}

	proven, err := DeriveCurrentVersionFromHistory(db)
	if err != nil {
		report.Issues = append(report.Issues, fmt.Sprintf("error leyendo historial: %v", err))
		return report
	}
	report.ProvenVersion = proven

	if declaredVersion <= 0 {
		return report
	}

	if declaredVersion > proven {
		report.Issues = append(report.Issues,
			fmt.Sprintf("versión declarada V%d > historial probado V%d", declaredVersion, proven))
	}

	for _, mig := range tenantmigrations.TenantMigrations {
		v := mig.Version()
		if v > declaredVersion {
			continue
		}
		applied, err := isHistoryApplied(db, v, database.MigrationHistoryTypeSchema)
		if err != nil {
			report.Issues = append(report.Issues, fmt.Sprintf("V%d: error leyendo historial: %v", v, err))
			continue
		}
		if !applied {
			report.Issues = append(report.Issues, fmt.Sprintf("V%d %s no registrada en tenant_migration_history", v, mig.Name()))
		}
	}

	appendCriticalObjectDrift(db, declaredVersion, &report)
	return report
}

func appendCriticalObjectDrift(db *gorm.DB, declaredVersion int, report *DriftReport) {
	mig := db.Migrator()
	for _, c := range criticalSchemaChecks {
		if declaredVersion < c.version {
			continue
		}
		if !mig.HasTable(c.table) {
			report.Issues = append(report.Issues, fmt.Sprintf("falta %s (esperado desde V%d)", c.label, c.version))
		}
	}
}

// ValidateSchemaAtVersion falla si la versión declarada no tiene evidencia completa.
func ValidateSchemaAtVersion(db *gorm.DB, version int) error {
	report := DetectSchemaDrift(db, version)
	if !report.HasDrift() {
		return nil
	}
	return fmt.Errorf("esquema inconsistente en V%d: %s", version, strings.Join(report.Issues, "; "))
}

// LowestMissingVersion primera migración registrada <= upTo sin historial exitoso.
func LowestMissingVersion(db *gorm.DB, upTo int) (int, bool) {
	lowest := 0
	found := false
	for _, mig := range tenantmigrations.TenantMigrations {
		v := mig.Version()
		if v > upTo {
			continue
		}
		applied, _ := isHistoryApplied(db, v, database.MigrationHistoryTypeSchema)
		if applied {
			continue
		}
		if !found || v < lowest {
			lowest = v
			found = true
		}
	}
	return lowest, found
}
