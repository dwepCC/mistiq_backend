// Package mysql implementa knowledge.Retriever/Indexer sobre MySQL, con
// similitud coseno calculada en Go (sin motor vectorial). Diseño v1
// deliberadamente simple: "cero infra nueva", detrás de una interfaz
// migrable a un motor vectorial real después.
package mysql

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"tukifac/pkg/agent/knowledge"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"

	"gorm.io/gorm"
)

const (
	defaultMaxCandidates = 500
	defaultMinScore      = 0.30
	defaultChunkSize     = 800
)

type Store struct {
	db       *gorm.DB
	embedder knowledge.Embedder
}

var (
	_ knowledge.Retriever = (*Store)(nil)
	_ knowledge.Indexer   = (*Store)(nil)
)

func New(db *gorm.DB, embedder knowledge.Embedder) *Store {
	return &Store{db: db, embedder: embedder}
}

func (s *Store) Upsert(ctx context.Context, scope knowledge.KBScope, sourceType, title, content string) (int, error) {
	if err := s.DeleteByTitle(ctx, scope, title); err != nil {
		return 0, err
	}
	pieces := knowledge.Split(content, defaultChunkSize)
	if len(pieces) == 0 {
		return 0, nil
	}
	if s.embedder == nil {
		return 0, fmt.Errorf("knowledge: proveedor de embeddings no configurado")
	}
	vectors, err := s.embedder.Embed(ctx, pieces)
	if err != nil {
		return 0, fmt.Errorf("knowledge: embeber contenido: %w", err)
	}
	rows := make([]database.AssistantKnowledgeChunk, 0, len(pieces))
	for i, piece := range pieces {
		var embJSON string
		if i < len(vectors) && vectors[i] != nil {
			embJSON, err = knowledge.EncodeEmbedding(vectors[i])
			if err != nil {
				return 0, fmt.Errorf("knowledge: codificar embedding: %w", err)
			}
		}
		rows = append(rows, database.AssistantKnowledgeChunk{
			AssistantID: scope.AssistantID,
			KbID:        scope.KbID,
			SourceType:  sourceType,
			Title:       title,
			Content:     piece,
			Embedding:   embJSON,
			TokenCount:  len(strings.Fields(piece)),
		})
	}
	if err := s.db.WithContext(ctx).Create(&rows).Error; err != nil {
		return 0, fmt.Errorf("knowledge: guardar fragmentos: %w", err)
	}
	return len(rows), nil
}

func (s *Store) DeleteByTitle(ctx context.Context, scope knowledge.KBScope, title string) error {
	return s.db.WithContext(ctx).
		Where("assistant_id = ? AND kb_id = ? AND title = ?", scope.AssistantID, scope.KbID, title).
		Delete(&database.AssistantKnowledgeChunk{}).Error
}

func (s *Store) Delete(ctx context.Context, id uint) error {
	return s.db.WithContext(ctx).Delete(&database.AssistantKnowledgeChunk{}, id).Error
}

func (s *Store) List(ctx context.Context, scope knowledge.KBScope) ([]knowledge.Chunk, error) {
	var rows []database.AssistantKnowledgeChunk
	if err := s.db.WithContext(ctx).
		Where("assistant_id = ? AND kb_id = ?", scope.AssistantID, scope.KbID).
		Order("title, id").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("knowledge: listar fragmentos: %w", err)
	}
	out := make([]knowledge.Chunk, len(rows))
	for i, r := range rows {
		out[i] = knowledge.Chunk{ID: r.ID, SourceType: r.SourceType, Title: r.Title, Content: r.Content}
	}
	return out, nil
}

func (s *Store) Retrieve(ctx context.Context, scope knowledge.KBScope, query string, k int) ([]knowledge.Chunk, error) {
	if k <= 0 {
		k = 5
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	if s.embedder == nil {
		return s.keywordFallback(ctx, scope, query, k)
	}
	vectors, err := s.embedder.Embed(ctx, []string{query})
	if err != nil || len(vectors) == 0 || vectors[0] == nil {
		return s.keywordFallback(ctx, scope, query, k)
	}
	queryVec := vectors[0]

	var rows []database.AssistantKnowledgeChunk
	if err := s.db.WithContext(ctx).
		Where("assistant_id = ? AND kb_id = ? AND embedding <> ''", scope.AssistantID, scope.KbID).
		Limit(defaultMaxCandidates + 1).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("knowledge: buscar candidatos: %w", err)
	}
	s.warnIfTruncated(scope, len(rows))
	if len(rows) > defaultMaxCandidates {
		rows = rows[:defaultMaxCandidates]
	}

	type scored struct {
		chunk knowledge.Chunk
		score float32
	}
	results := make([]scored, 0, len(rows))
	for _, r := range rows {
		vec, err := knowledge.DecodeEmbedding(r.Embedding)
		if err != nil || vec == nil {
			continue
		}
		score := knowledge.Cosine(queryVec, vec)
		if score < defaultMinScore {
			continue
		}
		results = append(results, scored{
			chunk: knowledge.Chunk{ID: r.ID, SourceType: r.SourceType, Title: r.Title, Content: r.Content, Score: score},
			score: score,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })
	if len(results) > k {
		results = results[:k]
	}
	out := make([]knowledge.Chunk, len(results))
	for i, r := range results {
		out[i] = r.chunk
	}
	return out, nil
}

// keywordFallback: si falla el embedding de la consulta o no hay proveedor
// configurado, cae a búsqueda por palabra clave (LIKE) en vez de fallar.
func (s *Store) keywordFallback(ctx context.Context, scope knowledge.KBScope, query string, k int) ([]knowledge.Chunk, error) {
	var rows []database.AssistantKnowledgeChunk
	like := "%" + query + "%"
	if err := s.db.WithContext(ctx).
		Where("assistant_id = ? AND kb_id = ? AND content LIKE ?", scope.AssistantID, scope.KbID, like).
		Limit(k).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("knowledge: búsqueda por palabra clave: %w", err)
	}
	out := make([]knowledge.Chunk, len(rows))
	for i, r := range rows {
		out[i] = knowledge.Chunk{ID: r.ID, SourceType: r.SourceType, Title: r.Title, Content: r.Content}
	}
	return out, nil
}

// warnIfTruncated avisa (log, no error) cuando el corpus supera el tope de
// candidatos: a partir de ahí se puntúa un subconjunto arbitrario, lo que
// puede hacer que el bot "no encuentre" contenido que sí existe.
func (s *Store) warnIfTruncated(scope knowledge.KBScope, total int) {
	if total <= defaultMaxCandidates {
		return
	}
	logger.L.Warn("knowledge_candidates_truncated",
		slog.Uint64("assistant_id", uint64(scope.AssistantID)),
		slog.Uint64("kb_id", uint64(scope.KbID)),
		slog.Int("total_candidates", total),
		slog.Int("max_candidates", defaultMaxCandidates),
	)
}
