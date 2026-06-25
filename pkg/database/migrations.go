package database

import (
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// =================== MODELOS CENTRALES ===================

type Tenant struct {
	ID                 uint           `gorm:"primaryKey" json:"id"`
	Name               string         `gorm:"size:255;not null" json:"name"`
	Slug               string         `gorm:"size:100;uniqueIndex;not null" json:"slug"`
	DBName             string         `gorm:"size:100;not null" json:"db_name"`
	Plan               string         `gorm:"size:50;default:'trial'" json:"plan"`
	Status             string         `gorm:"size:50;default:'active'" json:"status"`
	Email              string         `gorm:"size:255" json:"email"`
	Phone              string         `gorm:"size:50" json:"phone"`
	RUC                string         `gorm:"size:20" json:"ruc"`
	Rubro              string         `gorm:"size:30;default:'general';index" json:"rubro"` // general | gastronomico
	Address            string         `gorm:"size:500" json:"address"`
	Ubigeo             string         `gorm:"size:6;index" json:"ubigeo"`                   // código 6 dígitos (distrito) para filtros y comprobantes
	LogoURL            string         `gorm:"type:longtext" json:"logo_url"`                // logo de la empresa (data URL); se sincroniza desde el panel tenant cuando tiene SUNAT
	TokenConsultaDatos string         `gorm:"size:255" json:"token_consulta_datos"`         // token para consultas públicas (módulo restaurante)
	SunatConnectedAt   *time.Time     `json:"sunat_connected_at"`                           // última sincronización exitosa con Lycet/SUNAT
	SunatEnvMode       string         `gorm:"size:20;default:'demo'" json:"sunat_env_mode"` // demo/beta = pruebas, production = producción
	TrialEndsAt        *time.Time     `json:"trial_ends_at"`
	StrikeCount        int            `gorm:"default:0" json:"strike_count"`
	PaymentBlocked     bool           `gorm:"default:false" json:"payment_blocked"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	DeletedAt          gorm.DeletedAt `gorm:"index" json:"-"`
}

type SuperAdminUser struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Name      string         `gorm:"size:255;not null" json:"name"`
	Email     string         `gorm:"size:255;uniqueIndex;not null" json:"email"`
	Password  string         `gorm:"size:255;not null" json:"-"`
	Role      string         `gorm:"size:50;default:'admin'" json:"role"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (u *SuperAdminUser) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.Password = string(hash)
	return nil
}

func (u *SuperAdminUser) CheckPassword(password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)) == nil
}

type TenantModule struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	TenantID   uint      `gorm:"not null;index" json:"tenant_id"`
	ModuleKey  string    `gorm:"size:100;not null" json:"module_key"`
	Enabled    bool      `gorm:"default:true" json:"enabled"`
	ConfigJSON *string   `gorm:"type:json" json:"config_json"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type AuditLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TenantID  uint      `gorm:"index" json:"tenant_id"`
	UserID    uint      `gorm:"index" json:"user_id"`
	Action    string    `gorm:"size:100" json:"action"`
	Entity    string    `gorm:"size:100" json:"entity"`
	EntityID  uint      `json:"entity_id"`
	Payload   string    `gorm:"type:text" json:"payload"`
	IPAddress string    `gorm:"size:45" json:"ip_address"`
	CreatedAt time.Time `json:"created_at"`
}

// SaasPlan — planes de suscripción disponibles
type SaasPlan struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Name         string    `gorm:"size:100;not null;uniqueIndex" json:"name"`
	Description  string    `gorm:"size:500" json:"description"`
	Price        float64   `gorm:"not null;default:0" json:"price"`
	BillingCycle string    `gorm:"size:20;default:'monthly'" json:"billing_cycle"` // monthly | yearly | lifetime
	Active       bool      `gorm:"default:true" json:"active"`
	// Límite documentos electrónicos SUNAT por ciclo de suscripción.
	IsUnlimitedDocuments  bool `gorm:"default:false" json:"is_unlimited_documents"`
	MonthlyDocumentsLimit int  `gorm:"default:0" json:"monthly_documents_limit"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SaasModule — catálogo global de módulos disponibles en el sistema
type SaasModule struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Key         string    `gorm:"size:100;not null;uniqueIndex" json:"key"`
	Name        string    `gorm:"size:100;not null" json:"name"`
	Description string    `gorm:"size:500" json:"description"`
	Icon        string    `gorm:"size:100" json:"icon"`
	Active      bool      `gorm:"default:true" json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SaasPlanModule — qué módulos incluye cada plan
type SaasPlanModule struct {
	PlanID    uint      `gorm:"primaryKey;index" json:"plan_id"`
	ModuleKey string    `gorm:"primaryKey;size:100" json:"module_key"`
	CreatedAt time.Time `json:"created_at"`
}

// SaasSubscription — suscripción activa de un tenant (source of truth SaaS).
type SaasSubscription struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	TenantID     uint       `gorm:"not null;index" json:"tenant_id"`
	PlanID       uint       `gorm:"not null" json:"plan_id"`
	BillingCycle string     `gorm:"size:20;default:'monthly'" json:"billing_cycle"` // monthly | semiannual | annual
	StartDate    time.Time  `gorm:"not null" json:"start_date"`
	EndDate      time.Time  `gorm:"not null" json:"end_date"`
	GraceEndsAt  *time.Time `json:"grace_ends_at,omitempty"`
	ProvisionalUntil *time.Time `json:"provisional_until,omitempty"`
	Status       string     `gorm:"size:30;default:'active';index" json:"status"`
	Notes        string     `gorm:"size:500" json:"notes"`
	CancelledAt  *time.Time `json:"cancelled_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// SaasPayment — pagos manuales con comprobante (solo BD central).
type SaasPayment struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	TenantID       uint       `gorm:"not null;index" json:"tenant_id"`
	SubscriptionID *uint      `gorm:"index" json:"subscription_id"`
	BillingCycleID *uint      `gorm:"index" json:"billing_cycle_id"`
	Amount         float64    `gorm:"not null;default:0" json:"amount"`
	ReconnectionFee float64   `gorm:"default:0" json:"reconnection_fee"`
	Currency       string     `gorm:"size:10;default:'PEN'" json:"currency"`
	PeriodMonths   int        `gorm:"default:1" json:"period_months"`
	PaymentMethod  string     `gorm:"size:30" json:"payment_method"` // yape, plin, transfer, deposit
	PaymentDate    *time.Time `json:"payment_date,omitempty"`
	Reference      string     `gorm:"size:120" json:"reference"`
	ReceiptURL     string     `gorm:"size:500" json:"receipt_url"`
	Status         string     `gorm:"size:30;default:'pending_review';index" json:"status"`
	ProvisionalApplied bool     `gorm:"default:false" json:"provisional_applied"`
	Notes          string     `gorm:"size:500" json:"notes"`
	AdminNotes     string     `gorm:"size:500" json:"admin_notes"`
	SubmittedBy    *uint      `json:"submitted_by,omitempty"` // tenant user id
	ReviewedBy     *uint      `json:"reviewed_by"`
	ReviewedAt     *time.Time `json:"reviewed_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CentralAjuste — configuración general del sistema central (una sola fila: ID=1).
// Incluye nombre del sistema, slogan, dirección, UGIE y token_consulta para APIs externas (ej. apiperu.dev).
type CentralAjuste struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	NombreSistema string    `gorm:"size:255" json:"nombre_sistema"`
	Slogan        string    `gorm:"size:500" json:"slogan"`
	Direccion     string    `gorm:"size:500" json:"direccion"`
	Ubigeo        string    `gorm:"size:100" json:"ubigeo"`
	TokenConsulta string    `gorm:"size:500" json:"-"` // No exponer en JSON; solo para uso interno (apiperu.dev)
	EmailContacto string    `gorm:"size:255" json:"email_contacto"`
	Telefono      string    `gorm:"size:50" json:"telefono"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (CentralAjuste) TableName() string { return "central_ajustes" }

// =================== UBIGEO PERÚ (catálogo en BD central y en cada tenant) ===================
// No se gestionan desde la UI; solo se siembran desde migraciones/seed.
// Usado en direcciones de clientes, proveedores, tenants y comprobantes electrónicos (SUNAT).

type UbiRegion struct {
	ID     string `gorm:"primaryKey;size:6;not null" json:"id"`
	Nombre string `gorm:"size:100;not null" json:"nombre"`
}

func (UbiRegion) TableName() string { return "ubi_regiones" }

type UbiProvincia struct {
	ID       string `gorm:"primaryKey;size:6;not null" json:"id"`
	Nombre   string `gorm:"size:100;not null" json:"nombre"`
	RegionID string `gorm:"size:6;not null;index" json:"region_id"`
}

func (UbiProvincia) TableName() string { return "ubi_provincias" }

type UbiDistrito struct {
	ID           string `gorm:"primaryKey;size:6;not null" json:"id"`
	Nombre       string `gorm:"size:100;not null" json:"nombre"`
	ProvinciaID  string `gorm:"size:6;not null;index" json:"provincia_id"`
	RegionID     string `gorm:"size:6;not null;index" json:"region_id"`
	InfoBusqueda string `gorm:"size:255" json:"info_busqueda,omitempty"`
}

func (UbiDistrito) TableName() string { return "ubi_distritos" }

// MigrateCentral aplica las migraciones a la BD central.
func MigrateCentral() error {
	return CentralDB.AutoMigrate(
		&Tenant{},
		&TenantSchemaVersion{},
		&SuperAdminUser{},
		&TenantModule{},
		&AuditLog{},
		&SaasPlan{},
		&SaasModule{},
		&SaasPlanModule{},
		&SaasSubscription{},
		&SaasPayment{},
		&SaasPlatformSettings{},
		&SaasBillingCycle{},
		&SaasNotificationLog{},
		&SaasSubscriptionEvent{},
		&SaasDocumentPackage{},
		&SaasTenantDocumentPackage{},
		&SaasElectronicDocumentUsage{},
		&CentralAjuste{},
		&UbiRegion{},
		&UbiProvincia{},
		&UbiDistrito{},
	)
}

