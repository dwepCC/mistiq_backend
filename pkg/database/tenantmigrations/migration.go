package tenantmigrations

import "gorm.io/gorm"

// TenantMigration migración incremental de esquema tenant (solo DDL / cambios estructurales).
type TenantMigration interface {
	Version() int
	Name() string
	Up(db *gorm.DB) error
}

// MinVersion primera versión registrada (esquema vacío = 0).
func MinVersion() int {
	min := 0
	for _, mig := range TenantMigrations {
		if v := mig.Version(); min == 0 || v < min {
			min = v
		}
	}
	return min
}

// CountRegisteredUpTo cuenta migraciones registradas con versión <= upTo.
func CountRegisteredUpTo(upTo int) int {
	n := 0
	for _, mig := range TenantMigrations {
		if mig.Version() <= upTo {
			n++
		}
	}
	return n
}

// CountPendingBetween cuenta migraciones en (fromV, toV].
func CountPendingBetween(fromV, toV int) int {
	return len(MigrationsUpTo(fromV, toV))
}

// MigrationsUpTo devuelve migraciones registradas con versión en (fromV, toV].
func MigrationsUpTo(fromV, toV int) []TenantMigration {
	if toV <= fromV {
		return nil
	}
	out := make([]TenantMigration, 0, len(TenantMigrations))
	for _, mig := range TenantMigrations {
		v := mig.Version()
		if v > fromV && v <= toV {
			out = append(out, mig)
		}
	}
	return out
}
