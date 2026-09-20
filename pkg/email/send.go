package email

import (
	"bytes"
	"errors"
	"fmt"
	"net/smtp"
	"strings"

	"tukifac/config"
)

// Send envía un correo de texto plano, sin adjunto — distinto de
// SendWithAttachment (que exige un PDF no vacío). Usado por notificaciones
// internas simples (p. ej. el agente comercial avisando al equipo).
func Send(cfg *config.Config, to, subject, textBody string) error {
	if !IsConfigured(cfg) {
		return ErrNotConfigured
	}
	to = strings.TrimSpace(to)
	if !ValidateAddress(to) {
		return errors.New("correo del destinatario inválido")
	}
	from := strings.TrimSpace(cfg.SMTPFrom)
	if from == "" {
		from = "noreply@tukifac.com"
	}

	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	msg.WriteString(textBody)

	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort)
	var auth smtp.Auth
	if strings.TrimSpace(cfg.SMTPUser) != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost)
	}
	return smtp.SendMail(addr, auth, from, []string{to}, msg.Bytes())
}
