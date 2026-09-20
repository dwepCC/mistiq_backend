// Package knowledge define el puerto RAG del motor: indexar/recuperar
// fragmentos de conocimiento scopeados por asistente. v1: implementación
// MySQL + coseno en Go (mysql/store.go) — "cero infra nueva", migrable a un
// motor vectorial real después detrás de esta misma interfaz.
package knowledge

import "context"

// KBScope acota toda operación de conocimiento. v1: KbID == AssistantID
// (ver ScopeFor) — namespace preparado para un futuro con varias bases de
// conocimiento por asistente.
type KBScope struct {
	AssistantID uint
	KbID        uint
}

func ScopeFor(assistantID uint) KBScope {
	return KBScope{AssistantID: assistantID, KbID: assistantID}
}

// Chunk es un fragmento indexado del corpus.
type Chunk struct {
	ID         uint
	SourceType string
	Title      string
	Content    string
	Score      float32 // similitud con la consulta, solo en resultados de Retrieve
}

// Retriever recupera los fragmentos más relevantes para una consulta.
type Retriever interface {
	Retrieve(ctx context.Context, scope KBScope, query string, k int) ([]Chunk, error)
}

// Indexer da de alta/baja contenido del corpus.
type Indexer interface {
	// Upsert trocea `content`, embebe cada fragmento y los guarda. Reemplaza
	// cualquier fragmento previo con el mismo `title` en el scope.
	Upsert(ctx context.Context, scope KBScope, sourceType, title, content string) (chunks int, err error)
	DeleteByTitle(ctx context.Context, scope KBScope, title string) error
	Delete(ctx context.Context, id uint) error
	List(ctx context.Context, scope KBScope) ([]Chunk, error)
}

// Embedder es el subconjunto de providers.LLM que el paquete knowledge
// necesita, para no acoplarse al paquete providers entero.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