// MigrateModuleKeySaasToMemberships renombra el módulo ERP legacy `saas` → `memberships`
// en catálogo (saas_modules), planes (saas_plan_modules) y flags por tenant (tenant_modules).
// Idempotente: no hace nada si ya no existe la clave `saas`.
func MigrateModuleKeySaasToMemberships() error {
	if CentralDB == nil {
		return nil
	}
	var saasCount, memCount int64
	CentralDB.Model(&SaasModule{}).Where("`key` = ?", "saas").Count(&saasCount)
	CentralDB.Model(&SaasModule{}).Where("`key` = ?", "memberships").Count(&memCount)
	if saasCount == 0 {
		return nil
	}
	if memCount > 0 {
		// Quitar filas legacy `saas` donde ya exista `memberships` para el mismo tenant/plan.
		if err := CentralDB.Exec(`
			DELETE tm_saas FROM tenant_modules tm_saas
			INNER JOIN tenant_modules tm_mem ON tm_mem.tenant_id = tm_saas.tenant_id AND tm_mem.module_key = ?
			WHERE tm_saas.module_key = ?
		`, "memberships", "saas").Error; err != nil {
			return err
		}
		if err := CentralDB.Exec(`
			DELETE pm_saas FROM saas_plan_modules pm_saas
			INNER JOIN saas_plan_modules pm_mem ON pm_mem.plan_id = pm_saas.plan_id AND pm_mem.module_key = ?
			WHERE pm_saas.module_key = ?
		`, "memberships", "saas").Error; err != nil {
			return err
		}
	}
	if err := CentralDB.Exec("UPDATE tenant_modules SET module_key = ? WHERE module_key = ?", "memberships", "saas").Error; err != nil {
		return err
	}
	if err := CentralDB.Exec("UPDATE saas_plan_modules SET module_key = ? WHERE module_key = ?", "memberships", "saas").Error; err != nil {
		return err
	}
	if memCount > 0 {
		return CentralDB.Where("`key` = ?", "saas").Delete(&SaasModule{}).Error
	}
	return CentralDB.Model(&SaasModule{}).Where("`key` = ?", "saas").Update("key", "memberships").Error
}

// SeedCentral inserta datos iniciales en la BD central.
func SeedCentral() error {
	// Super admin
	var adminCount int64
	CentralDB.Model(&SuperAdminUser{}).Count(&adminCount)
	if adminCount == 0 {
		admin := &SuperAdminUser{
			Name:  "Super Administrador",
			Email: "superadmin@saas.com",
			Role:  "superadmin",
		}
		if err := admin.SetPassword("superadmin123"); err != nil {
			return err
		}
		if err := CentralDB.Create(admin).Error; err != nil {
			return err
		}
	}

	// Módulos del catálogo global
	var moduleCount int64
	CentralDB.Model(&SaasModule{}).Count(&moduleCount)
	if moduleCount == 0 {
		modules := []SaasModule{
			{Key: "sales", Name: "Ventas / POS", Description: "Punto de venta, facturas, boletas", Icon: "shopping-cart"},
			{Key: "purchases", Name: "Compras", Description: "Gestión de compras a proveedores", Icon: "truck"},
			{Key: "inventory", Name: "Inventario", Description: "Stock, kardex, movimientos", Icon: "package"},
			{Key: "cashbank", Name: "Caja y Bancos", Description: "Sesiones de caja y cuentas bancarias", Icon: "piggy-bank"},
			{Key: "contacts", Name: "Clientes y Proveedores", Description: "Gestión de contactos", Icon: "users"},
			{Key: "products", Name: "Productos", Description: "Catálogo de productos y servicios", Icon: "tag"},
			{Key: "billing", Name: "Facturación Electrónica", Description: "Integración SUNAT", Icon: "file-invoice"},
			{Key: "restaurant", Name: "Restaurante", Description: "Mesas, comandas y mozos", Icon: "utensils"},
			{Key: "ecommerce", Name: "Ecommerce", Description: "Tienda virtual básica", Icon: "shopping-bag"},
			{Key: "hotel", Name: "Hotel", Description: "Reservas, habitaciones y huéspedes", Icon: "building-2"},
			{Key: "clinic", Name: "Clínica / Consultorio", Description: "Pacientes, citas y atenciones", Icon: "stethoscope"},
			{Key: "transport", Name: "Transporte / Logística", Description: "Rutas, unidades y guías", Icon: "truck"},
			{Key: "manufacturing", Name: "Producción / Manufactura", Description: "Órdenes de producción y procesos", Icon: "factory"},
			{Key: "memberships", Name: "Cuotas y membresías (clientes del tenant)", Description: "Módulo del ERP para cuotas recurrentes entre el tenant y sus propios clientes (gimnasios, academias, etc.). No administra el contrato del tenant con la plataforma Tukifac.", Icon: "layers"},
			{Key: "hr", Name: "Recursos Humanos (HR)", Description: "Colaboradores, asistencias y nóminas", Icon: "users"},
			{Key: "accounting", Name: "Contabilidad", Description: "Libros contables y asientos", Icon: "file-text"},
			{Key: "bi", Name: "Business Intelligence", Description: "Dashboards y analítica avanzada", Icon: "bar-chart-3"},
			{Key: "fixedassets", Name: "Activos fijos", Description: "Activos, depreciaciones y ubicaciones", Icon: "library"},
			{Key: "documents", Name: "Documentos", Description: "Gestión documental, contratos y archivos", Icon: "folder"},
			{Key: "support", Name: "Soporte / Tickets", Description: "Tickets y atención al cliente", Icon: "life-buoy"},
		}
		CentralDB.Create(&modules)
	}

	// Planes por defecto
	var planCount int64
	CentralDB.Model(&SaasPlan{}).Count(&planCount)
	if planCount == 0 {
		plans := []SaasPlan{
			{Name: "Trial", Description: "Período de prueba gratuito 30 días", Price: 0, BillingCycle: "monthly", MonthlyDocumentsLimit: 20},
			{Name: "Basic", Description: "Plan básico para pequeñas empresas", Price: 49, BillingCycle: "monthly", MonthlyDocumentsLimit: 50},
			{Name: "Pro", Description: "Plan profesional con todos los módulos", Price: 99, BillingCycle: "monthly", IsUnlimitedDocuments: true},
		}
		CentralDB.Create(&plans)

		// Módulos del plan Basic (los 6 core)
		var basicPlan SaasPlan
		CentralDB.Where("name = ?", "Basic").First(&basicPlan)
		if basicPlan.ID > 0 {
			basicModules := []SaasPlanModule{
				{PlanID: basicPlan.ID, ModuleKey: "sales"},
				{PlanID: basicPlan.ID, ModuleKey: "purchases"},
				{PlanID: basicPlan.ID, ModuleKey: "inventory"},
				{PlanID: basicPlan.ID, ModuleKey: "cashbank"},
				{PlanID: basicPlan.ID, ModuleKey: "contacts"},
				{PlanID: basicPlan.ID, ModuleKey: "products"},
			}
			CentralDB.Create(&basicModules)
		}

		// Módulos del plan Pro (todos)
		var proPlan SaasPlan
		CentralDB.Where("name = ?", "Pro").First(&proPlan)
		if proPlan.ID > 0 {
			proModules := []SaasPlanModule{
				{PlanID: proPlan.ID, ModuleKey: "sales"},
				{PlanID: proPlan.ID, ModuleKey: "purchases"},
				{PlanID: proPlan.ID, ModuleKey: "inventory"},
				{PlanID: proPlan.ID, ModuleKey: "cashbank"},
				{PlanID: proPlan.ID, ModuleKey: "contacts"},
				{PlanID: proPlan.ID, ModuleKey: "products"},
				{PlanID: proPlan.ID, ModuleKey: "billing"},
				{PlanID: proPlan.ID, ModuleKey: "restaurant"},
				{PlanID: proPlan.ID, ModuleKey: "ecommerce"},
			}
			CentralDB.Create(&proModules)
		}
	}

	var docPkgCount int64
	CentralDB.Model(&SaasDocumentPackage{}).Count(&docPkgCount)
	if docPkgCount == 0 {
		CentralDB.Create([]SaasDocumentPackage{
			{Name: "50 documentos", Description: "Paquete adicional 50 comprobantes", DocumentsQty: 50, Price: 10, SortOrder: 1},
			{Name: "150 documentos", Description: "Paquete adicional 150 comprobantes", DocumentsQty: 150, Price: 20, SortOrder: 2},
			{Name: "500 documentos", Description: "Paquete adicional 500 comprobantes", DocumentsQty: 500, Price: 50, SortOrder: 3},
		})
	}

	// Ubigeo Perú: departamentos y provincias (y distritos si existe data_ubi.txt)
	if err := SeedUbigeoRegionesProvincias(CentralDB); err != nil {
		return err
	}
	_ = SeedUbigeoDistritos(CentralDB, UbigeoDistritosCSVPath())

	// Ajustes centrales (una sola fila)
	var ajusteCount int64
	CentralDB.Model(&CentralAjuste{}).Count(&ajusteCount)
	if ajusteCount == 0 {
		CentralDB.Create(&CentralAjuste{ID: 1, NombreSistema: "Tukifac"})
	}

	return nil
}

type TenantRole struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"size:100;not null" json:"name"`
	Description string         `gorm:"size:255" json:"description"`
	IsSystem    bool           `gorm:"default:false" json:"is_system"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type TenantPermission struct {
	ID     uint   `gorm:"primaryKey" json:"id"`
	Module string `gorm:"size:100;not null" json:"module"`
	Action string `gorm:"size:100;not null" json:"action"`
	Label  string `gorm:"size:255" json:"label"`
}

type TenantRolePermission struct {
	RoleID       uint `gorm:"primaryKey" json:"role_id"`
	PermissionID uint `gorm:"primaryKey" json:"permission_id"`
}

