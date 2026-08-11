package email

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/smtp"
	"strings"

	"tukifac/config"
)

var ErrNotConfigured = errors.New("servidor de correo no configurado (SMTP_HOST)")

func IsConfigured(cfg *config.Config) bool {
	return cfg != nil && strings.TrimSpace(cfg.SMTPHost) != ""
}

func ValidateAddress(addr string) bool {
	addr = strings.TrimSpace(strings.ToLower(addr))
	if len(addr) < 5 || len(addr) > 254 {
		return false
	}
	at := strings.LastIndex(addr, "@")
	if at < 1 || at >= len(addr)-2 {
		return false
	}
	return strings.Contains(addr[at+1:], ".")
}

// SendWithAttachment envía correo MIME multipart con un PDF adjunto.
func SendWithAttachment(cfg *config.Config, to, subject, textBody, fileName string, fileData []byte) error {
	if !IsConfigured(cfg) {
		return ErrNotConfigured
	}
	to = strings.TrimSpace(to)
	if !ValidateAddress(to) {
		return errors.New("correo del destinatario inválido")
	}
	if len(fileData) == 0 {
		return errors.New("el PDF del comprobante está vacío")
	}
	from := strings.TrimSpace(cfg.SMTPFrom)
	if from == "" {
		from = "noreply@tukifac.com"
	}
	safeName := strings.ReplaceAll(strings.TrimSpace(fileName), "\"", "")
	if safeName == "" {
		safeName = "comprobante.pdf"
	}

	boundary := "tukifac-mail-boundary"
	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=%s\r\n\r\n", boundary))

	msg.WriteString("--" + boundary + "\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	msg.WriteString(textBody + "\r\n")

	msg.WriteString("--" + boundary + "\r\n")
	msg.WriteString("Content-Type: application/pdf\r\n")
	msg.WriteString("Content-Transfer-Encoding: base64\r\n")
	msg.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n\r\n", safeName))
	encoded := base64.StdEncoding.EncodeToString(fileData)
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		msg.WriteString(encoded[i:end] + "\r\n")
	}
	msg.WriteString("--" + boundary + "--\r\n")

	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort)
	var auth smtp.Auth
	if strings.TrimSpace(cfg.SMTPUser) != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost)
	}
	return smtp.SendMail(addr, auth, from, []string{to}, msg.Bytes())
}
