// Package service lectura de TenantNotification para el panel interno (Contrato ecommerce v2
// §1.7, Fase 6). La ESCRITURA de notificaciones sigue viviendo donde ya vivía (ecommerce_service.go
// crea la fila dentro de la misma transacción del evento de negocio, ver CreateOrder/
// UpdateOrderStatus/ConvertToSale) — este paquete solo resuelve qué ve cada usuario y su estado de
// lectura, nunca decide cuándo se genera una notificación.
package service

import (
	"errors"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type NotificationService struct {
	db *gorm.DB
}

func NewNotificationService(db *gorm.DB) *NotificationService {
	return &NotificationService{db: db}
}

// broadcastPermissionByType mapa Type -> permiso requerido para que un usuario vea una
// notificación BROADCAST (TenantNotification.UserID = nil). Fail-closed: un Type sin entrada acá
// nunca es visible como broadcast, así que agregar un tipo nuevo exige declarar explícitamente qué
// permiso lo protege. Fase 6: los 4 eventos de pedidos web, todos gateados por el permiso mínimo
// del panel — no se crean permisos nuevos (Contrato v2 Fase 6, "No crear nuevos permisos").
var broadcastPermissionByType = map[string]string{
	"ecommerce.order.created":   "ecommerce.orders_view",
	"ecommerce.order.confirmed": "ecommerce.orders_view",
	"ecommerce.order.cancelled": "ecommerce.orders_view",
	"ecommerce.order.converted": "ecommerce.orders_view",
}

// hasImpliedPermission mismo criterio que internal/ecommerce/handler/ecommerce_scope.go
// hasEcommercePermission: coincidencia exacta o "{módulo}.manage" (regla genérica de
// pkg/middleware/tenant_permissions.go). Se reimplementa acá (en vez de importar el paquete
// middleware, no exportado) siguiendo el mismo patrón de duplicación puntual ya usado entre
// cashbank_scope.go/ecommerce_scope.go.
func hasImpliedPermission(permissions []string, required string) bool {
	for _, p := range permissions {
		if p == required {
			return true
		}
	}
	if i := strings.IndexByte(required, '.'); i > 0 {
		manage := required[:i] + ".manage"
		for _, p := range permissions {
			if p == manage {
				return true
			}
		}
	}
	return false
}

// allowedBroadcastTypes tipos broadcast visibles para este conjunto de permisos.
func allowedBroadcastTypes(permissions []string) []string {
	out := make([]string, 0, len(broadcastPermissionByType))
	for t, perm := range broadcastPermissionByType {
		if hasImpliedPermission(permissions, perm) {
			out = append(out, t)
		}
	}
	return out
}

// NotificationView notificación ya resuelta para UN usuario concreto — Read ya tiene en cuenta si
// es dirigida (TenantNotification.ReadAt) o broadcast (TenantNotificationRead), el frontend nunca
// tiene que distinguir los dos casos.
type NotificationView struct {
	ID        uint      `json:"id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	LinkPath  string    `json:"link_path"`
	CreatedAt time.Time `json:"created_at"`
	Read      bool      `json:"read"`
}

// ListParams UserID/Permissions siempre derivados del JWT en el handler, nunca del query string.
type ListParams struct {
	UserID      uint
	Permissions []string
	Limit       int
	BeforeID    uint // cursor "cargar más": solo filas con id < BeforeID
}

// List notificaciones visibles para params.UserID: las dirigidas a él + las broadcast de un tipo
// cuyo permiso tiene. Nunca lee/filtra por columnas fuera de esa condición (aislamiento entre
// usuarios y tenants: el tenant ya está implícito en s.db, una conexión por tenant).
func (s *NotificationService) List(params ListParams) ([]NotificationView, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	allowedTypes := allowedBroadcastTypes(params.Permissions)

	q := s.db.Model(&database.TenantNotification{})
	if len(allowedTypes) > 0 {
		q = q.Where("user_id = ? OR (user_id IS NULL AND type IN ?)", params.UserID, allowedTypes)
	} else {
		q = q.Where("user_id = ?", params.UserID)
	}
	if params.BeforeID > 0 {
		q = q.Where("id < ?", params.BeforeID)
	}
	var rows []database.TenantNotification
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []NotificationView{}, nil
	}

	broadcastIDs := make([]uint, 0, len(rows))
	for _, r := range rows {
		if r.UserID == nil {
			broadcastIDs = append(broadcastIDs, r.ID)
		}
	}
	readBroadcast := map[uint]bool{}
	if len(broadcastIDs) > 0 {
		var reads []database.TenantNotificationRead
		if err := s.db.Where("user_id = ? AND notification_id IN ?", params.UserID, broadcastIDs).
			Find(&reads).Error; err != nil {
			return nil, err
		}
		for _, r := range reads {
			readBroadcast[r.NotificationID] = true
		}
	}

	out := make([]NotificationView, len(rows))
	for i, r := range rows {
		read := false
		if r.UserID != nil {
			read = r.ReadAt != nil
		} else {
			read = readBroadcast[r.ID]
		}
		out[i] = NotificationView{
			ID: r.ID, Type: r.Type, Title: r.Title, Body: r.Body, LinkPath: r.LinkPath,
			CreatedAt: r.CreatedAt, Read: read,
		}
	}
	return out, nil
}

// UnreadCount fuente de verdad real del badge — el frontend nunca la calcula localmente.
func (s *NotificationService) UnreadCount(userID uint, permissions []string) (int64, error) {
	var directCount int64
	if err := s.db.Model(&database.TenantNotification{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Count(&directCount).Error; err != nil {
		return 0, err
	}
	allowedTypes := allowedBroadcastTypes(permissions)
	if len(allowedTypes) == 0 {
		return directCount, nil
	}
	var broadcastCount int64
	err := s.db.Table("tenant_notifications AS n").
		Joins("LEFT JOIN tenant_notification_reads r ON r.notification_id = n.id AND r.user_id = ?", userID).
		Where("n.user_id IS NULL AND n.type IN ? AND r.id IS NULL", allowedTypes).
		Count(&broadcastCount).Error
	if err != nil {
		return 0, err
	}
	return directCount + broadcastCount, nil
}

// MarkRead: dirigida -> set TenantNotification.ReadAt (solo si UserID == caller). Broadcast -> fila
// en TenantNotificationRead para userID, NUNCA toca TenantNotification.ReadAt (eso marcaría
// "leído" para todos los que la ven, ver comentario del struct en pkg/database/migrations.go).
func (s *NotificationService) MarkRead(id, userID uint, permissions []string) error {
	var n database.TenantNotification
	if err := s.db.First(&n, id).Error; err != nil {
		return errors.New("notificación no encontrada")
	}
	if n.UserID != nil {
		if *n.UserID != userID {
			return errors.New("no autorizado")
		}
		if n.ReadAt != nil {
			return nil
		}
		now := time.Now()
		return s.db.Model(&database.TenantNotification{}).Where("id = ?", id).Update("read_at", &now).Error
	}
	// Broadcast: revalida el permiso del tipo acá también (defensa en profundidad, no confiar en
	// que el caller solo intente marcar lo que List() ya le mostró).
	perm, ok := broadcastPermissionByType[n.Type]
	if !ok || !hasImpliedPermission(permissions, perm) {
		return errors.New("no autorizado")
	}
	read := database.TenantNotificationRead{NotificationID: id, UserID: userID, ReadAt: time.Now()}
	// OnConflict DoNothing: si dos requests concurrentes marcan la misma notificación, la UNIQUE
	// (notification_id, user_id) ya lo impedía a nivel de BD — esto solo evita que la segunda
	// request reciba un error de duplicado por algo que no es un fallo real.
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&read).Error
}

// MarkAllRead mismo criterio que MarkRead pero en bloque — una sola transacción, nunca deja las
// dirigidas actualizadas sin las broadcast (o viceversa) si algo falla a mitad de camino.
func (s *NotificationService) MarkAllRead(userID uint, permissions []string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Model(&database.TenantNotification{}).
			Where("user_id = ? AND read_at IS NULL", userID).
			Update("read_at", &now).Error; err != nil {
			return err
		}
		allowedTypes := allowedBroadcastTypes(permissions)
		if len(allowedTypes) == 0 {
			return nil
		}
		var unreadIDs []uint
		if err := tx.Table("tenant_notifications AS n").
			Select("n.id").
			Joins("LEFT JOIN tenant_notification_reads r ON r.notification_id = n.id AND r.user_id = ?", userID).
			Where("n.user_id IS NULL AND n.type IN ? AND r.id IS NULL", allowedTypes).
			Scan(&unreadIDs).Error; err != nil {
			return err
		}
		if len(unreadIDs) == 0 {
			return nil
		}
		reads := make([]database.TenantNotificationRead, len(unreadIDs))
		for i, id := range unreadIDs {
			reads[i] = database.TenantNotificationRead{NotificationID: id, UserID: userID, ReadAt: now}
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&reads).Error
	})
}
