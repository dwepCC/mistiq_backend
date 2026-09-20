// Package actions es el catálogo de acciones comerciales concretas del
// agente de Mistiq (vender el propio SaaS — ver
// docs/CHATBOT-AGENT-ARCHITECTURE.md §5-6): crear/calificar leads, agendar
// demos, validar RUC, crear tenants de prueba/pagados, gestionar el pago
// manual. Implementa el framework de pkg/agent/actions (interfaz Action).
package actions

import (
	"encoding/json"
	"fmt"
	"strings"

	"tukifac/pkg/agent/executor"
)

// decodeArgs deserializa los argumentos de una tool call a `dst`, con un
// error Validation (nunca Fatal) si el JSON viene roto — así el modelo
// puede corregir su propia llamada.
func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return executor.Validation("faltan los argumentos de la acción", nil)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return executor.Validation(fmt.Sprintf("argumentos inválidos: %s", err.Error()), err)
	}
	return nil
}

func requireNonEmpty(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return executor.Validation(fmt.Sprintf("falta el campo %q", field), nil)
	}
	return nil
}