type TenantUser struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	RoleID    uint           `gorm:"not null;index" json:"role_id"`
	BranchID  *uint          `gorm:"index" json:"branch_id"` // legacy; usar home_branch_id
	HomeBranchID          *uint `gorm:"index" json:"home_branch_id"`
	CanSwitchBranch       bool  `gorm:"default:false" json:"can_switch_branch"`
	BranchSessionVersion  uint  `gorm:"default:0" json:"branch_session_version"`
	Name      string         `gorm:"size:255;not null" json:"name"`
	Email     string         `gorm:"size:255;uniqueIndex;not null" json:"email"`
	Password  string         `gorm:"size:255;not null" json:"-"`
	Phone     string         `gorm:"size:50" json:"phone"`
	Active    bool           `gorm:"default:true" json:"active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (u *TenantUser) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.Password = string(hash)
	return nil
}

func (u *TenantUser) CheckPassword(password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)) == nil
}

// TenantUserBranch asignación usuario ↔ sucursal (N:N).
type TenantUserBranch struct {
	UserID   uint `gorm:"primaryKey" json:"user_id"`
	BranchID uint `gorm:"primaryKey;index" json:"branch_id"`
}

func (TenantUserBranch) TableName() string { return "tenant_user_branches" }

type TenantBranch struct {
	ID                   uint           `gorm:"primaryKey" json:"id"`
	Name                 string         `gorm:"size:255;not null" json:"name"`
	Address              string         `gorm:"size:255" json:"address"`
	Phone                string         `gorm:"size:50" json:"phone"`
	FiscalDomicileCode   string         `gorm:"size:20" json:"fiscal_domicile_code"`
	IsMain               bool           `gorm:"default:false" json:"is_main"`
	Active    bool           `gorm:"default:true" json:"active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type TenantCompanyConfig struct {
	ID                     uint   `gorm:"primaryKey" json:"id"`
	DefaultBranchID        *uint  `gorm:"index" json:"default_branch_id,omitempty"`
	DefaultWalkInContactID *uint  `gorm:"index" json:"default_walk_in_contact_id,omitempty"`
	BusinessName           string `gorm:"size:255;not null" json:"business_name"`
	TradeName    string `gorm:"size:255" json:"trade_name"`
	RUC          string `gorm:"size:20;not null" json:"ruc"`
	Address      string `gorm:"size:255" json:"address"`
	Ubigeo       string `gorm:"size:10" json:"ubigeo"`
	Country      string `gorm:"size:5;default:'PE'" json:"country"`
	Phone        string `gorm:"size:50" json:"phone"`
	Email        string `gorm:"size:255" json:"email"`
	Website      string `gorm:"size:255" json:"website"`
	LogoURL      string `gorm:"type:longtext" json:"logo_url"`
	Currency     string `gorm:"size:10;default:'PEN'" json:"currency"`
	// Impuestos — configurable por empresa/régimen
	TaxRate        float64 `gorm:"type:decimal(5,2);default:18.00" json:"tax_rate"` // IGV vigente de la empresa
	IgvRegime      string  `gorm:"size:30;default:'standard'" json:"igv_regime"`    // standard | reduced | exonerated
	TaxBenefitZone bool    `gorm:"default:false" json:"tax_benefit_zone"`           // zona amazónica / selva
	// Facturación electrónica — solo metadatos en ERP (secretos en facturador SSOT)
	SunatEnabled           bool       `gorm:"default:false" json:"sunat_enabled"`
	SunatEnvMode           string     `gorm:"size:20;default:'demo'" json:"sunat_env_mode"`
	SendMode               string     `gorm:"size:30;default:'sunat_direct'" json:"send_mode"`
	FiscalProvider         string     `gorm:"size:50" json:"fiscal_provider"`
	FiscalConnectionType   string     `gorm:"size:20;default:'bearer'" json:"fiscal_connection_type"`
	FiscalConnectionStatus string     `gorm:"size:30" json:"fiscal_connection_status"`
	FiscalLastSyncAt       *time.Time `json:"fiscal_last_sync_at"`
	SunatConnected         bool       `gorm:"default:false" json:"sunat_connected"`
	AutomaticSend          bool       `gorm:"default:true" json:"automatic_send"`
	ColorTheme             string     `gorm:"size:30;default:'green'" json:"color_theme"`
	AdditionalNotes        string     `gorm:"type:text" json:"additional_notes"`
	// QR de pago Yape/Plin en comprobantes locales (PDF ticket / A4)
	WalletProvider     string `gorm:"size:20" json:"wallet_provider"`       // yape | plin
	WalletPhone        string `gorm:"size:30" json:"wallet_phone"`
	WalletQrURL        string `gorm:"type:longtext" json:"wallet_qr_url"`
	WalletShowOnA4     bool   `gorm:"default:false" json:"wallet_show_on_a4"`
	WalletShowOnTicket bool   `gorm:"default:false" json:"wallet_show_on_ticket"`
	// JSON array de IDs (tenant_bank_accounts). Vacío sin configurar = todas las activas; "[]" = ninguna.
	ReceiptBankAccountIDs string `gorm:"type:text" json:"receipt_bank_account_ids"`
	// Detracción SUNAT — cuenta Banco de la Nación del emisor y medio de pago default (cat. 59).
	DetractionBNAccount           string `gorm:"size:30" json:"detraction_bn_account"`
	DetractionDefaultPaymentMethod string `gorm:"size:10;default:'001'" json:"detraction_default_payment_method"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type TenantDocumentSeries struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	BranchID    uint      `gorm:"not null;index" json:"branch_id"`
	DocType     string    `gorm:"size:80;not null" json:"doc_type"`        // Nombre legible: "Factura Electrónica"
	SunatCode   string    `gorm:"size:10;default:'01'" json:"sunat_code"`  // Código SUNAT: 01, 03, 07…; 00 = no enviable a SUNAT (ej. nota de venta)
	Category    string    `gorm:"size:30;default:'venta'" json:"category"` // venta | nota_credito | nota_debito | retencion | percepcion | guia_remision | almacen
	Series      string    `gorm:"size:10;not null" json:"series"`
	Correlative uint      `gorm:"default:1" json:"correlative"`
	Active      bool      `gorm:"default:true" json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type TenantContact struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	Type          string         `gorm:"size:20;not null;default:'customer'" json:"type"` // customer, supplier, both
	DocType       string         `gorm:"size:20;default:'RUC'" json:"doc_type"`
	DocNumber     string         `gorm:"size:20;not null;index" json:"doc_number"`
	BusinessName  string         `gorm:"size:255;not null" json:"business_name"`
	TradeName     string         `gorm:"size:255" json:"trade_name"`
	Address       string         `gorm:"size:255" json:"address"`
	Ubigeo        string         `gorm:"size:6;index" json:"ubigeo"` // código 6 dígitos del distrito (SUNAT)
	Phone         string         `gorm:"size:50" json:"phone"`
	Email         string         `gorm:"size:255" json:"email"`
	PhotoURL      string         `gorm:"size:500" json:"photo_url"`
	ContactPerson string         `gorm:"size:255" json:"contact_person"`
	Notes                           string         `gorm:"type:text" json:"notes"`
	EsAgenteDeRetencion             bool           `gorm:"default:false" json:"es_agente_de_retencion"`
	EsAgenteDePercepcion            bool           `gorm:"default:false" json:"es_agente_de_percepcion"`
	EsAgenteDePercepcionCombustible bool           `gorm:"default:false" json:"es_agente_de_percepcion_combustible"`
	EsBuenContribuyente             bool           `gorm:"default:false" json:"es_buen_contribuyente"`
	IsDefaultWalkIn                 bool           `gorm:"default:false;index" json:"is_default_walkin"`
	Active                          bool           `gorm:"default:true" json:"active"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`

	ContactPersons []TenantContactPerson `gorm:"foreignKey:ContactID" json:"contact_persons,omitempty"`
}

