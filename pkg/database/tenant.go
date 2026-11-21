package database

import (
	"fmt"
	"regexp"

	"tukifac/config"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var tenantDBNamePattern = regexp.MustCompile(`^saas_tenant_[a-z0-9_]+$`)

// GetTenantDB retorna el pool GORM del tenant vía TenantDBManager (singleflight + eviction).
// Debe parearse con ReleaseTenantDB al final del request (middleware TenantDBRelease).
func GetTenantDB(dbName string) (*gorm.DB, error) {
	if defaultManager == nil {
		return nil, ErrNoTenantDBManager
	}
	return defaultManager.acquire(dbName)
}

// tenantDSN usa interpolateParams=true: sin PrepareStmt el driver haría PREPARE+EXECUTE+CLOSE por cada
// query con argumentos (2 round trips); interpolando en el cliente es 1, igual que con el statement cacheado.
func tenantDSN(dbName string) string {
	cfg := config.AppConfig
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local&interpolateParams=true",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, dbName,
	)
}

// tenantGormConfig NO activa PrepareStmt: en gorm v1.31 cada gorm.Open con PrepareStmt crea un LRU de
// statements cuya goroutine de expiración nunca termina (su canal done no se cierra) y retiene el *sql.DB
// aunque se cierre. Los pools de tenant se abren y cierran todo el día (LRU de 200, idle TTL, conexiones
// efímeras de los barridos), así que cada apertura fugaba ~50 KB + 1 goroutine hasta matar el contenedor
// por OOM cada pocas horas (14-oct-2026). Ver TestTenantGormConfigNoGoroutineLeak.
func tenantGormConfig() *gorm.Config {
	return &gorm.Config{Logger: gormLogger()}
}

func openTenantDB(dbName string) (*gorm.DB, error) {
	return openTenantDBWithDSN(dbName, tenantDSN(dbName))
}

func openTenantDBWithDSN(dbName, dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(dsn), tenantGormConfig())
	if err != nil {
		return nil, fmt.Errorf("conectando BD tenant %s: %w", dbName, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	configureTenantPool(sqlDB)

	if err := installBranchScope(db); err != nil {
		return nil, fmt.Errorf("scope de sucursal en BD tenant %s: %w", dbName, err)
	}

	return db, nil
}

// CreateTenantDB crea la base de datos MySQL para un nuevo tenant.
func CreateTenantDB(dbName string) error {
	if err := validateTenantDBName(dbName); err != nil {
		return err
	}
	if CentralDB == nil {
		return fmt.Errorf("BD central no inicializada")
	}
	sql := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", dbName)
	return CentralDB.Exec(sql).Error
}

// DropTenantDB elimina la base de datos del tenant (rollback al fallar el alta).
func DropTenantDB(dbName string) error {
	if err := validateTenantDBName(dbName); err != nil {
		return err
	}
	if CentralDB == nil {
		return fmt.Errorf("BD central no inicializada")
	}
	RemoveTenantFromPool(dbName)
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName)
	return CentralDB.Exec(sql).Error
}

func validateTenantDBName(dbName string) error {
	if dbName == "" || !tenantDBNamePattern.MatchString(dbName) {
		return fmt.Errorf("nombre de base de datos de tenant inválido: %q", dbName)
	}
	return nil
}

// RemoveTenantFromPool cierra y elimina el pool del tenant.
func RemoveTenantFromPool(dbName string) {
	if defaultManager != nil {
		defaultManager.Remove(dbName)
	}
}
