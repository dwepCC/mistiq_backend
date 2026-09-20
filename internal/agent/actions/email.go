package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"tukifac/config"
	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/email"
)

type NotifyTeam struct {
	Cfg *config.Config
}

func NewNotifyTeam(cfg *config.Config) *NotifyTeam { return &NotifyTeam{Cfg: cfg} }

func (NotifyTeam) Name() string { return "commercial_notify_team" }
func (NotifyTeam) Description() string {
	return "Envía una notificación por correo al equipo comercial de Mistiq sobre algo que necesita atención humana pronto (una oportunidad importante, una duda que no pudiste resolver). No la uses para cada mensaje — solo cuando de verdad amerite avisar."
}
func (NotifyTeam) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["subject","message"],"properties":{
		"subject":{"type":"string"},"message":{"type":"string"}
	}}`)
}
func (NotifyTeam) Meta() actions.Meta { return actions.Meta{Idempotent: true} }

type notifyTeamArgs struct {
	Subject string `json:"subject"`
	Message string `json:"message"`
}

func (a *NotifyTeam) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var args notifyTeamArgs
	if err := decodeArgs(raw, &args); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("subject", args.Subject); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("message", args.Message); err != nil {
		return actions.Result{}, err
	}

	if a.Cfg.AssistantNotifyEmail == "" {
		// No configurado: no es un error del modelo ni del cliente — se
		// avisa igual en el resultado para que el modelo no insista, pero
		// no se rompe la conversación.
		return actions.Result{Message: "(No hay correo de notificación configurado; el equipo no recibió el aviso automático.)"}, nil
	}
	if !email.IsConfigured(a.Cfg) {
		return actions.Result{Message: "(El servidor de correo no está configurado; el equipo no recibió el aviso automático.)"}, nil
	}

	body := fmt.Sprintf("%s\n\n— Conversación #%d, canal %s, contacto %s.", args.Message, bc.ConversationID, bc.Channel, bc.ContactRef)
	if err := email.Send(a.Cfg, a.Cfg.AssistantNotifyEmail, "[Agente Mistiq] "+args.Subject, body); err != nil {
		return actions.Result{}, fmt.Errorf("enviar notificación al equipo: %w", err)
	}
	return actions.Result{Message: "Notificación enviada al equipo."}, nil
}
