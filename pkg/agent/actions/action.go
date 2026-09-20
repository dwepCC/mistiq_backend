// Package actions define el framework de function calling del motor:
// interfaz Action, Registry, y el contrato de resultado/auditoría. El
// catálogo de acciones comerciales concretas (crear lead, validar RUC,
// crear tenant de prueba, ...) se agrega en una fase posterior — este
// paquete solo trae el framework, deliberadamente vacío de negocio.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"tukifac/pkg/agent/bizctx"
)

// Meta declara propiedades de una acción, leídas por las políticas sin
// listas hardcodeadas en otro lado.
type Meta struct {
	Sensitive          bool // requiere confirmación explícita del policy Confirmation
	ReadOnly           bool
	Idempotent         bool // habilita reintento automático ante error transitorio
	RequiredPermission string
}

// Result es lo que una acción devuelve al motor.
type Result struct {
	// Message es lo que se reinyecta al modelo como resultado de la tool
	// call (puede incluir datos sensibles reales, p. ej. una contraseña
	// recién generada — ver AuditSummary para separar eso del log).
	Message string
	// AuditSummary es un resumen para auditoría/logs SIN datos sensibles.
	// Si queda vacío, se usa Message tal cual.
	AuditSummary string
}

// Action es la interfaz que implementa cada herramienta expuesta al LLM.
type Action interface {
	Name() string
	Description() string
	Schema() json.RawMessage // JSON-Schema de los argumentos
	Meta() Meta
	Execute(ctx context.Context, bc bizctx.Context, args json.RawMessage) (Result, error)
}

// Registry es el catálogo global de acciones disponibles en el binario.
type Registry struct {
	mu      sync.RWMutex
	actions map[string]Action
}

func NewRegistry() *Registry {
	return &Registry{actions: make(map[string]Action)}
}

func (r *Registry) Register(a Action) error {
	name := a.Name()
	if name == "" {
		return fmt.Errorf("actions: Action.Name() vacío")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.actions[name]; exists {
		return fmt.Errorf("actions: %q ya registrada", name)
	}
	r.actions[name] = a
	return nil
}

func (r *Registry) Get(name string) (Action, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actions[name]
	return a, ok
}

// SpecsFor filtra el catálogo por el toolset efectivo del agente y lo
// ordena alfabéticamente (estabilidad para el prompt caching del proveedor).
func (r *Registry) SpecsFor(toolset []string) []Action {
	r.mu.RLock()
	defer r.mu.RUnlock()
	allowed := make(map[string]bool, len(toolset))
	for _, name := range toolset {
		allowed[name] = true
	}
	out := make([]Action, 0, len(allowed))
	for name, a := range r.actions {
		if len(toolset) == 0 || allowed[name] {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
