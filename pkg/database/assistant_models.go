package database

import (
	"errors"
	"sync"
	"time"
)

// Assistant es la fila de configuración del agente comercial de la plataforma.
// v1: un único asistente activo (owner_kind="platform"). owner_kind/owner_tenant_id
// quedan reservados para un futuro agente por tenant, sin cablear todavía
// (ver docs/CHATBOT-AGENT-ARCHITECTURE.md §5).
type Assistant struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	Name          string `gorm:"size:120;not null" json:"name"`
	Slug          string `gorm:"size:120;uniqueIndex" json:"slug"`
	OwnerKind     string `gorm:"size:20;not null;default:'platform';index" json:"owner_kind"`
	OwnerTenantID uint   `gorm:"not null;default:0;index" json:"owner_tenant_id"`
	Status        string `gorm:"size:20;not null;default:'active';index" json:"status"`
	DefaultLocale string `gorm:"size:10;not null;default:'es'" json:"default_locale"`

	LLMProvider   string `gorm:"size:40;not null;default:'openai'" json:"llm_provider"`
	LLMModel      string `gorm:"size:80" json:"llm_model"`
	LLMBaseURL    string `gorm:"size:255" json:"llm_base_url"`
	LLMAPIKey     string `gorm:"size:1024" json:"-"`
	LLMParamsJSON string `gorm:"type:text" json:"-"`

	EmbedProvider string `gorm:"size:40" json:"embed_provider"`
	EmbedModel    string `gorm:"size:80" json:"embed_model"`
	EmbedBaseURL  string `gorm:"size:255" json:"embed_base_url"`
	EmbedAPIKey   string `gorm:"size:1024" json:"-"`

	SystemPromptOverride string `gorm:"type:text" json:"system_prompt_override"`
	PersonalityOverride  string `gorm:"type:text" json:"personality_override"`

	WhatsAppNumber        string `gorm:"size:30;index" json:"whatsapp_number"`
	WhatsAppPhoneNumberID string `gorm:"size:60" json:"whatsapp_phone_number_id"`
	WhatsAppAccessToken   string `gorm:"type:text" json:"-"`
	WhatsAppAppSecret     string `gorm:"size:255" json:"-"`
	WhatsAppVerifyToken   string `gorm:"size:255" json:"-"`
	WhatsAppTemplatesJSON string `gorm:"type:text" json:"whatsapp_templates_json"`

	EnableTrialTenant  bool `gorm:"not null;default:false" json:"enable_trial_tenant"`
	EnablePaidContract bool `gorm:"not null;default:false" json:"enable_paid_contract"`

	AgentType     string `gorm:"size:40;not null;default:'commercial';index" json:"agent_type"`
	EnabledTools  string `gorm:"type:text" json:"enabled_tools"`
	MaxToolRounds int    `gorm:"not null;default:0" json:"max_tool_rounds"`

	Active bool `gorm:"not null;default:true;index" json:"active"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Assistant) TableName() string { return "assistants" }

// Estados válidos de AssistantConversation.Status.
const (
	ConvStatusBot        = "bot"
	ConvStatusNeedsHuman = "needs_human"
	ConvStatusHuman      = "human"
	ConvStatusClosed     = "closed"
)

// AssistantConversation es un hilo de chat con un contacto (WhatsApp/web).
type AssistantConversation struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	AssistantID uint   `gorm:"not null;index:idx_ac_assistant_status,priority:1" json:"assistant_id"`
	Channel     string `gorm:"size:30;index" json:"channel"`
	ContactRef  string `gorm:"size:120;index" json:"contact_ref"`
	Status      string `gorm:"size:20;not null;default:'bot';index:idx_ac_assistant_status,priority:2" json:"status"`
	Locale      string `gorm:"size:10" json:"locale"`

	NeedsHumanReason string `gorm:"size:60" json:"needs_human_reason"`

	AssignedSAUserID uint      `gorm:"not null;default:0;index" json:"assigned_sa_user_id"`
	LastMsgAt        time.Time `gorm:"index" json:"last_msg_at"`

	OutreachTenantID uint   `gorm:"not null;default:0;index" json:"outreach_tenant_id"`
	OutreachGoal     string `gorm:"type:text" json:"outreach_goal"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (AssistantConversation) TableName() string { return "assistant_conversations" }

// Roles válidos de AssistantMessage.Role.
const (
	MsgRoleUser      = "user"
	MsgRoleAssistant = "assistant"
	MsgRoleTool      = "tool"
	MsgRoleAgent     = "agent"
)

