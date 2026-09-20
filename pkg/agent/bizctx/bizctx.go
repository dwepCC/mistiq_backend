// Package bizctx define el contexto de negocio con el que se ejecuta una
// acción del agente: quién es, en qué scope opera, y qué identidades trae
// disponibles (assistant, conversación, lead). No importa pkg/agent para
// evitar ciclos — es un paquete de hoja.
package bizctx

// Scope acota qué puede tocar una acción. v1 (Mistiq): solo ScopePlatform,
// el agente comercial de la plataforma que vende el propio SaaS. ScopeTenant
// queda declarado para una futura extensión (agente por tenant) que no se
// implementa todavía — ver docs/CHATBOT-AGENT-ARCHITECTURE.md §5.
type Scope string

const (
	ScopePlatform Scope = "platform"
	ScopeTenant   Scope = "tenant"
)

// Context es el contexto de negocio pasado a cada Action.Execute.
type Context struct {
	Scope          Scope
	AssistantID    uint
	ConversationID uint
	ContactRef     string
	Channel        string
	Locale         string
}

// Valid verifica la integridad mínima del contexto (usado por la política
// ScopeIntegrity antes de ejecutar cualquier acción).
func (c Context) Valid() bool {
	if c.Scope != ScopePlatform && c.Scope != ScopeTenant {
		return false
	}
	if c.AssistantID == 0 {
		return false
	}
	return true
}
