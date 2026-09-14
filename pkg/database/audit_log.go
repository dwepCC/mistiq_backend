package database

import (
	"log/slog"
	"strings"

	"gorm.io/gorm"
)

// BeforeCreate normaliza la IP para que la fila entre siempre en ip_address (varchar 45).
//
// La app corre con ProxyHeader = X-Forwarded-For, así que c.IP() devuelve la CADENA entera del
// header, no una IP: detrás de Cloudflare llega "<cliente>, <edge>". Con un cliente IPv6 eso
// pasa de 45 caracteres y MySQL en modo estricto rechaza el INSERT con error 1406. Así se
// habían perdido 109 de 196 ajustes de vigencia, todos los hechos desde IPv6.
//
// Se guarda el primer tramo, que es el cliente real; el resto son saltos del proxy y no aportan
// a la auditoría. Una IPv6 completa mide 45 como máximo, así que ya no hace falta ensanchar la
// columna. El truncado final es solo un cinturón por si llegara algo que no sea una IP.
func (a *AuditLog) BeforeCreate(*gorm.DB) error {
	ip := a.IPAddress
	if i := strings.IndexByte(ip, ','); i >= 0 {
		ip = ip[:i]
	}
	ip = strings.TrimSpace(ip)
	if len(ip) > 45 {
		ip = ip[:45]
	}
	a.IPAddress = ip
	return nil
}

// WriteAuditLog registra una acción en la bitácora central.
//
// Auditar no debe tumbar la operación que se está auditando, así que el error no se propaga —
// pero tampoco se descarta. Antes cada llamador hacía `_ = db.Create(&AuditLog{...}).Error` (o
// ni eso), y un fallo de inserción no dejaba rastro en ningún lado: el error 1406 de la IP
// estuvo dos meses comiéndose más de la mitad de la bitácora sin que nada lo delatara. Acá se
// registra en el log de la aplicación con los datos suficientes para reconstruir la acción
// perdida y para que salte en cuanto vuelva a pasar.
func WriteAuditLog(entry *AuditLog) {
	WriteAuditLogTx(CentralDB, entry)
}

// WriteAuditLogTx es WriteAuditLog sobre una conexión concreta, para auditar dentro de una
// transacción o desde un servicio que trae su propio *gorm.DB. Con db nil cae en CentralDB.
func WriteAuditLogTx(db *gorm.DB, entry *AuditLog) {
	if entry == nil {
		return
	}
	if db == nil {
		db = CentralDB
	}
	if db == nil {
		slog.Error("auditoría no registrada: BD central no disponible",
			slog.String("action", entry.Action),
			slog.String("entity", entry.Entity),
			slog.Uint64("entity_id", uint64(entry.EntityID)),
			slog.Uint64("tenant_id", uint64(entry.TenantID)),
			slog.Uint64("user_id", uint64(entry.UserID)))
		return
	}
	if err := db.Create(entry).Error; err != nil {
		slog.Error("auditoría no registrada",
			slog.String("action", entry.Action),
			slog.String("entity", entry.Entity),
			slog.Uint64("entity_id", uint64(entry.EntityID)),
			slog.Uint64("tenant_id", uint64(entry.TenantID)),
			slog.Uint64("user_id", uint64(entry.UserID)),
			slog.String("payload", entry.Payload),
			slog.Any("error", err))
	}
}