// AssistantMessage es un turno dentro de una conversación.
type AssistantMessage struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	ConversationID uint   `gorm:"not null;index:idx_am_conv,priority:1" json:"conversation_id"`
	Role           string `gorm:"size:20;not null" json:"role"`
	Content        string `gorm:"type:text" json:"content"`

	ToolName   string `gorm:"size:80" json:"tool_name,omitempty"`
	ToolCallID string `gorm:"size:80" json:"tool_call_id,omitempty"`

	ChannelMsgID string `gorm:"size:120" json:"channel_msg_id,omitempty"`
	ReplyToID    uint   `gorm:"not null;default:0" json:"reply_to_id,omitempty"`

	PromptTokens     int `gorm:"not null;default:0" json:"prompt_tokens"`
	CompletionTokens int `gorm:"not null;default:0" json:"completion_tokens"`
	TotalTokens      int `gorm:"not null;default:0" json:"total_tokens"`

	CreatedAt time.Time `gorm:"index:idx_am_conv,priority:2" json:"created_at"`
}

func (AssistantMessage) TableName() string { return "assistant_messages" }

// AssistantKnowledgeChunk es un fragmento del corpus RAG (embedding en JSON de texto).
type AssistantKnowledgeChunk struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	AssistantID uint   `gorm:"not null;index:idx_akc_scope,priority:1" json:"assistant_id"`
	KbID        uint   `gorm:"not null;index:idx_akc_scope,priority:2" json:"kb_id"`
	SourceType  string `gorm:"size:30;index" json:"source_type"`
	Title       string `gorm:"size:255" json:"title"`
	Content     string `gorm:"type:text" json:"content"`

	Embedding      string `gorm:"type:longtext" json:"-"`
	EmbeddingModel string `gorm:"size:80" json:"embedding_model"`
	TokenCount     int    `gorm:"not null;default:0" json:"token_count"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (AssistantKnowledgeChunk) TableName() string { return "assistant_knowledge_chunks" }

// AssistantLead es un prospecto/oportunidad generado por el agente (Fase 5 en adelante).
type AssistantLead struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	AssistantID    uint   `gorm:"not null;index" json:"assistant_id"`
	ConversationID uint   `gorm:"index" json:"conversation_id"`
	TenantID       *uint  `gorm:"index" json:"tenant_id,omitempty"`
	Name           string `gorm:"size:150" json:"name"`
	Phone          string `gorm:"size:30;index" json:"phone"`
	Email          string `gorm:"size:150" json:"email"`
	Interest       string `gorm:"type:text" json:"interest"`
	Qualification  string `gorm:"size:40" json:"qualification"`
	Kind           string `gorm:"size:30;not null;default:'lead';index" json:"kind"`
	Status         string `gorm:"size:30;not null;default:'new';index" json:"status"`

	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	Notes       string     `gorm:"type:text" json:"notes"`

	PaymentValidatedAt         *time.Time `json:"payment_validated_at,omitempty"`
	PaymentValidatedBySAUserID *uint      `json:"payment_validated_by_sa_user_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (AssistantLead) TableName() string { return "assistant_leads" }

// AssistantPushSubscription es una suscripción Web Push de un asesor del panel (Fase 3+).
type AssistantPushSubscription struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SAUserID  uint      `gorm:"index" json:"sa_user_id"`
	Endpoint  string    `gorm:"size:512;uniqueIndex" json:"-"`
	P256dh    string    `gorm:"size:255" json:"-"`
	Auth      string    `gorm:"size:255" json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

func (AssistantPushSubscription) TableName() string { return "assistant_push_subscriptions" }

var (
	ensureAssistantSchemaOnce sync.Once
	ensureAssistantSchemaErr  error
)

// EnsureAssistantSchema aplica las tablas del módulo agente comercial en BD central
// (idempotente, AutoMigrate — mismo patrón que EnsureCentralFleetSchema). Se invoca
// al arrancar el API.
func EnsureAssistantSchema() error {
	ensureAssistantSchemaOnce.Do(func() {
		if CentralDB == nil {
			ensureAssistantSchemaErr = errors.New("BD central no conectada")
			return
		}
		ensureAssistantSchemaErr = CentralDB.AutoMigrate(
			&Assistant{},
			&AssistantConversation{},
			&AssistantMessage{},
			&AssistantKnowledgeChunk{},
			&AssistantLead{},
			&AssistantPushSubscription{},
		)
	})
	return ensureAssistantSchemaErr
}
