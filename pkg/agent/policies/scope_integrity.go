package policies

import (
	"context"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
)

// ScopeIntegrity rechaza ejecutar una acción si el bizctx.Context no es
// válido (scope vacío/desconocido, o sin AssistantID) — defensa contra un
// contexto mal construido que mezclaría datos de plataforma con datos de
// un scope que no le corresponde.
type ScopeIntegrity struct{}

var _ Policy = ScopeIntegrity{}

func (ScopeIntegrity) Check(ctx context.Context, bc bizctx.Context, action actions.Action, args []byte) error {
	if !bc.Valid() {
		return Denied("scope_integrity", "contexto de negocio inválido")
	}
	return nil
}