// TenantContactPerson persona de contacto adicional (cliente o proveedor).
type TenantContactPerson struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	ContactID    uint      `gorm:"not null;index" json:"contact_id"`
	Name         string    `gorm:"size:255;not null" json:"name"`
	Phone        string    `gorm:"size:50" json:"phone"`
	Email        string    `gorm:"size:255" json:"email"`
	Relationship string    `gorm:"size:100" json:"relationship"` // parentesco
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TenantCategory struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"size:255;not null" json:"name"`
	Description string         `gorm:"size:255" json:"description"`
	ParentID    *uint          `gorm:"index" json:"parent_id"`
	Active      bool           `gorm:"default:true" json:"active"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type TenantProduct struct {
	ID                 uint           `gorm:"primaryKey" json:"id"`
	CategoryID         *uint          `gorm:"index" json:"category_id"`
	Code               string         `gorm:"size:100;not null;index" json:"code"`
	Name               string         `gorm:"size:255;not null" json:"name"`
	Description        string         `gorm:"type:text" json:"description"`
	Type               string         `gorm:"size:20;default:'product'" json:"type"` // product, service
	Unit               string         `gorm:"size:50;default:'NIU'" json:"unit"`
	SalePrice          float64        `gorm:"type:decimal(15,2);not null" json:"sale_price"`
	PurchasePrice      float64        `gorm:"type:decimal(15,2)" json:"purchase_price"`
	TaxRate            float64        `gorm:"type:decimal(5,2);default:18.00" json:"tax_rate"`
	IgvAffectationType string         `gorm:"size:10;default:'10'" json:"igv_affectation_type"` // Catálogo SUNAT N°7
	PriceIncludesIgv   bool           `gorm:"default:true" json:"price_includes_igv"`
	ManageStock        bool           `gorm:"default:true" json:"manage_stock"`
	ManageSeries       bool           `gorm:"default:false" json:"manage_series"`
	HasVariants        bool           `gorm:"default:false" json:"has_variants"`
	HasModifiers       bool           `gorm:"default:false" json:"has_modifiers"`
	IsRestaurant       bool           `gorm:"default:false" json:"is_restaurant"`
	BranchID           uint           `gorm:"index" json:"branch_id"` // platos Tukichef: sucursal dueña del catálogo
	PreparationArea    string         `gorm:"size:50" json:"preparation_area"` // solo restaurante: cocina, bar, barra, etc.
	MinStock           float64        `gorm:"type:decimal(15,3);default:0" json:"min_stock"`
	ImageURL           string         `gorm:"size:255" json:"image_url"`
	Active             bool           `gorm:"default:true" json:"active"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	DeletedAt          gorm.DeletedAt `gorm:"index" json:"-"`
}

// TenantProductSerial rastrea números de serie individuales por producto y sucursal.
type TenantProductSerial struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ProductID      uint      `gorm:"not null;index" json:"product_id"`
	BranchID       uint      `gorm:"not null;index" json:"branch_id"`
	Serial         string    `gorm:"size:200;not null" json:"serial"`
	Status         string    `gorm:"size:30;default:'available'" json:"status"` // available, reserved, sold, transferred, in_transit
	SaleItemID     *uint     `gorm:"index" json:"sale_item_id"`
	PurchaseItemID *uint     `gorm:"index" json:"purchase_item_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TenantProductPresentation: tamaño/envase propio de cada producto (reemplaza precio base en POS).
type TenantProductPresentation struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	ProductID uint           `gorm:"not null;index" json:"product_id"`
	Name      string         `gorm:"size:120;not null" json:"name"`
	SalePrice float64        `gorm:"type:decimal(15,2);not null" json:"sale_price"`
	SortOrder int            `gorm:"default:0" json:"sort_order"`
	Active    bool           `gorm:"default:true" json:"active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TenantModifierGroup: extras reutilizables entre productos (suman al precio).
type TenantModifierGroup struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"size:100;not null" json:"name"`
	Kind        string         `gorm:"size:20;default:extra" json:"kind"` // presentation | extra
	Required    bool           `gorm:"default:false" json:"required"`
	MultiSelect bool           `gorm:"default:false" json:"multi_select"`
	Active      bool           `gorm:"default:true" json:"active"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TenantModifierOption define una opción dentro de un grupo (ej: Rojo, XL).
type TenantModifierOption struct {
	ID         uint    `gorm:"primaryKey" json:"id"`
	GroupID    uint    `gorm:"not null;index" json:"group_id"`
	Name       string  `gorm:"size:100;not null" json:"name"`
	ExtraPrice float64 `gorm:"type:decimal(15,2);default:0" json:"extra_price"`
	Active     bool    `gorm:"default:true" json:"active"`
}

// TenantProductModifierGroup vincula un producto con sus grupos de modificadores.
type TenantProductModifierGroup struct {
	ID        uint `gorm:"primaryKey" json:"id"`
	ProductID uint `gorm:"not null;uniqueIndex:product_modifier_uidx" json:"product_id"`
	GroupID   uint `gorm:"not null;uniqueIndex:product_modifier_uidx" json:"group_id"`
}

type TenantProductStock struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ProductID uint      `gorm:"not null;index" json:"product_id"`
	BranchID  uint      `gorm:"not null;index" json:"branch_id"`
	Quantity  float64   `gorm:"type:decimal(15,3);default:0" json:"quantity"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TenantStockMovement struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ProductID uint      `gorm:"not null;index" json:"product_id"`
	BranchID  uint      `gorm:"not null;index" json:"branch_id"`
	Type      string    `gorm:"size:30;not null" json:"type"` // in, out, transfer, adjustment
	Quantity  float64   `gorm:"type:decimal(15,3);not null" json:"quantity"`
	UnitCost  float64   `gorm:"type:decimal(15,2)" json:"unit_cost"`
	Balance   float64   `gorm:"type:decimal(15,3)" json:"balance"`
	Reference string    `gorm:"size:100" json:"reference"`
	Notes     string    `gorm:"type:text" json:"notes"`
	UserID    uint      `gorm:"index" json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// TenantTransfer cabecera de transferencia entre sucursales. Flujo: pending → confirmada en destino; solo se puede cancelar si pending.
type TenantTransfer struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	FromBranchID uint       `gorm:"not null;index" json:"from_branch_id"`
	ToBranchID   uint       `gorm:"not null;index" json:"to_branch_id"`
	Status       string     `gorm:"size:20;default:'pending';index" json:"status"` // pending, confirmed, cancelled
	Notes        string     `gorm:"type:text" json:"notes"`
	CreatedAt    time.Time  `json:"created_at"`
	CreatedBy    uint       `gorm:"not null;index" json:"created_by"`
	ConfirmedAt  *time.Time `gorm:"index" json:"confirmed_at"`
	ConfirmedBy  *uint      `gorm:"index" json:"confirmed_by"`
}

// TenantTransferLog línea de una transferencia (producto + cantidad/series). Con transfer_id agrupado por cabecera.
// Sin transfer_id = registro legacy (flujo antiguo sin estados).
type TenantTransferLog struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	TransferID   *uint      `gorm:"index" json:"transfer_id"` // nil = legacy
	ProductID    uint       `gorm:"not null;index" json:"product_id"`
	FromBranchID uint       `gorm:"not null;index" json:"from_branch_id"`
	ToBranchID   uint       `gorm:"not null;index" json:"to_branch_id"`
	Quantity     float64    `gorm:"type:decimal(15,3);not null" json:"quantity"`
	SerialsJSON  string     `gorm:"type:text" json:"serials_json"` // JSON array de strings; vacío si no es producto con series
	UserID       uint       `gorm:"not null;index" json:"user_id"`
	Notes        string     `gorm:"type:text" json:"notes"`
	CreatedAt    time.Time  `json:"created_at"`
	RevertedAt   *time.Time `gorm:"index" json:"reverted_at"` // legacy: si no nil, anulada (flujo antiguo)
}

type TenantSale struct {
	ID             uint           `gorm:"primaryKey" json:"id"`
	BranchID       uint           `gorm:"not null;index" json:"branch_id"`
	ContactID      *uint          `gorm:"index" json:"contact_id"`
	UserID         uint           `gorm:"not null;index" json:"user_id"`
	CashSessionID  *uint          `gorm:"index" json:"cash_session_id"`
	SeriesID       uint           `gorm:"not null;index" json:"series_id"`
	DocType        string         `gorm:"size:50;not null" json:"doc_type"`
	Series         string         `gorm:"size:10;not null" json:"series"`
	Correlative    uint           `gorm:"not null" json:"correlative"`
	Number         string         `gorm:"size:20;not null;index" json:"number"`
	IssueDate      time.Time      `gorm:"not null;index" json:"issue_date"`
	DueDate        *time.Time     `json:"due_date"`
	Subtotal       float64        `gorm:"type:decimal(15,2);not null" json:"subtotal"`
	TaxAmount      float64        `gorm:"type:decimal(15,2);not null" json:"tax_amount"`
	Total          float64        `gorm:"type:decimal(15,2);not null" json:"total"`
	Currency            string   `gorm:"size:10;default:'PEN'" json:"currency"`
	OperationTypeCode   string   `gorm:"size:10;default:'0101'" json:"operation_type_code"`
	ExchangeRate        *float64 `gorm:"type:decimal(10,4)" json:"exchange_rate,omitempty"`
	PaymentMethod       string   `gorm:"size:50" json:"payment_method"`
	Notes          string         `gorm:"type:text" json:"notes"`
	Status         string         `gorm:"size:30;default:'paid'" json:"status"`            // draft, paid, cancelled, credit
	BillingStatus  string         `gorm:"size:30;default:'pending'" json:"billing_status"` // pending, sent, accepted, rejected
	RestaurantSessionID *uint     `gorm:"index" json:"restaurant_session_id,omitempty"` // pedido restaurante que originó la venta
	OriginalSaleID *uint          `gorm:"index" json:"original_sale_id"`                   // Si es NOTA_CREDITO: venta que se anuló
	// Si esta venta es factura/boleta (01/03) generada desde una nota de venta (00), apunta al ID de esa NV.
	IssuedFromNotaSaleID *uint `gorm:"index" json:"issued_from_nota_sale_id,omitempty"`
	// Origen comercial: direct | converted_from_nota | api | migration | legacy
	SaleOrigin string `gorm:"size:30;default:direct;index" json:"sale_origin"`
	// Venta generada desde una cotización (pre venta).
	IssuedFromQuotationID *uint `gorm:"index" json:"issued_from_quotation_id,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
	DeletedAt              gorm.DeletedAt `gorm:"index" json:"-"`

	// ContactName se rellena al listar (join con tenant_contacts), no es columna en BD
	ContactName string `gorm:"-" json:"contact_name"`
	// ID de la venta electrónica (01/03) emitida desde esta NV; solo listados NV.
	ElectronicIssueSaleID *uint `gorm:"-" json:"electronic_issue_sale_id,omitempty"`
	ElectronicIssueDocType string `gorm:"-" json:"electronic_issue_doc_type,omitempty"`
	ElectronicIssueSeries  string `gorm:"-" json:"electronic_issue_series,omitempty"`
	ElectronicIssueNumber  string `gorm:"-" json:"electronic_issue_number,omitempty"`
	// Estado NV: registrado | convertida | anulada (solo comprobantes NV).
	NvStatus string `gorm:"-" json:"nv_status,omitempty"`
	// Documento comercial vigente para reportes (hijo FE si NV convertida).
	DisplaySaleID   *uint  `gorm:"-" json:"display_sale_id,omitempty"`
	DisplayDocType  string `gorm:"-" json:"display_doc_type,omitempty"`
	DisplaySeries   string `gorm:"-" json:"display_series,omitempty"`
	DisplayNumber   string `gorm:"-" json:"display_number,omitempty"`
	// Detracción 1001 (join tenant_sale_detraccion); no es columna en BD.
	HasDetraccion          bool    `gorm:"-" json:"has_detraccion,omitempty"`
	DetraccionAmount       float64 `gorm:"-" json:"detraccion_amount,omitempty"`
	NetPayable             float64 `gorm:"-" json:"net_payable,omitempty"`
	DetraccionRatePercent  float64 `gorm:"-" json:"detraccion_rate_percent,omitempty"`
}

type TenantSaleItem struct {
	ID                 uint    `gorm:"primaryKey" json:"id"`
	SaleID             uint    `gorm:"not null;index" json:"sale_id"`
	ProductID          *uint   `gorm:"index" json:"product_id"`
	Code               string  `gorm:"size:100" json:"code"`
	Description        string  `gorm:"size:255;not null" json:"description"`
	Unit               string  `gorm:"size:50" json:"unit"`
	Quantity           float64 `gorm:"type:decimal(15,3);not null" json:"quantity"`
	UnitPrice          float64 `gorm:"type:decimal(15,2);not null" json:"unit_price"`
	Discount           float64 `gorm:"type:decimal(15,2);default:0" json:"discount"`
	TaxRate            float64 `gorm:"type:decimal(5,2);default:0" json:"tax_rate"`
	IgvAffectationType string  `gorm:"size:10;default:'10'" json:"igv_affectation_type"`
	Subtotal           float64 `gorm:"type:decimal(15,2);not null" json:"subtotal"`
	TaxAmount          float64 `gorm:"type:decimal(15,2);not null" json:"tax_amount"`
	Total              float64 `gorm:"type:decimal(15,2);not null" json:"total"`
	ModifiersJSON      string  `gorm:"type:text" json:"modifiers_json"` // JSON array de { option_id, name, extra_price } para el detalle
}

