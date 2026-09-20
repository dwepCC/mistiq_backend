package policies

import (
	"context"
	"strings"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/agent/memory"
)

const defaultConfirmationWindow = 12

// confirmationMarker es lo que una acción Sensitive debe dejar en su propio
// Result.Message (o el modelo repetir en su respuesta previa) para que esta
// política reconozca que YA se pidió confirmación para esa herramienta.
const confirmationMarker = "[confirmación-requerida]"

// affirmativeWords: lista CERRADA de respuestas afirmativas válidas. Un
// mensaje de más de 5 palabras, o que no matchee ninguna de estas, no
// cuenta como confirmación — hace a la política resistente a que alguien
// intente colar una confirmación dentro de una frase más larga (inyección).
var affirmativeWords = map[string]bool{
	"si": true, "sí": true, "sip": true, "simon": true, "claro": true,
	"dale": true, "ok": true, "okay": true, "okey": true, "vale": true,
	"correcto": true, "exacto": true, "afirmativo": true, "confirmo": true,
	"confirmado": true, "procede": true, "adelante": true, "hazlo": true,
	"acepto": true, "de acuerdo": true, "está bien": true, "esta bien": true,
	"perfecto": true, "listo": true, "ya": true, "va": true, "asi es": true,
	"así es": true,
}

// Confirmation exige confirmación explícita, verificada contra el
// historial PERSISTIDO (no contra lo que "dice" el prompt), para acciones
// marcadas Sensitive.
type Confirmation struct {
	Turns  memory.Memory
	Window int
}

var _ Policy = Confirmation{}

func NewConfirmation(turns memory.Memory) Confirmation {
	return Confirmation{Turns: turns, Window: defaultConfirmationWindow}
}

func (c Confirmation) Check(ctx context.Context, bc bizctx.Context, action actions.Action, args []byte) error {
	if !action.Meta().Sensitive {
		return nil
	}
	window := c.Window
	if window <= 0 {
		window = defaultConfirmationWindow
	}
	history, err := c.Turns.LoadContext(ctx, bc.ConversationID, window)
	if err != nil {
		return Denied("confirmation", "no se pudo verificar el historial de confirmación")
	}

	// Buscar el turno `tool` MÁS RECIENTE que marcó confirmación pendiente
	// para esta herramienta exacta.
	markerIdx := -1
	for i := len(history) - 1; i >= 0; i-- {
		t := history[i]
		if t.Role == memory.RoleTool && t.ToolName == action.Name() && strings.Contains(t.Content, confirmationMarker) {
			markerIdx = i
			break
		}
	}
	if markerIdx == -1 {
		return Denied("confirmation", "esta acción requiere confirmación explícita del cliente antes de ejecutarse")
	}

	// Solo el/los turnos de usuario INMEDIATAMENTE posteriores al marcador
	// cuentan como respuesta a esa confirmación.
	for i := markerIdx + 1; i < len(history); i++ {
		t := history[i]
		if t.Role != memory.RoleUser {
			continue
		}
		if isAffirmative(t.Content) {
			return nil
		}
		// La primera respuesta de usuario que no sea afirmativa invalida
		// la confirmación pendiente — hay que repetirla.
		return Denied("confirmation", "la última respuesta del cliente no confirmó la acción; hay que volver a pedir confirmación")
	}
	return Denied("confirmation", "todavía no hay respuesta del cliente a la confirmación pedida")
}

func isAffirmative(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	words := strings.Fields(text)
	if len(words) > 5 {
		return false
	}
	return affirmativeWords[text]
}
