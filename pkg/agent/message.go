// Package agent contiene los tipos agnósticos de canal del motor del agente
// comercial (mensaje entrante/saliente). Este archivo NO importa ningún
// subpaquete (providers, memory, knowledge, ...) para evitar ciclos de
// dependencia — los subpaquetes importan este paquete raíz, nunca al revés.
package agent

import "time"

// Inbound es un mensaje entrante normalizado, agnóstico del canal de origen
// (WhatsApp, chat web, futuros canales).
type Inbound struct {
	AssistantID      uint
	Channel          string // "whatsapp" | "webchat" | "test"
	ChannelAccountID string // número/ID de la cuenta del canal que recibió el mensaje
	ChannelMsgID     string // id del mensaje en el canal origen (dedup)
	From             string // referencia del contacto (teléfono, "web:<session>")
	Text             string
	Locale           string
	ReceivedAt       time.Time
}

// MenuOption es una opción de un menú interactivo (lista de WhatsApp, botones web).
type MenuOption struct {
	ID          string
	Title       string
	Description string
}

// Menu es un menú interactivo opcional adjunto a una respuesta saliente.
// Máximo 10 opciones (límite real de las listas interactivas de WhatsApp).
type Menu struct {
	Title       string
	Description string
	Options     []MenuOption
}

// OutboundTemplate referencia una plantilla de mensaje aprobada por el canal
// (WhatsApp exige plantilla para escribir fuera de la ventana de 24h).
type OutboundTemplate struct {
	Name     string
	Language string
	Params   []string
}

// Outbound es la respuesta que el motor produce para un turno.
type Outbound struct {
	To           string
	Text         string
	Menu         *Menu
	ReplyToMsgID string
	Template     *OutboundTemplate
	Silent       bool // true: el motor no generó nada (p. ej. un humano tiene la conversación)
}
