// Package whatsapp implementa el canal WhatsApp contra la Cloud API oficial
// de Meta (Graph API) — no Twilio/360dialog. Cubre: verificación de webhook,
// firma HMAC del payload entrante, parseo a agent.Inbound, y envío de
// respuestas (texto / plantilla aprobada / lista interactiva).
package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	agentpkg "tukifac/pkg/agent"
)

const (
	defaultGraphURL   = "https://graph.facebook.com"
	defaultAPIVersion = "v21.0"

	// bsuidPrefix marca contactos que ocultan su número ("WhatsApp
	// Usernames"): el webhook manda un identificador alternativo en vez
	// del teléfono. Se usa como ContactRef con este prefijo en TODO el
	// pipeline (memoria, lock de cola) para no colisionar con un contacto
	// que sí comparte teléfono.
	bsuidPrefix = "bsuid:"
)

type Client struct {
	PhoneNumberID    string
	AccessToken      string
	AppSecret        string
	VerifyTokenValue string
	GraphURL         string
	APIVersion       string

	http *http.Client
}

func New(phoneNumberID, accessToken, appSecret, verifyToken, graphURL, apiVersion string) *Client {
	if graphURL == "" {
		graphURL = defaultGraphURL
	}
	if apiVersion == "" {
		apiVersion = defaultAPIVersion
	}
	return &Client{
		PhoneNumberID:    phoneNumberID,
		AccessToken:      accessToken,
		AppSecret:        appSecret,
		VerifyTokenValue: verifyToken,
		GraphURL:         graphURL,
		APIVersion:       apiVersion,
		http:             &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) VerifyToken() string { return c.VerifyTokenValue }
func (c *Client) HasAppSecret() bool  { return c.AppSecret != "" }

// VerifySignature valida el header X-Hub-Signature-256 (HMAC-SHA256 del
// body con AppSecret). CONDICIONAL: si HasAppSecret() es false, no hay
// nada que verificar — el llamador decide si eso es aceptable (en
// desarrollo) o debe rechazar (producción sin AppSecret configurado es un
// error de configuración, no algo que este método deba decidir).
func (c *Client) VerifySignature(body []byte, header string) bool {
	if !c.HasAppSecret() {
		return true
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	expectedHex := strings.TrimPrefix(header, prefix)
	mac := hmac.New(sha256.New, []byte(c.AppSecret))
	mac.Write(body)
	computed := mac.Sum(nil)
	computedHex := hex.EncodeToString(computed)
	return hmac.Equal([]byte(computedHex), []byte(expectedHex))
}

// --- Payload entrante (Meta Graph webhook) ---

type webhookPayload struct {
	Entry []struct {
		Changes []struct {
			Value struct {
				Metadata struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata"`
				Messages []inboundMessage `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type inboundMessage struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	FromUserID string `json:"from_user_id"` // BSUID: presente cuando el contacto oculta su número
	Timestamp  string `json:"timestamp"`
	Type       string `json:"type"`
	Text       *struct {
		Body string `json:"body"`
	} `json:"text"`
	Interactive *struct {
		Type        string `json:"type"`
		ButtonReply *struct {
			ID string `json:"id"`
		} `json:"button_reply"`
		ListReply *struct {
			ID string `json:"id"`
		} `json:"list_reply"`
	} `json:"interactive"`
}

// Parse normaliza el payload de Meta a []agent.Inbound. Tipos no
// soportados en v1 (imagen, audio, documento, ubicación, ...) se ignoran
// — no hay manejo de medios entrantes.
func (c *Client) Parse(assistantID uint, body []byte) ([]agentpkg.Inbound, error) {
	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("whatsapp: parsear payload: %w", err)
	}

	var out []agentpkg.Inbound
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, m := range change.Value.Messages {
				text, ok := extractText(m)
				if !ok {
					continue // tipo no soportado (imagen/audio/...): se ignora en v1
				}
				from := m.From
				if strings.TrimSpace(m.FromUserID) != "" {
					from = bsuidPrefix + m.FromUserID
				}
				out = append(out, agentpkg.Inbound{
					AssistantID:      assistantID,
					Channel:          "whatsapp",
					ChannelAccountID: change.Value.Metadata.PhoneNumberID,
					ChannelMsgID:     m.ID,
					From:             from,
					Text:             text,
					ReceivedAt:       time.Now(),
				})
			}
		}
	}
	return out, nil
}

func extractText(m inboundMessage) (string, bool) {
	switch m.Type {
	case "text":
		if m.Text == nil {
			return "", false
		}
		return m.Text.Body, true
	case "interactive":
		if m.Interactive == nil {
			return "", false
		}
		switch m.Interactive.Type {
		case "button_reply":
			if m.Interactive.ButtonReply != nil {
				return m.Interactive.ButtonReply.ID, true
			}
		case "list_reply":
			if m.Interactive.ListReply != nil {
				return m.Interactive.ListReply.ID, true
			}
		}
		return "", false
	default:
		return "", false // imagen/audio/documento/ubicación/...: ignorado en v1
	}
}

// IsBSUID indica si un ContactRef corresponde a un contacto que oculta su
// número (WhatsApp Usernames) — el envío usa un campo distinto de la API.
func IsBSUID(contactRef string) bool { return strings.HasPrefix(contactRef, bsuidPrefix) }

func stripBSUID(contactRef string) string { return strings.TrimPrefix(contactRef, bsuidPrefix) }

// --- Envío ---

type wireRecipientMsg struct {
	MessagingProduct string           `json:"messaging_product"`
	RecipientType    string           `json:"recipient_type,omitempty"`
	To               string           `json:"to,omitempty"`
	Recipient        string           `json:"recipient,omitempty"` // usado en vez de "to" para contactos BSUID
	Type             string           `json:"type"`
	Context          *wireContext     `json:"context,omitempty"`
	Text             *wireText        `json:"text,omitempty"`
	Template         *wireTemplate    `json:"template,omitempty"`
	Interactive      *wireInteractive `json:"interactive,omitempty"`
}

type wireContext struct {
	MessageID string `json:"message_id"`
}

type wireText struct {
	Body string `json:"body"`
}

type wireTemplate struct {
	Name       string        `json:"name"`
	Language   wireLang      `json:"language"`
	Components []wireTplComp `json:"components,omitempty"`
}

type wireLang struct {
	Code string `json:"code"`
}

type wireTplComp struct {
	Type       string         `json:"type"`
	Parameters []wireTplParam `json:"parameters"`
}

type wireTplParam struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type wireInteractive struct {
	Type   string                `json:"type"`
	Header *wireInteractiveText  `json:"header,omitempty"`
	Body   wireInteractiveText   `json:"body"`
	Action wireInteractiveAction `json:"action"`
}

type wireInteractiveText struct {
	Text string `json:"text"`
}

type wireInteractiveAction struct {
	Button   string            `json:"button"`
	Sections []wireListSection `json:"sections"`
}

type wireListSection struct {
	Title string        `json:"title,omitempty"`
	Rows  []wireListRow `json:"rows"`
}

type wireListRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// Límites reales de la lista interactiva de WhatsApp — se aplican aquí en
// vez de confiar en que el llamador los respete.
const (
	maxButtonChars = 20
	maxRowTitle    = 24
	maxRowDesc     = 72
	maxRows        = 10
)

func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// Send envía una respuesta por WhatsApp: texto plano, plantilla aprobada
// (única forma de escribir fuera de la ventana de 24h), o lista interactiva
// (Outbound.Menu). Devuelve el wamid del mensaje enviado.
func (c *Client) Send(ctx context.Context, out agentpkg.Outbound) (string, error) {
	msg := wireRecipientMsg{MessagingProduct: "whatsapp"}
	if IsBSUID(out.To) {
		msg.Recipient = stripBSUID(out.To)
	} else {
		msg.RecipientType = "individual"
		msg.To = out.To
	}
	if out.ReplyToMsgID != "" {
		msg.Context = &wireContext{MessageID: out.ReplyToMsgID}
	}

	switch {
	case out.Template != nil:
		msg.Type = "template"
		comps := []wireTplComp{}
		if len(out.Template.Params) > 0 {
			params := make([]wireTplParam, len(out.Template.Params))
			for i, p := range out.Template.Params {
				params[i] = wireTplParam{Type: "text", Text: p}
			}
			comps = append(comps, wireTplComp{Type: "body", Parameters: params})
		}
		msg.Template = &wireTemplate{
			Name:       out.Template.Name,
			Language:   wireLang{Code: out.Template.Language},
			Components: comps,
		}
	case out.Menu != nil:
		msg.Type = "interactive"
		msg.Interactive = buildInteractiveList(*out.Menu, out.Text)
	default:
		msg.Type = "text"
		msg.Text = &wireText{Body: toWhatsAppMarkdown(out.Text)}
	}

	return c.sendRaw(ctx, msg)
}

func buildInteractiveList(menu agentpkg.Menu, bodyText string) *wireInteractive {
	rows := make([]wireListRow, 0, maxRows)
	for _, opt := range menu.Options {
		if len(rows) >= maxRows {
			break
		}
		rows = append(rows, wireListRow{
			ID:          opt.ID,
			Title:       clip(opt.Title, maxRowTitle),
			Description: clip(opt.Description, maxRowDesc),
		})
	}
	body := bodyText
	if body == "" {
		body = menu.Description
	}
	return &wireInteractive{
		Type: "list",
		Body: wireInteractiveText{Text: toWhatsAppMarkdown(body)},
		Action: wireInteractiveAction{
			Button:   clip(firstNonEmpty(menu.Title, "Ver opciones"), maxButtonChars),
			Sections: []wireListSection{{Rows: rows}},
		},
	}
}

func (c *Client) sendRaw(ctx context.Context, msg wireRecipientMsg) (string, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("whatsapp: marshal mensaje: %w", err)
	}
	url := fmt.Sprintf("%s/%s/%s/messages", c.GraphURL, c.APIVersion, c.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("whatsapp: construir request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("whatsapp: request falló: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whatsapp: status %d: %s", resp.StatusCode, string(respBody))
	}

	var wireResp struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(respBody, &wireResp); err == nil && len(wireResp.Messages) > 0 {
		return wireResp.Messages[0].ID, nil
	}
	return "", nil
}

var (
	reBold1  = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reBold2  = regexp.MustCompile(`__(.+?)__`)
	reHeader = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// toWhatsAppMarkdown adapta el markdown que el modelo pueda haber generado
// al formato real de WhatsApp: **bold**/__bold__ -> *bold* (WhatsApp usa un
// solo asterisco), quita encabezados `#`, y reescribe enlaces markdown
// (WhatsApp no los interpreta, los muestra como texto literal roto).
func toWhatsAppMarkdown(s string) string {
	s = reBold1.ReplaceAllString(s, "*$1*")
	s = reBold2.ReplaceAllString(s, "*$1*")
	s = reHeader.ReplaceAllString(s, "")
	s = reLink.ReplaceAllString(s, "$1: $2")
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