// TenantSaleFiscalProfile información adicional fiscal de una venta (1:1).
type TenantSaleFiscalProfile struct {
	SaleID                     uint      `gorm:"primaryKey" json:"sale_id"`
	SchemaVersion              int       `gorm:"default:1" json:"schema_version"`
	OperationTypeCode          string    `gorm:"size:10;default:'0101'" json:"operation_type_code"`
	HasIgvRetention            bool      `gorm:"default:false" json:"has_igv_retention"`
	IgvRetentionManualOverride bool      `gorm:"default:false" json:"igv_retention_manual_override"`
	ShowTermsConditions        bool      `gorm:"default:false" json:"show_terms_conditions"`
	FiscalObservations         string    `gorm:"type:text" json:"fiscal_observations"`
	PurchaseOrderNumber        string    `gorm:"size:50" json:"purchase_order_number"`
	SellerUserID               *uint     `gorm:"index" json:"seller_user_id,omitempty"`
	CreatedByUserID            uint      `gorm:"index" json:"created_by_user_id"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

func (TenantSaleFiscalProfile) TableName() string { return "tenant_sale_fiscal_profiles" }

// TenantSaleFiscalReference documento relacionado con la venta (guías, etc.).
type TenantSaleFiscalReference struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	SaleID               uint      `gorm:"not null;index" json:"sale_id"`
	ReferenceKind        string    `gorm:"size:40;not null;index" json:"reference_kind"`
	ReferencedSunatType  string    `gorm:"size:10" json:"referenced_sunat_type"`
	ReferencedSeries     string    `gorm:"size:20" json:"referenced_series"`
	ReferencedNumber     string    `gorm:"size:20" json:"referenced_number"`
	ReferencedFullNumber string    `gorm:"size:40" json:"referenced_full_number"`
	ReferencedSaleID     *uint     `gorm:"index" json:"referenced_sale_id,omitempty"`
	SortOrder            int       `gorm:"default:0" json:"sort_order"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func (TenantSaleFiscalReference) TableName() string { return "tenant_sale_fiscal_references" }

// TenantSaleFiscalObligation obligación fiscal calculada (retención, percepción, etc.).
type TenantSaleFiscalObligation struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	SaleID              uint      `gorm:"not null;index" json:"sale_id"`
	ObligationKind      string    `gorm:"size:40;not null;index" json:"obligation_kind"`
	Direction           string    `gorm:"size:40;not null" json:"direction"`
	RatePercent         float64   `gorm:"type:decimal(7,4);default:0" json:"rate_percent"`
	BaseAmount          float64   `gorm:"type:decimal(15,6);default:0" json:"base_amount"`
	ObligationAmount    float64   `gorm:"type:decimal(15,6);default:0" json:"obligation_amount"`
	Currency            string    `gorm:"size:10;default:'PEN'" json:"currency"`
	ApplicabilityStatus string    `gorm:"size:30;not null" json:"applicability_status"`
	ApplicabilityReason string    `gorm:"type:text" json:"applicability_reason"`
	Source              string    `gorm:"size:30;not null" json:"source"`
	Status              string    `gorm:"size:30;default:'pending'" json:"status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (TenantSaleFiscalObligation) TableName() string { return "tenant_sale_fiscal_obligations" }

// TenantSaleDetraccion datos de detracción SUNAT (1:1 con venta factura 1001).
type TenantSaleDetraccion struct {
	SaleID              uint      `gorm:"primaryKey" json:"sale_id"`
	GoodCode            string    `gorm:"size:10;not null" json:"good_code"`
	PaymentMethodCode   string    `gorm:"size:10;not null" json:"payment_method_code"`
	BankAccount         string    `gorm:"size:30;not null" json:"bank_account"`
	RatePercent         float64   `gorm:"type:decimal(5,2);not null" json:"rate_percent"`
	BaseAmountPen       float64   `gorm:"type:decimal(15,2);not null" json:"base_amount_pen"`
	DetractionAmountPen float64   `gorm:"type:decimal(15,2);not null" json:"detraction_amount_pen"`
	InvoiceTotalPen     float64   `gorm:"type:decimal(15,2);not null" json:"invoice_total_pen"`
	NetPayablePen              float64    `gorm:"type:decimal(15,2);not null" json:"net_payable_pen"`
	BnConfirmationStatus       string     `gorm:"size:20;default:'pending'" json:"bn_confirmation_status"`
	BnConfirmedAt              *time.Time `json:"bn_confirmed_at,omitempty"`
	BnConfirmationReference    string     `gorm:"size:100" json:"bn_confirmation_reference,omitempty"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

func (TenantSaleDetraccion) TableName() string { return "tenant_sale_detraccion" }

// TenantQuotation — cotización comercial (pre venta); no afecta inventario ni caja.
type TenantQuotation struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	BranchID          uint       `gorm:"not null;index" json:"branch_id"`
	ContactID         *uint      `gorm:"index" json:"contact_id"`
	UserID            uint       `gorm:"not null;index" json:"user_id"`
	SeriesID          uint       `gorm:"not null;index" json:"series_id"`
	Series            string     `gorm:"size:10;not null" json:"series"`
	Correlative       uint       `gorm:"not null" json:"correlative"`
	Number            string     `gorm:"size:20;not null;index" json:"number"`
	IssueDate         time.Time  `gorm:"not null;index" json:"issue_date"`
	ValidUntil        *time.Time `json:"valid_until,omitempty"`
	Subtotal          float64    `gorm:"type:decimal(15,2);not null" json:"subtotal"`
	TaxAmount         float64    `gorm:"type:decimal(15,2);not null" json:"tax_amount"`
	Total             float64    `gorm:"type:decimal(15,2);not null" json:"total"`
	Currency          string     `gorm:"size:10;default:'PEN'" json:"currency"`
	ExchangeRate      *float64   `gorm:"type:decimal(10,4)" json:"exchange_rate,omitempty"`
	Notes             string     `gorm:"type:text" json:"notes"`
	Status            string     `gorm:"size:30;default:'draft';index" json:"status"` // draft | converted
	ConvertedSaleID   *uint      `gorm:"index" json:"converted_sale_id,omitempty"`
	ConvertedAt       *time.Time `json:"converted_at,omitempty"`
	ConvertedTarget   string     `gorm:"size:30" json:"converted_target,omitempty"` // nota_venta | 01 | 03
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ContactName       string     `gorm:"-" json:"contact_name"`
}

func (TenantQuotation) TableName() string { return "tenant_quotations" }

type TenantQuotationItem struct {
	ID                 uint    `gorm:"primaryKey" json:"id"`
	QuotationID        uint    `gorm:"not null;index" json:"quotation_id"`
	ProductID          *uint   `gorm:"index" json:"product_id"`
	Code               string  `gorm:"size:100" json:"code"`
	Description        string  `gorm:"size:255;not null" json:"description"`
	Unit               string  `gorm:"size:50" json:"unit"`
	Quantity           float64 `gorm:"type:decimal(15,3);not null" json:"quantity"`
	UnitPrice          float64 `gorm:"type:decimal(15,2);not null" json:"unit_price"`
	Discount           float64 `gorm:"type:decimal(15,2);default:0" json:"discount"`
	TaxRate            float64 `gorm:"type:decimal(5,2);default:0" json:"tax_rate"`
	IgvAffectationType string  `gorm:"size:10;default:'10'" json:"igv_affectation_type"`
	PriceIncludesIgv   bool    `gorm:"default:true" json:"price_includes_igv"`
	Subtotal           float64 `gorm:"type:decimal(15,2);not null" json:"subtotal"`
	TaxAmount          float64 `gorm:"type:decimal(15,2);not null" json:"tax_amount"`
	Total              float64 `gorm:"type:decimal(15,2);not null" json:"total"`
	ModifiersJSON      string  `gorm:"type:text" json:"modifiers_json"`
}

func (TenantQuotationItem) TableName() string { return "tenant_quotation_items" }

type TenantInvoice struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	SaleID            uint       `gorm:"uniqueIndex;not null" json:"sale_id"`
	ExternalID        string     `gorm:"size:100;index" json:"external_id"`
	SunatStatus       string     `gorm:"size:50" json:"sunat_status"`
	SunatMessage      string     `gorm:"type:text" json:"sunat_message"`
	SunatCDRCode      string     `gorm:"size:20" json:"sunat_cdr_code"`    // Código SUNAT (0=aceptado, 3205 etc.=rechazo). Según RESPUESTA-SUNAT-BACKEND.md
	SunatCDRNotes     string     `gorm:"type:text" json:"sunat_cdr_notes"` // Notas del CDR (JSON array o texto) para mostrar en panel
	SunatHash         string     `gorm:"size:500" json:"sunat_hash"`       // Hash de la firma del XML (Lycet); para generar QR en el PDF
	LycetResponseJSON string     `gorm:"type:longtext" json:"-"`           // Respuesta completa de Lycet (xml, hash, sunatResponse); se actualiza en cada envío/reenvío
	PayloadJSON       string     `gorm:"type:longtext" json:"-"`           // Payload completo para reenvío (según PAYLOAD-FACTURA-BOLETA.md)
	NotePayloadJSON   string     `gorm:"type:longtext" json:"-"`           // Payload nota de crédito/débito (sale NOTA_CREDITO); para PDF/XML desde Lycet
	XMLURL            string     `gorm:"size:500" json:"xml_url"`
	CDRURL            string     `gorm:"size:500" json:"cdr_url"`
	PDFURL            string     `gorm:"size:500" json:"pdf_url"`
	SentAt            *time.Time `json:"sent_at"`
	ResponseAt        *time.Time `json:"response_at"`
	RetryCount        int        `gorm:"default:0" json:"retry_count"`
	JobStatus         string     `gorm:"size:30;index;default:'pending'" json:"job_status"` // pending, processing, sent, failed, retrying, dead_letter
	PipelineStatus    string     `gorm:"size:40;index;default:'DRAFT'" json:"pipeline_status"` // máquina de estados billingstate
	IdempotencyKey    string     `gorm:"size:128;index" json:"idempotency_key"`
	JobLastError      string     `gorm:"type:text" json:"job_last_error,omitempty"`
	NextRetryAt       *time.Time `gorm:"index" json:"next_retry_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// TenantSunatSummary registra un resumen diario enviado a SUNAT (ticket; se consulta estado hasta obtener CDR).
type TenantSunatSummary struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	FecResumen   time.Time `gorm:"not null;index" json:"fec_resumen"` // Fecha del día reportado
	Correlativo  string    `gorm:"size:20;not null" json:"correlativo"`
	Ticket       string    `gorm:"size:100;index" json:"ticket"`
	Status       string    `gorm:"size:30;default:'pending'" json:"status"` // pending, accepted, rejected, error
	SunatCode    string    `gorm:"size:20" json:"sunat_code"`
	SunatMessage string    `gorm:"type:text" json:"sunat_message"`
	CDRURL       string    `gorm:"size:500" json:"cdr_url"`
	PayloadJSON  string    `gorm:"type:longtext" json:"-"`
	DetailsCount int       `gorm:"default:0" json:"details_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TenantSunatVoided registra una comunicación de baja enviada a SUNAT (ticket o CDR directo).
