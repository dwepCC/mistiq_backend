package database

import "time"

// Proveedores de transporte con API (hoy solo Shalom). Todo vive en la BD central.

const ProviderShalom = "shalom"

// EquipProviderSettings configuración de la integración con un proveedor. La API key se guarda CIFRADA
// (pkg/secrets) y nunca se devuelve por la API: la pantalla solo muestra los últimos 4 caracteres.
type EquipProviderSettings struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Provider string `gorm:"size:30;not null;uniqueIndex" json:"provider"`
	BaseURL  string `gorm:"size:200" json:"base_url"`
	Enabled  bool   `gorm:"default:false" json:"enabled"`

	APIKeyEnc  string `gorm:"type:text" json:"-"`
	APIKeyHint string `gorm:"size:8" json:"api_key_hint"`

	// Cuenta de Shalom Pro (solo para cotizar tarifas): el correo se muestra, la clave va CIFRADA y no se devuelve.
	ProEmail       string `gorm:"size:150" json:"pro_email"`
	ProPasswordEnc string `gorm:"type:text" json:"-"`

	// Webhook de seguimiento: la URL pública que registramos en el proveedor y el secreto de firma (cifrado).
	WebhookURL       string `gorm:"size:255" json:"webhook_url"`
	WebhookSecretEnc string `gorm:"type:text" json:"-"`
	WebhookVerified  bool   `gorm:"default:false" json:"webhook_verified"`

	LastAgenciesSyncAt    *time.Time `json:"last_agencies_sync_at"`
	LastAgenciesSyncCount int        `json:"last_agencies_sync_count"`
	LastLocationsSyncAt   *time.Time `json:"last_locations_sync_at"`

	UpdatedAt time.Time `json:"updated_at"`
}

func (EquipProviderSettings) TableName() string { return "equip_provider_settings" }

// Tipos de ubicación del proveedor.
const (
	ProviderLocDepartment = "department"
	ProviderLocProvince   = "province"
	ProviderLocDistrict   = "district"
)

// EquipProviderLocation departamentos, provincias y distritos tal como los publica el proveedor (sus ids no
// son ubigeos; solo el distrito trae ubigeo INEI). Se sincronizan bajo demanda y el panel lee SIEMPRE de aquí.
type EquipProviderLocation struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Provider   string    `gorm:"size:30;not null;uniqueIndex:ux_equip_prov_loc,priority:1" json:"provider"`
	Kind       string    `gorm:"size:12;not null;uniqueIndex:ux_equip_prov_loc,priority:2" json:"kind"`
	DepID      int       `gorm:"not null;default:0;uniqueIndex:ux_equip_prov_loc,priority:3;index" json:"dep_id"`
	ProvID     int       `gorm:"not null;default:0;uniqueIndex:ux_equip_prov_loc,priority:4;index" json:"prov_id"`
	ExternalID int       `gorm:"not null;uniqueIndex:ux_equip_prov_loc,priority:5" json:"external_id"`
	Name       string    `gorm:"size:120;not null" json:"name"`
	Ubigeo     string    `gorm:"size:6" json:"ubigeo"`
	SyncedAt   time.Time `json:"synced_at"`
}

func (EquipProviderLocation) TableName() string { return "equip_provider_locations" }

// EquipProviderEvent evento de webhook recibido. EventID único → entrega idempotente (el proveedor entrega
// «al menos una vez»), y queda como bitácora de lo que informó cada transportista.
type EquipProviderEvent struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	Provider    string     `gorm:"size:30;not null;uniqueIndex:ux_equip_prov_event,priority:1" json:"provider"`
	EventID     string     `gorm:"size:120;not null;uniqueIndex:ux_equip_prov_event,priority:2" json:"event_id"`
	EventType   string     `gorm:"size:40" json:"event_type"`
	Reference   string     `gorm:"size:40;index" json:"reference"` // N° de guía (numero)
	Payload     string     `gorm:"type:text" json:"payload"`
	ProcessedAt *time.Time `json:"processed_at"`
	Note        string     `gorm:"size:255" json:"note"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (EquipProviderEvent) TableName() string { return "equip_provider_events" }

// EquipNotification aviso de la campana del panel central (llegada a agencia, entrega, demora…). DedupKey único
// evita avisos repetidos por entregas duplicadas del webhook.
type EquipNotification struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	Kind        string     `gorm:"size:30;index" json:"kind"`
	Severity    string     `gorm:"size:10" json:"severity"` // info | success | warning
	Title       string     `gorm:"size:150" json:"title"`
	Message     string     `gorm:"size:255" json:"message"`
	OrderID     uint       `gorm:"index" json:"order_id"`
	OrderNumber int        `json:"order_number"`
	DedupKey    string     `gorm:"size:120;uniqueIndex" json:"-"`
	ReadAt      *time.Time `gorm:"index" json:"read_at"`
	CreatedAt   time.Time  `gorm:"index" json:"created_at"`
}

func (EquipNotification) TableName() string { return "equip_notifications" }
