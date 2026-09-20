package agent

import (
	"strings"

	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

// assistantConfigDTO es lo que devuelve GET /assistant/config — los
// secretos NUNCA se devuelven, solo flags has_* (mismo patrón que el
// original: el formulario del panel se inicializa vacío en esos campos y
// solo los reenvía si el usuario escribió un valor nuevo).
type assistantConfigDTO struct {
	Name          string `json:"name"`
	DefaultLocale string `json:"default_locale"`
	Active        bool   `json:"active"`

	LLMProvider  string `json:"llm_provider"`
	LLMModel     string `json:"llm_model"`
	LLMBaseURL   string `json:"llm_base_url"`
	HasLLMAPIKey bool   `json:"has_llm_api_key"`

	EmbedModel     string `json:"embed_model"`
	EmbedBaseURL   string `json:"embed_base_url"`
	HasEmbedAPIKey bool   `json:"has_embed_api_key"`

	WhatsAppNumber         string `json:"whatsapp_number"`
	WhatsAppPhoneNumberID  string `json:"whatsapp_phone_number_id"`
	HasWhatsAppAccessToken bool   `json:"has_whatsapp_access_token"`
	HasWhatsAppAppSecret   bool   `json:"has_whatsapp_app_secret"`
	HasWhatsAppVerifyToken bool   `json:"has_whatsapp_verify_token"`

	PaymentPlinNumber   string `json:"payment_plin_number"`
	PaymentHolderName   string `json:"payment_holder_name"`
	PaymentReceiptPhone string `json:"payment_receipt_phone"`

	EnableTrialTenant  bool `json:"enable_trial_tenant"`
	EnablePaidContract bool `json:"enable_paid_contract"`

	MaxToolRounds        int    `json:"max_tool_rounds"`
	SystemPromptOverride string `json:"system_prompt_override"`
	PersonalityOverride  string `json:"personality_override"`
}

func toConfigDTO(a database.Assistant) assistantConfigDTO {
	return assistantConfigDTO{
		Name: a.Name, DefaultLocale: a.DefaultLocale, Active: a.Active,
		LLMProvider: a.LLMProvider, LLMModel: a.LLMModel, LLMBaseURL: a.LLMBaseURL,
		HasLLMAPIKey: a.LLMAPIKey != "",
		EmbedModel:   a.EmbedModel, EmbedBaseURL: a.EmbedBaseURL, HasEmbedAPIKey: a.EmbedAPIKey != "",
		WhatsAppNumber: a.WhatsAppNumber, WhatsAppPhoneNumberID: a.WhatsAppPhoneNumberID,
		HasWhatsAppAccessToken: a.WhatsAppAccessToken != "",
		HasWhatsAppAppSecret:   a.WhatsAppAppSecret != "",
		HasWhatsAppVerifyToken: a.WhatsAppVerifyToken != "",
		PaymentPlinNumber:      a.PaymentPlinNumber, PaymentHolderName: a.PaymentHolderName, PaymentReceiptPhone: a.PaymentReceiptPhone,
		EnableTrialTenant: a.EnableTrialTenant, EnablePaidContract: a.EnablePaidContract,
		MaxToolRounds: a.MaxToolRounds, SystemPromptOverride: a.SystemPromptOverride, PersonalityOverride: a.PersonalityOverride,
	}
}

// handleConfigGet: GET /assistant/config
func handleConfigGet(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	var row database.Assistant
	if err := database.CentralDB.First(&row, eng.platformAssistantID).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo leer la configuración"})
	}
	return c.JSON(fiber.Map{"data": toConfigDTO(row)})
}