type TenantSunatVoided struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	FecComunicacion time.Time `gorm:"not null;index" json:"fec_comunicacion"`
	Correlativo     string    `gorm:"size:20;not null" json:"correlativo"`
	Ticket          string    `gorm:"size:100;index" json:"ticket"`
	Status          string    `gorm:"size:30;default:'pending'" json:"status"` // pending, accepted, rejected, error
	SunatCode       string    `gorm:"size:20" json:"sunat_code"`
	SunatMessage    string    `gorm:"type:text" json:"sunat_message"`
	CDRURL          string    `gorm:"size:500" json:"cdr_url"`
	PayloadJSON     string    `gorm:"type:longtext" json:"-"`
	DetailsCount    int       `gorm:"default:0" json:"details_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TenantGreCarrier transportista para guías GRE (SUNAT CarrierParty).
type TenantGreCarrier struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	DocType       string    `gorm:"size:2;not null;default:'6'" json:"doc_type"`
	DocNumber     string    `gorm:"size:20;not null;index" json:"doc_number"`
	BusinessName  string    `gorm:"size:255;not null" json:"business_name"`
	FiscalAddress string    `gorm:"size:500" json:"fiscal_address"`
	MTCNumber     string    `gorm:"size:50" json:"mtc_number"`
	IsDefault     bool      `gorm:"default:false;index" json:"is_default"`
	Active        bool      `gorm:"default:true;index" json:"active"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (TenantGreCarrier) TableName() string { return "tenant_gre_carriers" }

// TenantGreDriver conductor para guías GRE.
type TenantGreDriver struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	DocType       string    `gorm:"size:2;not null;default:'1'" json:"doc_type"`
	DocNumber     string    `gorm:"size:20;not null;index" json:"doc_number"`
	FullName      string    `gorm:"size:255;not null" json:"full_name"`
	LicenseNumber string    `gorm:"size:50" json:"license_number"`
	Phone         string    `gorm:"size:30" json:"phone"`
	CarrierID     *uint     `gorm:"index" json:"carrier_id,omitempty"`
	IsDefault     bool      `gorm:"default:false;index" json:"is_default"`
	Active        bool      `gorm:"default:true;index" json:"active"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (TenantGreDriver) TableName() string { return "tenant_gre_drivers" }

// TenantGreVehicle vehículo para guías GRE.
type TenantGreVehicle struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	Plate            string    `gorm:"size:20;not null;uniqueIndex" json:"plate"`
	Brand            string    `gorm:"size:80" json:"brand"`
	Model            string    `gorm:"size:80" json:"model"`
	HabilitationCert string    `gorm:"size:100" json:"habilitation_cert"`
	CarrierID        *uint     `gorm:"index" json:"carrier_id,omitempty"`
	IsDefault        bool      `gorm:"default:false;index" json:"is_default"`
	Active           bool      `gorm:"default:true;index" json:"active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (TenantGreVehicle) TableName() string { return "tenant_gre_vehicles" }

// TenantDespatch guía de remisión enviada a SUNAT (remitente o transportista).
type TenantDespatch struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	SaleID            *uint     `gorm:"index" json:"sale_id,omitempty"`
	BranchID          uint      `gorm:"not null;index" json:"branch_id"`
	SeriesID          uint      `gorm:"not null;index" json:"series_id"`
	Series            string    `gorm:"size:20;not null" json:"series"`
	Correlative       uint      `gorm:"not null" json:"correlative"`
	IssueDate         time.Time `gorm:"not null;index" json:"issue_date"`
	DestinatarioRUC   string    `gorm:"size:20" json:"destinatario_ruc"`
	DestinatarioRazon string    `gorm:"size:255" json:"destinatario_razon"`
	Ticket            string    `gorm:"size:100;index" json:"ticket"`
	Status            string    `gorm:"size:30;default:'pending'" json:"status"` // pending, accepted, rejected, error
	SunatCode         string    `gorm:"size:20" json:"sunat_code"`
	SunatMessage      string    `gorm:"type:text" json:"sunat_message"`
	CDRURL            string    `gorm:"size:500" json:"cdr_url"`
	PayloadJSON       string    `gorm:"type:longtext" json:"-"`
	DetailsCount      int       `gorm:"default:0" json:"details_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TenantRetention comprobante de retención enviado a SUNAT.
type TenantRetention struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	SaleID         *uint     `gorm:"index" json:"sale_id,omitempty"`
	Series         string    `gorm:"size:20;not null" json:"series"`
	Correlative    string    `gorm:"size:20;not null" json:"correlative"`
	FechaEmision   time.Time `gorm:"not null;index" json:"fecha_emision"`
	ProveedorRUC   string    `gorm:"size:20" json:"proveedor_ruc"`
	ProveedorRazon string    `gorm:"size:255" json:"proveedor_razon"`
	Regimen        string    `gorm:"size:20" json:"regimen"`
	Tasa           float64   `gorm:"type:decimal(5,2)" json:"tasa"`
	ImpRetenido    float64   `gorm:"type:decimal(15,2)" json:"imp_retenido"`
	ImpPagado      float64   `gorm:"type:decimal(15,2)" json:"imp_pagado"`
	Status         string    `gorm:"size:30;default:'pending'" json:"status"`
	SunatCode      string    `gorm:"size:20" json:"sunat_code"`
	SunatMessage   string    `gorm:"type:text" json:"sunat_message"`
	CDRURL         string    `gorm:"size:500" json:"cdr_url"`
	PayloadJSON    string    `gorm:"type:longtext" json:"-"`
	DetailsCount   int       `gorm:"default:0" json:"details_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TenantPerception comprobante de percepción enviado a SUNAT.
type TenantPerception struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	SaleID         *uint     `gorm:"index" json:"sale_id,omitempty"`
	Series         string    `gorm:"size:20;not null" json:"series"`
	Correlative    string    `gorm:"size:20;not null" json:"correlative"`
	FechaEmision   time.Time `gorm:"not null;index" json:"fecha_emision"`
	ProveedorRUC   string    `gorm:"size:20" json:"proveedor_ruc"`
	ProveedorRazon string    `gorm:"size:255" json:"proveedor_razon"`
	Regimen        string    `gorm:"size:20" json:"regimen"`
	Tasa           float64   `gorm:"type:decimal(5,2)" json:"tasa"`
	ImpPercibido   float64   `gorm:"type:decimal(15,2)" json:"imp_percibido"`
	ImpCobrado     float64   `gorm:"type:decimal(15,2)" json:"imp_cobrado"`
	Status         string    `gorm:"size:30;default:'pending'" json:"status"`
	SunatCode      string    `gorm:"size:20" json:"sunat_code"`
	SunatMessage   string    `gorm:"type:text" json:"sunat_message"`
	CDRURL         string    `gorm:"size:500" json:"cdr_url"`
	PayloadJSON    string    `gorm:"type:longtext" json:"-"`
	DetailsCount   int       `gorm:"default:0" json:"details_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TenantSunatReversion comunicación de reversión enviada a SUNAT (mismo esquema que voided).
type TenantSunatReversion struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	FecComunicacion time.Time `gorm:"not null;index" json:"fec_comunicacion"`
	Correlativo     string    `gorm:"size:20;not null" json:"correlativo"`
	Ticket          string    `gorm:"size:100;index" json:"ticket"`
	Status          string    `gorm:"size:30;default:'pending'" json:"status"`
	SunatCode       string    `gorm:"size:20" json:"sunat_code"`
	SunatMessage    string    `gorm:"type:text" json:"sunat_message"`
	CDRURL          string    `gorm:"size:500" json:"cdr_url"`
	PayloadJSON     string    `gorm:"type:longtext" json:"-"`
	DetailsCount    int       `gorm:"default:0" json:"details_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type TenantPurchase struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	BranchID      uint           `gorm:"not null;index" json:"branch_id"`
	ContactID     *uint          `gorm:"index" json:"contact_id"`
	UserID        uint           `gorm:"not null;index" json:"user_id"`
	DocType       string         `gorm:"size:50;not null" json:"doc_type"`
	Series        string         `gorm:"size:20;not null" json:"series"`
	Number        string         `gorm:"size:50;not null" json:"number"`
	IssueDate     time.Time      `gorm:"not null" json:"issue_date"`
	DueDate       *time.Time     `json:"due_date"`
	Subtotal      float64        `gorm:"type:decimal(15,2);not null" json:"subtotal"`
	TaxAmount     float64        `gorm:"type:decimal(15,2);not null" json:"tax_amount"`
	Total         float64        `gorm:"type:decimal(15,2);not null" json:"total"`
	Currency      string         `gorm:"size:10;default:'PEN'" json:"currency"`
	PaymentMethod string         `gorm:"size:50" json:"payment_method"`
	Notes         string         `gorm:"type:text" json:"notes"`
	Status        string         `gorm:"size:30;default:'received'" json:"status"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

type TenantPurchaseItem struct {
	ID                 uint    `gorm:"primaryKey" json:"id"`
	PurchaseID         uint    `gorm:"not null;index" json:"purchase_id"`
	ProductID          *uint   `gorm:"index" json:"product_id"`
	Code               string  `gorm:"size:100" json:"code"`
	Description        string  `gorm:"size:255;not null" json:"description"`
	Unit               string  `gorm:"size:50" json:"unit"`
	Quantity           float64 `gorm:"type:decimal(15,3);not null" json:"quantity"`
	UnitCost           float64 `gorm:"type:decimal(15,2);not null" json:"unit_cost"`
	TaxRate            float64 `gorm:"type:decimal(5,2);default:0" json:"tax_rate"`
	IgvAffectationType string  `gorm:"size:10;default:'10'" json:"igv_affectation_type"`
	Subtotal           float64 `gorm:"type:decimal(15,2);not null" json:"subtotal"`
	TaxAmount          float64 `gorm:"type:decimal(15,2);not null" json:"tax_amount"`
	Total              float64 `gorm:"type:decimal(15,2);not null" json:"total"`
}

type TenantCashSession struct {
	ID              uint           `gorm:"primaryKey" json:"id"`
	BranchID        uint           `gorm:"not null;index" json:"branch_id"`
	UserID          uint           `gorm:"not null;index" json:"user_id"`
	OpenedBy        uint           `gorm:"not null" json:"opened_by"`
	RegisterCode    *string        `gorm:"size:50" json:"register_code,omitempty"` // Fase C: punto de caja físico
	RegisterName    *string        `gorm:"size:100" json:"register_name,omitempty"`
	ClosedBy        *uint          `json:"closed_by"`
	OpeningBalance  float64        `gorm:"type:decimal(15,2);default:0" json:"opening_balance"`
	ClosingBalance  *float64       `gorm:"type:decimal(15,2)" json:"closing_balance"`
	ExpectedBalance *float64       `gorm:"type:decimal(15,2)" json:"expected_balance"`
	Difference      *float64       `gorm:"type:decimal(15,2)" json:"difference"`
	ArqueoJSON      string         `gorm:"type:text" json:"arqueo_json"` // JSON: {"200":5,"100":10,...} = cantidad por denominación; vacío = sin arqueo
	Notes           string         `gorm:"type:text" json:"notes"`
	Status          string         `gorm:"size:20;default:'open'" json:"status"` // open, closed
	OpenedAt        time.Time      `json:"opened_at"`
	ClosedAt        *time.Time     `json:"closed_at"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
}

