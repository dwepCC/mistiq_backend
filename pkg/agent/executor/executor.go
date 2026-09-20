// Package executor ejecuta tool calls con el pipeline invariante del motor
// (catálogo → políticas previas → ejecución con timeout/recover/reintento →
// políticas de resultado → truncado) y la estrategia ReAct que orquesta
// llamadas al LLM intercaladas con esas ejecuciones.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/agent/policies"
	"tukifac/pkg/logger"

	"log/slog"
)

const (
	defaultTimeout   = 20 * time.Second
	maxRetries       = 2
	maxResultChars   = 4000
	retryBaseBackoff = 200 * time.Millisecond
)

// Executor corre el pipeline invariante para una tool call.
type Executor struct {
	Registry       *actions.Registry
	Policies       []policies.Policy
	ResultPolicies []policies.ResultPolicy
}

func New(registry *actions.Registry, pol []policies.Policy, resultPol []policies.ResultPolicy) *Executor {
	return &Executor{Registry: registry, Policies: pol, ResultPolicies: resultPol}
}

// Run ejecuta una única tool call por su nombre y argumentos crudos.
// Nunca hace panic hacia arriba (recupera cualquier pánico de la acción) y
// siempre devuelve un mensaje seguro para reinyectar al modelo (ver
// errors.go:Sanitize).
func (e *Executor) Run(ctx context.Context, bc bizctx.Context, name string, args json.RawMessage) string {
	action, ok := e.Registry.Get(name)
	if !ok {
		return fmt.Sprintf("Herramienta %q no existe.", name)
	}

	for _, p := range e.Policies {
		if err := p.Check(ctx, bc, action, args); err != nil {
			return Sanitize(err)
		}
	}

	result, err := e.callWithRetry(ctx, bc, action, args)
	if err != nil {
		logger.L.Warn("agent_action_failed",
			slog.String("action", name),
			slog.String("error", err.Error()),
		)
		return Sanitize(err)
	}

	for _, rp := range e.ResultPolicies {
		if err := rp.Inspect(ctx, bc, action, result); err != nil {
			return Sanitize(err)
		}
	}

	return truncate(result.Message, maxResultChars)
}

func (e *Executor) callWithRetry(ctx context.Context, bc bizctx.Context, action actions.Action, args json.RawMessage) (result actions.Result, err error) {
	idempotent := action.Meta().Idempotent
	attempts := 1
	if idempotent {
		attempts = maxRetries + 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		result, err = e.callOnce(ctx, bc, action, args)
		if err == nil {
			return result, nil
		}
		if !idempotent || !IsTransient(err) {
			return actions.Result{}, err
		}
		if attempt < attempts-1 {
			select {
			case <-ctx.Done():
				return actions.Result{}, ctx.Err()
			case <-time.After(retryBaseBackoff * time.Duration(attempt+1)):
			}
		}
	}
	return actions.Result{}, err
}

func (e *Executor) callOnce(ctx context.Context, bc bizctx.Context, action actions.Action, args json.RawMessage) (result actions.Result, err error) {
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				err = Fatal(fmt.Sprintf("pánico en acción %q", action.Name()), fmt.Errorf("%v", r))
			}
			close(done)
		}()
		result, err = action.Execute(callCtx, bc, args)
	}()

	select {
	case <-done:
		return result, err
	case <-callCtx.Done():
		return actions.Result{}, Transient("timeout ejecutando la acción", callCtx.Err())
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