// updateAssistantConfigInput: los campos *_key/*_token son punteros — nil
// = "no tocar", string vacío no llega nunca desde un formulario que use
// este mismo patrón (el frontend solo manda el campo si el usuario
// escribió algo).
type updateAssistantConfigInput struct {
	Name          *string `json:"name"`
	DefaultLocale *string `json:"default_locale"`
	Active        *bool   `json:"active"`

	LLMProvider *string `json:"llm_provider"`
	LLMModel    *string `json:"llm_model"`
	LLMBaseURL  *string `json:"llm_base_url"`
	LLMAPIKey   *string `json:"llm_api_key"`

	EmbedModel   *string `json:"embed_model"`
	EmbedBaseURL *string `json:"embed_base_url"`
	EmbedAPIKey  *string `json:"embed_api_key"`

	WhatsAppNumber        *string `json:"whatsapp_number"`
	WhatsAppPhoneNumberID *string `json:"whatsapp_phone_number_id"`
	WhatsAppAccessToken   *string `json:"whatsapp_access_token"`
	WhatsAppAppSecret     *string `json:"whatsapp_app_secret"`
	WhatsAppVerifyToken   *string `json:"whatsapp_verify_token"`

	PaymentPlinNumber   *string `json:"payment_plin_number"`
	PaymentHolderName   *string `json:"payment_holder_name"`
	PaymentReceiptPhone *string `json:"payment_receipt_phone"`

	EnableTrialTenant  *bool `json:"enable_trial_tenant"`
	EnablePaidContract *bool `json:"enable_paid_contract"`

	MaxToolRounds        *int    `json:"max_tool_rounds"`
	SystemPromptOverride *string `json:"system_prompt_override"`
	PersonalityOverride  *string `json:"personality_override"`
}

// handleConfigUpdate: PUT /assistant/config — actualiza solo los campos
// presentes en el body; recarga el resolver (invalida caché) y el cliente
// WhatsApp en caliente, sin reiniciar el proceso.
func handleConfigUpdate(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	var in updateAssistantConfigInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	updates := map[string]any{}
	setStr := func(field string, v *string) {
		if v != nil {
			updates[field] = strings.TrimSpace(*v)
		}
	}
	setStrKeepSecret := func(field string, v *string) {
		// secreto: solo se toca si viene NO VACÍO (vacío = "no lo cambies",
		// nunca "bórralo" — evitar que un guardado accidental limpie una
		// credencial ya configurada).
		if v != nil && strings.TrimSpace(*v) != "" {
			updates[field] = strings.TrimSpace(*v)
		}
	}

	setStr("name", in.Name)
	setStr("default_locale", in.DefaultLocale)
	if in.Active != nil {
		updates["active"] = *in.Active
	}
	setStr("llm_provider", in.LLMProvider)
	setStr("llm_model", in.LLMModel)
	setStr("llm_base_url", in.LLMBaseURL)
	setStrKeepSecret("llm_api_key", in.LLMAPIKey)
	setStr("embed_model", in.EmbedModel)
	setStr("embed_base_url", in.EmbedBaseURL)
	setStrKeepSecret("embed_api_key", in.EmbedAPIKey)
	setStr("whatsapp_number", in.WhatsAppNumber)
	setStr("whatsapp_phone_number_id", in.WhatsAppPhoneNumberID)
	setStrKeepSecret("whatsapp_access_token", in.WhatsAppAccessToken)
	setStrKeepSecret("whatsapp_app_secret", in.WhatsAppAppSecret)
	setStrKeepSecret("whatsapp_verify_token", in.WhatsAppVerifyToken)
	setStr("payment_plin_number", in.PaymentPlinNumber)
	setStr("payment_holder_name", in.PaymentHolderName)
	setStr("payment_receipt_phone", in.PaymentReceiptPhone)
	if in.EnableTrialTenant != nil {
		updates["enable_trial_tenant"] = *in.EnableTrialTenant
	}
	if in.EnablePaidContract != nil {
		updates["enable_paid_contract"] = *in.EnablePaidContract
	}
	if in.MaxToolRounds != nil {
		updates["max_tool_rounds"] = *in.MaxToolRounds
	}
	setStr("system_prompt_override", in.SystemPromptOverride)
	setStr("personality_override", in.PersonalityOverride)

	if len(updates) > 0 {
		if err := database.CentralDB.Model(&database.Assistant{}).
			Where("id = ?", eng.platformAssistantID).Updates(updates).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo guardar la configuración"})
		}
	}

	eng.resolver.Invalidate(eng.platformAssistantID)
	reloadWhatsApp(c.Context())

	var row database.Assistant
	database.CentralDB.First(&row, eng.platformAssistantID)
	return c.JSON(fiber.Map{"data": toConfigDTO(row)})
}