type TenantCashMovement struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	CashSessionID uint      `gorm:"not null;index" json:"cash_session_id"`
	Type          string    `gorm:"size:20;not null" json:"type"` // income, expense
	Amount        float64   `gorm:"type:decimal(15,2);not null" json:"amount"`
	PaymentMethod string    `gorm:"size:50" json:"payment_method"` // para movimientos manuales: efectivo, yape, plin, tarjeta, transferencia
	Category      string    `gorm:"size:100" json:"category"`
	Reference     string    `gorm:"size:100" json:"reference"`
	SaleID        *uint     `gorm:"index" json:"sale_id"`
	PurchaseID    *uint     `gorm:"index" json:"purchase_id"`
	Notes         string    `gorm:"type:text" json:"notes"`
	UserID        uint      `gorm:"not null" json:"user_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// TenantPaymentMethod medios reales de cobro (efectivo, Yape, Plin, etc.).
// Solo códigos operativos: cash, yape, plin, transferencia, tarjeta.
type TenantPaymentMethod struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	Code          string         `gorm:"size:50;not null;uniqueIndex" json:"code"`
	Name          string         `gorm:"size:100;not null" json:"name"`
	BankAccountID *uint          `gorm:"index" json:"bank_account_id"`
	IsSystem      bool           `gorm:"default:false" json:"is_system"`
	SortOrder     int            `gorm:"default:0" json:"sort_order"`
	Active        bool           `gorm:"default:true" json:"active"`
	// Legacy column — se elimina en v075; GORM lo omite en BD nueva sin la columna.
	DestinationType string `gorm:"size:20" json:"destination_type,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
}

// TenantPaymentCondition condición comercial de la venta (contado / crédito).
type TenantPaymentCondition struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Code      string         `gorm:"size:50;not null;uniqueIndex" json:"code"`
	Name      string         `gorm:"size:100;not null" json:"name"`
	Active    bool           `gorm:"default:true" json:"active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TenantTaxPaymentType concepto tributario en pagos (SPOT detracción, retenciones futuras).
type TenantTaxPaymentType struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Code      string         `gorm:"size:50;not null;uniqueIndex" json:"code"`
	Name      string         `gorm:"size:100;not null" json:"name"`
	Active    bool           `gorm:"default:true" json:"active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type TenantBankAccount struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	Name          string         `gorm:"size:255;not null" json:"name"`
	BankName      string         `gorm:"size:255" json:"bank_name"`
	AccountNumber string         `gorm:"size:100" json:"account_number"`
	Currency      string         `gorm:"size:10;default:'PEN'" json:"currency"`
	Balance       float64        `gorm:"type:decimal(15,2);default:0" json:"balance"`
	Type          string         `gorm:"size:30;default:'bank'" json:"type"`  // bank, wallet, cash
	PaymentMethod string         `gorm:"size:50;index" json:"payment_method"` // legacy: efectivo, yape, etc.; preferir tenant_payment_methods
	Active        bool           `gorm:"default:true" json:"active"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

type TenantBankMovement struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	BankAccountID uint      `gorm:"not null;index" json:"bank_account_id"`
	Type          string    `gorm:"size:20;not null" json:"type"` // debit, credit
	Amount        float64   `gorm:"type:decimal(15,2);not null" json:"amount"`
	Description   string    `gorm:"size:255" json:"description"`
	Reference     string    `gorm:"size:100" json:"reference"`
	Date          time.Time `gorm:"not null" json:"date"`
	UserID        uint      `gorm:"not null" json:"user_id"`
	CreatedAt     time.Time `json:"created_at"`
}

type TenantExternalModule struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	ModuleKey  string    `gorm:"size:100;not null;uniqueIndex" json:"module_key"`
	Name       string    `gorm:"size:255;not null" json:"name"`
	BaseURL    string    `gorm:"size:500" json:"base_url"`
	APIKey     string    `gorm:"size:255" json:"-"`
	Enabled    bool      `gorm:"default:true" json:"enabled"`
	ConfigJSON *string   `gorm:"type:json" json:"config_json"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// =================== MÓDULO RESTAURANTE ===================

// TenantRestaurantFloor representa un piso o sala del restaurante.
type TenantRestaurantFloor struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	BranchID  uint           `gorm:"not null;index;default:1" json:"branch_id"`
	Name      string         `gorm:"size:100;not null" json:"name"`
	SortOrder int            `gorm:"default:0" json:"sort_order"`
	Active    bool           `gorm:"default:true" json:"active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TenantRestaurantTable representa una mesa del restaurante.
type TenantRestaurantTable struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	BranchID  uint           `gorm:"not null;index;default:1" json:"branch_id"`
	FloorID   uint           `gorm:"not null;index" json:"floor_id"`
	Name      string         `gorm:"size:50;not null" json:"name"`
	Capacity  int            `gorm:"default:4" json:"capacity"`
	Status    string         `gorm:"size:20;default:'libre'" json:"status"` // libre, ocupada, en_consumo
	Active    bool           `gorm:"default:true" json:"active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TenantWaiter representa un mozo del restaurante.
type TenantWaiter struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	UserID    *uint          `gorm:"index" json:"user_id"` // opcional: vinculado a usuario del sistema
	Name      string         `gorm:"size:100;not null" json:"name"`
	Code      string         `gorm:"size:20;index" json:"code"` // código corto para identificación rápida
	Active    bool           `gorm:"default:true" json:"active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TenantTableSession representa un pedido restaurante (mesa, llevar, delivery o POS).
// Status legacy: open, billed, cancelled, closed. OrderStatus: ciclo de negocio del pedido.
type TenantTableSession struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	TableID     *uint      `gorm:"index" json:"table_id"` // null = sin mesa
	WaiterID    *uint      `gorm:"index" json:"waiter_id,omitempty"` // deprecado: usar staff_id
	StaffID     *uint      `gorm:"index" json:"staff_id"`
	UserID      uint       `gorm:"not null;index" json:"user_id"`
	BranchID    uint       `gorm:"not null;index" json:"branch_id"`
	Guests      int        `gorm:"default:1" json:"guests"`
	OpenedAt    time.Time  `gorm:"not null" json:"opened_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	Status      string     `gorm:"size:20;default:'open'" json:"status"` // open, billed, cancelled, closed
	OrderCode   string     `gorm:"size:32;index" json:"order_code"`
	OrderType   string     `gorm:"size:20;default:'dine_in';index" json:"order_type"`   // dine_in, takeaway, delivery, quick_sale
	OrderStatus string     `gorm:"size:30;default:'pending';index" json:"order_status"` // draft, pending, sent_to_kitchen, preparing, ready, on_the_way, delivered, paid, cancelled
	ContactID   *uint      `gorm:"index" json:"contact_id"`
	CustomerName  string   `gorm:"size:200" json:"customer_name"`
	CustomerPhone string   `gorm:"size:30" json:"customer_phone"`
	DeliveryDriverID *uint `gorm:"index" json:"delivery_driver_id"`
	DeliveryAddress  string `gorm:"type:text" json:"delivery_address"`
	DeliveryReference string `gorm:"size:255" json:"delivery_reference"`
	EstimatedMinutes  int    `gorm:"default:0" json:"estimated_minutes"`
	SentToKitchenAt *time.Time `json:"sent_to_kitchen_at"`
	ReadyAt         *time.Time `json:"ready_at"`
	PaidAt          *time.Time `json:"paid_at"`
	Notes       string     `gorm:"type:text" json:"notes"`
	SaleID      *uint      `gorm:"index" json:"sale_id"`
	TotalAmount float64    `gorm:"type:decimal(15,2);default:0" json:"total_amount"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TenantDeliveryCompany plataforma de delivery (PedidosYa, Rappi, etc.).
type TenantDeliveryCompany struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Name      string         `gorm:"size:100;not null;uniqueIndex" json:"name"`
	SortOrder int            `gorm:"default:0" json:"sort_order"`
	Active    bool           `gorm:"default:true" json:"active"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TenantDeliveryDriver repartidor para pedidos delivery.
