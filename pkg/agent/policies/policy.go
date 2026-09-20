// Package policies implementa las reglas DURAS del motor (viven en Go, si
// fallan es un bug) — distintas de las reglas blandas que viven en el
// prompt (pueden fallar, es el modelo el que decide seguirlas o no). Ver
// docs/CHATBOT-AGENT-ARCHITECTURE.md §2.5.
package policies

import (
	"context"
	"fmt"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
)

// Policy se evalúa ANTES de ejecutar una acción.
type Policy interface {
	Check(ctx context.Context, bc bizctx.Context, action actions.Action, args []byte) error
}

// ResultPolicy se evalúa DESPUÉS de una ejecución exitosa, sobre el
// resultado.
type ResultPolicy interface {
	Inspect(ctx context.Context, bc bizctx.Context, action actions.Action, result actions.Result) error
}

// ErrPolicyDenied es el error que debe devolver una Policy cuando rechaza
// la ejecución (el executor lo distingue de un error de la acción misma).
type ErrPolicyDenied struct {
	Policy string
	Reason string
}

func (e *ErrPolicyDenied) Error() string {
	return fmt.Sprintf("policy %s: %s", e.Policy, e.Reason)
}

func Denied(policy, reason string) error {
	return &ErrPolicyDenied{Policy: policy, Reason: reason}
}
