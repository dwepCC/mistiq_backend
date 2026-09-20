package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tukifac/pkg/database"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	maxHotTurns = 50
	defaultTTL  = 30 * time.Minute
)

// Store implementa Memory combinando Redis (caliente) + MySQL (permanente).
// Si rdb es nil, degrada a solo MySQL (sin caché).
type Store struct {
	db  *gorm.DB
	rdb *redis.Client
}

var _ Memory = (*Store)(nil)

func NewStore(db *gorm.DB, rdb *redis.Client) *Store {
	return &Store{db: db, rdb: rdb}
}

func redisKey(conversationID uint) string {
	return fmt.Sprintf("assistant:conv:%d:turns", conversationID)
}

type hotTurn struct {
	ID               uint      `json:"id"`
	Role             Role      `json:"role"`
	Content          string    `json:"content"`
	ToolName         string    `json:"tool_name,omitempty"`
	ToolCallID       string    `json:"tool_call_id,omitempty"`
	ChannelMsgID     string    `json:"channel_msg_id,omitempty"`
	ReplyToID        uint      `json:"reply_to_id,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	TotalTokens      int       `json:"total_tokens,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

func toHotTurn(t Turn) hotTurn {
	return hotTurn{
		ID: t.ID, Role: t.Role, Content: t.Content, ToolName: t.ToolName,
		ToolCallID: t.ToolCallID, ChannelMsgID: t.ChannelMsgID, ReplyToID: t.ReplyToID,
		PromptTokens: t.PromptTokens, CompletionTokens: t.CompletionTokens, TotalTokens: t.TotalTokens,
		CreatedAt: t.CreatedAt,
	}
}

func (h hotTurn) toTurn(conversationID uint) Turn {
	return Turn{
		ID: h.ID, ConversationID: conversationID, Role: h.Role, Content: h.Content,
		ToolName: h.ToolName, ToolCallID: h.ToolCallID, ChannelMsgID: h.ChannelMsgID,
		ReplyToID: h.ReplyToID, PromptTokens: h.PromptTokens, CompletionTokens: h.CompletionTokens,
		TotalTokens: h.TotalTokens, CreatedAt: h.CreatedAt,
	}
}

func (s *Store) AppendTurn(ctx context.Context, t Turn) (Turn, error) {
	row := database.AssistantMessage{
		ConversationID:   t.ConversationID,
		Role:             string(t.Role),
		Content:          t.Content,
		ToolName:         t.ToolName,
		ToolCallID:       t.ToolCallID,
		ChannelMsgID:     t.ChannelMsgID,
		ReplyToID:        t.ReplyToID,
		PromptTokens:     t.PromptTokens,
		CompletionTokens: t.CompletionTokens,
		TotalTokens:      t.TotalTokens,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return Turn{}, fmt.Errorf("memory: persistir turno: %w", err)
	}
	t.ID = row.ID
	t.CreatedAt = row.CreatedAt

	if s.rdb != nil {
		s.warmAppend(ctx, t)
	}
	return t, nil
}

// warmAppend escribe en Redis best-effort: un fallo acá nunca hace fallar
// AppendTurn (la fuente de verdad es MySQL).
func (s *Store) warmAppend(ctx context.Context, t Turn) {
	raw, err := json.Marshal(toHotTurn(t))
	if err != nil {
		return
	}
	key := redisKey(t.ConversationID)
	pipe := s.rdb.TxPipeline()
	pipe.RPush(ctx, key, raw)
	pipe.LTrim(ctx, key, -maxHotTurns, -1)
	pipe.Expire(ctx, key, defaultTTL)
	_, _ = pipe.Exec(ctx)
}

func (s *Store) LoadContext(ctx context.Context, conversationID uint, window int) ([]Turn, error) {
	if window <= 0 {
		window = 20
	}
	if s.rdb != nil {
		if turns, ok := s.loadFromRedis(ctx, conversationID, window); ok {
			return turns, nil
		}
	}
	turns, err := s.loadFromDB(ctx, conversationID, window)
	if err != nil {
		return nil, err
	}
	if s.rdb != nil {
		s.warmRedis(ctx, conversationID, turns)
	}
	return turns, nil
}

func (s *Store) loadFromRedis(ctx context.Context, conversationID uint, window int) ([]Turn, bool) {
	key := redisKey(conversationID)
	raws, err := s.rdb.LRange(ctx, key, 0, -1).Result()
	if err != nil || len(raws) == 0 {
		return nil, false
	}
	turns := make([]Turn, 0, len(raws))
	for _, raw := range raws {
		var h hotTurn
		if err := json.Unmarshal([]byte(raw), &h); err != nil {
			continue
		}
		turns = append(turns, h.toTurn(conversationID))
	}
	if len(turns) > window {
		turns = turns[len(turns)-window:]
	}
	return turns, true
}

func (s *Store) loadFromDB(ctx context.Context, conversationID uint, window int) ([]Turn, error) {
	var rows []database.AssistantMessage
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		Order("created_at DESC, id DESC").
		Limit(window).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("memory: cargar historial: %w", err)
	}
	// rows viene en orden descendente; invertir a ascendente.
	turns := make([]Turn, len(rows))
	for i, r := range rows {
		turns[len(rows)-1-i] = Turn{
			ID: r.ID, ConversationID: r.ConversationID, Role: Role(r.Role), Content: r.Content,
			ToolName: r.ToolName, ToolCallID: r.ToolCallID, ChannelMsgID: r.ChannelMsgID,
			ReplyToID: r.ReplyToID, PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens,
			TotalTokens: r.TotalTokens, CreatedAt: r.CreatedAt,
		}
	}
	return turns, nil
}

func (s *Store) warmRedis(ctx context.Context, conversationID uint, turns []Turn) {
	if len(turns) == 0 {
		return
	}
	key := redisKey(conversationID)
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, key)
	for _, t := range turns {
		raw, err := json.Marshal(toHotTurn(t))
		if err != nil {
			continue
		}
		pipe.RPush(ctx, key, raw)
	}
	pipe.Expire(ctx, key, defaultTTL)
	_, _ = pipe.Exec(ctx)
}

func (s *Store) MarkNeedsHuman(ctx context.Context, conversationID uint, reason string) error {
	return s.db.WithContext(ctx).
		Model(&database.AssistantConversation{}).
		Where("id = ?", conversationID).
		Updates(map[string]any{
			"status":             database.ConvStatusNeedsHuman,
			"needs_human_reason": reason,
		}).Error
}
