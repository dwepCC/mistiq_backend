package policies

import (
	"context"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
)

// Toolset es defensa en profundidad: rechaza cualquier acción fuera del
// catálogo permitido para este agente, aunque el modelo la haya "alucinado"
// (pedido un nombre que no estaba en la lista de tools que se le mandó).
type Toolset struct {
	Allowed map[string]bool
}

var _ Policy = Toolset{}

func NewToolset(names []string) Toolset {
	allowed := make(map[string]bool, len(names))
	for _, n := range names {
		allowed[n] = true
	}
	return Toolset{Allowed: allowed}
}

func (t Toolset) Check(ctx context.Context, bc bizctx.Context, action actions.Action, args []byte) error {
	// Toolset vacío = sin restricción adicional (ver agent.Definition.Toolset).
	if len(t.Allowed) == 0 {
		return nil
	}
	if !t.Allowed[action.Name()] {
		return Denied("toolset", "herramienta \""+action.Name()+"\" no está en el catálogo permitido de este agente")
	}
	return nil
}
