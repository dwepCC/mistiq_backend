package memory

import (
	"context"
	"fmt"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ConversationStore resuelve/crea el hilo de conversación para un contacto.
type ConversationStore struct {
	db *gorm.DB
}

func NewConversationStore(db *gorm.DB) *ConversationStore {
	return &ConversationStore{db: db}
}

// GetOrCreate busca un hilo NO cerrado para (assistantID, channel, contactRef)
// o crea uno nuevo en estado "bot".
func (c *ConversationStore) GetOrCreate(ctx context.Context, assistantID uint, channel, contactRef, locale string) (database.AssistantConversation, bool, error) {
	var conv database.AssistantConversation
	err := c.db.WithContext(ctx).
		Where("assistant_id = ? AND channel = ? AND contact_ref = ? AND status <> ?",
			assistantID, channel, contactRef, database.ConvStatusClosed).
		Order("id DESC").
		First(&conv).Error

	if err == nil {
		return conv, false, nil
	}
	if err != gorm.ErrRecordNotFound {
		return database.AssistantConversation{}, false, fmt.Errorf("memory: buscar conversación: %w", err)
	}

	conv = database.AssistantConversation{
		AssistantID: assistantID,
		Channel:     channel,
		ContactRef:  contactRef,
		Status:      database.ConvStatusBot,
		Locale:      locale,
		LastMsgAt:   time.Now(),
	}
	if err := c.db.WithContext(ctx).Create(&conv).Error; err != nil {
		return database.AssistantConversation{}, false, fmt.Errorf("memory: crear conversación: %w", err)
	}
	return conv, true, nil
}

// Find busca un hilo NO cerrado para (assistantID, channel, contactRef) sin
// crearlo. Usado para rehidratar un hilo existente (p. ej. GET de mensajes
// del chat web) sin el efecto secundario de abrir uno nuevo.
func (c *ConversationStore) Find(ctx context.Context, assistantID uint, channel, contactRef string) (database.AssistantConversation, bool, error) {
	var conv database.AssistantConversation
	err := c.db.WithContext(ctx).
		Where("assistant_id = ? AND channel = ? AND contact_ref = ? AND status <> ?",
			assistantID, channel, contactRef, database.ConvStatusClosed).
		Order("id DESC").
		First(&conv).Error
	if err == nil {
		return conv, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return database.AssistantConversation{}, false, nil
	}
	return database.AssistantConversation{}, false, fmt.Errorf("memory: buscar conversación: %w", err)
}

// TouchLastMsgAt actualiza el timestamp del último mensaje del hilo.
func (c *ConversationStore) TouchLastMsgAt(ctx context.Context, conversationID uint) error {
	return c.db.WithContext(ctx).
		Model(&database.AssistantConversation{}).
		Where("id = ?", conversationID).
		Update("last_msg_at", time.Now()).Error
}
