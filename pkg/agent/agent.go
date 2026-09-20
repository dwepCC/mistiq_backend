package agent

import (
	"fmt"
	"sync"

	"tukifac/pkg/agent/bizctx"
)

// AgentType es la definición en CÓDIGO de una clase de agente (qué scope
// ocupa, qué herramientas puede usar, con qué tope de rondas/modelo por
// defecto). Separado de Config (la fila `assistants`, la CONFIGURACIÓN en
// BD) a propósito: el código decide qué es posible, la config decide qué
// está activo para una instancia concreta.
type AgentType struct {
	Key       string
	Scope     bizctx.Scope
	Tools     []string
	MaxRounds int
	Model     string
}

// TypeRegistry es un catálogo thread-safe de AgentType disponibles en el
// binario. Se puebla al arrancar (fallar temprano, no en el primer mensaje).
type TypeRegistry struct {
	mu    sync.RWMutex
	types map[string]AgentType
}

func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{types: make(map[string]AgentType)}
}

// Register agrega un AgentType al catálogo. Falla si la clave está vacía,
// el scope está vacío, o ya existe una clave igual (evita ambigüedad).
func (r *TypeRegistry) Register(t AgentType) error {
	if t.Key == "" {
		return fmt.Errorf("agent: AgentType.Key vacío")
	}
	if t.Scope == "" {
		return fmt.Errorf("agent: AgentType.Scope vacío para %q", t.Key)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.types[t.Key]; exists {
		return fmt.Errorf("agent: AgentType %q ya registrado", t.Key)
	}
	r.types[t.Key] = t
	return nil
}

func (r *TypeRegistry) Get(key string) (AgentType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.types[key]
	return t, ok
}

func (r *TypeRegistry) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.types))
	for k := range r.types {
		keys = append(keys, k)
	}
	return keys
}

// Definition es la definición EFECTIVA de un agente: tipo (código) +
// instancia (config en BD). Resuelve overrides con la prioridad
// instancia > tipo > default del motor.
type Definition struct {
	Type     AgentType
	Instance Config
}

func (d Definition) Scope() bizctx.Scope {
	return d.Type.Scope
}

// MaxRounds: instancia > tipo > default del motor (3, ver orchestrator).
func (d Definition) MaxRounds() int {
	if d.Instance.MaxToolRounds > 0 {
		return d.Instance.MaxToolRounds
	}
	if d.Type.MaxRounds > 0 {
		return d.Type.MaxRounds
	}
	return 0
}

// Model: instancia > tipo > vacío (el provider decide su default).
func (d Definition) Model() string {
	if d.Instance.LLMModel != "" {
		return d.Instance.LLMModel
	}
	return d.Type.Model
}

// Toolset: herramientas del tipo filtradas por las habilitadas en la
// instancia. Instancia vacía = sin restricción adicional (todas las del
// tipo), NUNCA "ninguna" — un array vacío en BD no es una señal de
// "desactivar todo", es "no hay override".
func (d Definition) Toolset() []string {
	if len(d.Instance.EnabledTools) == 0 {
		return d.Type.Tools
	}
	enabled := make(map[string]bool, len(d.Instance.EnabledTools))
	for _, name := range d.Instance.EnabledTools {
		enabled[name] = true
	}
	out := make([]string, 0, len(d.Type.Tools))
	for _, name := range d.Type.Tools {
		if enabled[name] {
			out = append(out, name)
		}
	}
	return out
}