type TenantDeliveryDriver struct {
	ID                uint           `gorm:"primaryKey" json:"id"`
	DeliveryCompanyID *uint          `gorm:"index" json:"delivery_company_id"`
	Name              string         `gorm:"size:100;not null" json:"name"`
	Phone             string         `gorm:"size:30" json:"phone"`
	VehicleType       string         `gorm:"size:50" json:"vehicle_type"`
	Plate             string         `gorm:"size:20" json:"plate"`
	Active            bool           `gorm:"default:true" json:"active"`
	Notes             string         `gorm:"type:text" json:"notes"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `gorm:"index" json:"-"`

	DeliveryCompany *TenantDeliveryCompany `gorm:"foreignKey:DeliveryCompanyID" json:"delivery_company,omitempty"`
}

// TenantTableOrder representa una ronda/comanda de cocina (ticket) dentro de la sesión.
type TenantTableOrder struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	SessionID   uint       `gorm:"not null;index" json:"session_id"`
	WaiterID    *uint      `gorm:"index" json:"waiter_id,omitempty"` // deprecado
	StaffID     *uint      `gorm:"index" json:"staff_id"`
	UserID      uint       `gorm:"not null;index" json:"user_id"`
	OrderNumber int        `gorm:"not null" json:"order_number"` // número de comanda/ronda en la sesión
	Notes       string     `gorm:"type:text" json:"notes"`
	Status      string     `gorm:"size:20;default:'active'" json:"status"` // active, cancelled
	PrintedAt   *time.Time `json:"printed_at"`
	PrintedByID *uint      `gorm:"index" json:"printed_by_id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TenantComanda representa un ítem individual dentro de un pedido.
type TenantComanda struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	OrderID       uint       `gorm:"not null;index" json:"order_id"`
	SessionID     uint       `gorm:"not null;index" json:"session_id"`
	ProductID     *uint      `gorm:"index" json:"product_id"`
	ProductCode   string     `gorm:"size:100" json:"product_code"`
	ProductName      string     `gorm:"size:255;not null" json:"product_name"`
	PreparationArea  string     `gorm:"size:50" json:"preparation_area"` // snapshot al enviar (cocina, bar, etc.)
	Quantity         float64    `gorm:"type:decimal(15,3);not null" json:"quantity"`
	UnitPrice        float64    `gorm:"type:decimal(15,2);not null" json:"unit_price"`
	Notes            string     `gorm:"size:500" json:"notes"`                     // instrucciones especiales (sin cebolla, etc.)
	ModifiersJSON        string `gorm:"type:text" json:"modifiers_json"` // variantes y extras [{ option_id, option_name, extra_price, type, ... }]
	IgvAffectationType   string `gorm:"size:10;default:'10'" json:"igv_affectation_type"`
	PriceIncludesIgv     bool   `gorm:"default:true" json:"price_includes_igv"`
	Status               string `gorm:"size:20;default:'pendiente'" json:"status"` // pendiente, preparacion, lista, entregada
	Printed          bool       `gorm:"default:false" json:"printed"`
	PrintedAt        *time.Time `json:"printed_at"`
	PrintedByID      *uint      `gorm:"index" json:"printed_by_id"`
	CancelledAt   *time.Time `json:"cancelled_at"`
	CancelledByID *uint      `gorm:"index" json:"cancelled_by_id"`
	CancelReason  string     `gorm:"size:255" json:"cancel_reason"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// TenantRestaurantSetting configuración del módulo restaurante (una fila por tenant).
type TenantRestaurantSetting struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	DeletionPin      string    `gorm:"size:72" json:"-"` // hash bcrypt del PIN de anulación (no se expone en JSON)
	StaffV2Enabled   bool      `gorm:"default:false" json:"staff_v2_enabled"`
	PermCacheVersion uint      `gorm:"default:0" json:"perm_cache_version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// TenantRestaurantStaff perfil operativo restaurante (1:1 con tenant_users).
type TenantRestaurantStaff struct {
	ID             uint           `gorm:"primaryKey" json:"id"`
	UserID         uint           `gorm:"uniqueIndex;not null" json:"user_id"`
	EmployeeType   string         `gorm:"size:30;not null;index" json:"employee_type"`
	StaffCode      string         `gorm:"size:20;index" json:"staff_code"`
	PinHash        string         `gorm:"size:72" json:"-"`
	DisplayName    string         `gorm:"size:100" json:"display_name"`
	IsActive       bool           `gorm:"default:true" json:"is_active"`
	CanCharge      bool           `gorm:"default:false" json:"can_charge"`
	CanDiscount    bool           `gorm:"default:false" json:"can_discount"`
	CanOpenTable   bool           `gorm:"default:true" json:"can_open_table"`
	KitchenAccess  bool           `gorm:"default:false" json:"kitchen_access"`
	DeliveryAccess bool           `gorm:"default:false" json:"delivery_access"`
	LegacyWaiterID *uint     `gorm:"index" json:"legacy_waiter_id,omitempty"`
	Notes          string    `gorm:"type:text" json:"notes,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (TenantRestaurantStaff) TableName() string { return "tenant_restaurant_staff" }

// TenantUserRestaurantRole rol operativo del usuario dentro del módulo restaurante.
// Independiente de TenantRole; solo aplica en el frontend de restaurante.
// Valores: admin, vendedor, mozo, cocinero
type TenantUserRestaurantRole struct {
	UserID    uint      `gorm:"primaryKey" json:"user_id"`
	Role      string    `gorm:"size:30;not null" json:"role"` // admin, vendedor, mozo, cocinero
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TenantSalePayment registra pagos individuales (pagos mixtos) asociados a una venta.
type TenantSalePayment struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SaleID    uint      `gorm:"not null;index" json:"sale_id"`
	Method    string    `gorm:"size:50;not null" json:"method"` // efectivo, tarjeta, transferencia, yape, plin, credito
	Amount    float64   `gorm:"type:decimal(15,2);not null" json:"amount"`
	Reference string    `gorm:"size:100" json:"reference"` // nro. de operación, voucher, etc.
	Notes     string    `gorm:"size:255" json:"notes"`
	CreatedAt time.Time `json:"created_at"`
}

// TenantMembership — cuota recurrente entre el tenant y un cliente (gimnasio, colegio, etc.).
// Opcionalmente enlazada a un producto/servicio del catálogo para reutilizar precio y SUNAT al facturar.
type TenantMembership struct {
	ID                  uint           `gorm:"primaryKey" json:"id"`
	ContactID           uint           `gorm:"not null;index" json:"contact_id"`
	ProductID           *uint          `gorm:"index" json:"product_id"`
	BranchID            uint           `gorm:"not null;index" json:"branch_id"`
	Title               string         `gorm:"size:255" json:"title"`
	BillingCycle        string         `gorm:"size:20;not null;default:'monthly'" json:"billing_cycle"` // weekly, biweekly, monthly, quarterly, yearly, custom
	BillingIntervalDays int            `gorm:"not null;default:0" json:"billing_interval_days"`       // si billing_cycle = custom
	Amount              float64        `gorm:"type:decimal(15,2);not null" json:"amount"`
	Currency            string         `gorm:"size:10;default:'PEN'" json:"currency"`
	StartDate           time.Time      `gorm:"not null" json:"start_date"`
	EndDate             *time.Time     `json:"end_date"`
	NextBillingDate     time.Time      `gorm:"not null" json:"next_billing_date"`
	LastBilledAt        *time.Time     `json:"last_billed_at"`
	Status              string         `gorm:"size:20;not null;default:'active';index" json:"status"` // active, paused, cancelled, expired
	Notes               string         `gorm:"type:text" json:"notes"`
	IgvAffectationType  string         `gorm:"size:10;default:'10'" json:"igv_affectation_type"`
	PriceIncludesIgv    bool           `gorm:"default:false" json:"price_includes_igv"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`

	ContactName  string `gorm:"-" json:"contact_name,omitempty"`
	ContactPhone string `gorm:"-" json:"contact_phone,omitempty"`
	ProductName  string `gorm:"-" json:"product_name,omitempty"`
}

// TenantMembershipInvoice vincula un cobro de período con la venta generada (para SUNAT / historial).
type TenantMembershipInvoice struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	MembershipID uint      `gorm:"not null;index" json:"membership_id"`
	SaleID       uint      `gorm:"not null;index" json:"sale_id"`
	PeriodStart  time.Time `gorm:"not null" json:"period_start"`
	PeriodEnd    time.Time `gorm:"not null" json:"period_end"`
	CreatedAt    time.Time `json:"created_at"`
}

// MigrateTenant está deprecado: el esquema se aplica solo vía tenantmigrations (V001+).
func MigrateTenant(db *gorm.DB) error {
	return fmt.Errorf("MigrateTenant deprecado: use migrate-fleet o ProvisionTenantDB (migraciones versionadas)")
}

// ensureServiceProductsNoStock corrige filas type=service que quedaron con manage_stock por datos legacy o bugs.
func ensureServiceProductsNoStock(db *gorm.DB) error {
	return db.Exec(`
		UPDATE tenant_products SET
			manage_stock = 0,
			manage_series = 0,
			has_variants = 0,
			has_modifiers = 0,
			is_restaurant = 0,
			min_stock = 0
		WHERE LOWER(TRIM(COALESCE(type, ''))) = 'service'
	`).Error
}

// EnsureMembershipModulePermissions agrega permisos del módulo memberships en tenants ya existentes.
// Si la tabla de permisos está vacía (BD recién creada antes de SeedPermissions), no hace nada.
func EnsureMembershipModulePermissions(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&TenantPermission{}) || !mig.HasTable(&TenantRole{}) {
		return nil
	}
	var totalPerms int64
	if err := db.Model(&TenantPermission{}).Count(&totalPerms).Error; err != nil {
		return err
	}
	if totalPerms == 0 {
		return nil
	}
	defs := []TenantPermission{
		{Module: "memberships", Action: "view", Label: "Ver membresías"},
		{Module: "memberships", Action: "create", Label: "Crear membresías"},
		{Module: "memberships", Action: "edit", Label: "Editar membresías"},
		{Module: "memberships", Action: "delete", Label: "Eliminar membresías"},
		{Module: "memberships", Action: "generate_sale", Label: "Generar venta desde membresía"},
	}
	for i := range defs {
		var n int64
		db.Model(&TenantPermission{}).Where("module = ? AND action = ?", defs[i].Module, defs[i].Action).Count(&n)
		if n > 0 {
			continue
		}
		if err := db.Create(&defs[i]).Error; err != nil {
			return err
		}
	}
	var admin TenantRole
	if err := db.Where("name = ?", "Administrador").First(&admin).Error; err != nil {
		return nil
	}
	var perms []TenantPermission
	db.Where("module = ?", "memberships").Find(&perms)
	for _, p := range perms {
		var cnt int64
		db.Model(&TenantRolePermission{}).Where("role_id = ? AND permission_id = ?", admin.ID, p.ID).Count(&cnt)
		if cnt > 0 {
			continue
		}
		if err := db.Create(&TenantRolePermission{RoleID: admin.ID, PermissionID: p.ID}).Error; err != nil {
			return err
		}
	}
	return nil
}

// ensureDocumentSeriesColumns agrega sunat_code y category a tenant_document_series si no existen.
func ensureDocumentSeriesColumns(db *gorm.DB) error {
	mig := db.Migrator()
	model := &TenantDocumentSeries{}
	if !mig.HasColumn(model, "SunatCode") {
		if err := mig.AddColumn(model, "SunatCode"); err != nil {
			return err
		}
	}
	if !mig.HasColumn(model, "Category") {
		if err := mig.AddColumn(model, "Category"); err != nil {
			return err
		}
	}
	return nil
}

// SeedTenant inserta datos iniciales en la BD de un tenant (delega a ProvisionTenantSeed).
func SeedTenant(db *gorm.DB, adminEmail, adminPassword, companyName, ruc, address, ubigeo string) error {
	if err := ProvisionTenantSeed(db, TenantSeedInput{
		AdminEmail: adminEmail, AdminPassword: adminPassword,
		CompanyName: companyName, RUC: ruc, Address: address, Ubigeo: ubigeo,
	}); err != nil {
		return err
	}
	if err := SeedUbigeoRegionesProvincias(db); err != nil {
		return err
	}
	_ = SeedUbigeoDistritos(db, UbigeoDistritosCSVPath())
	return nil
}

// SeedPaymentMethodsIfEmpty y SeedPaymentMethodsCatalog están en payment_method_seed.go.
