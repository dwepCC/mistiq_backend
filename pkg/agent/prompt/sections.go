package prompt

import (
	"fmt"
	"strings"
)

// DefaultSections es el orden real del prompt: de más estable/cacheable
// (identity) a más volátil (knowledge, distinto en cada turno). Ver
// docs/CHATBOT-AGENT-ARCHITECTURE.md §2.2.
func DefaultSections() []Section {
	return []Section{
		{Name: "identity", Order: 10, Render: renderIdentity},
		{Name: "personality", Order: 20, Render: renderPersonality},
		{Name: "rules", Order: 30, Protected: true, Render: renderRules},
		{Name: "policies", Order: 40, Protected: true, Render: renderPolicies},
		{Name: "temporal", Order: 50, Render: renderTemporal},
		{Name: "first_turn", Order: 60, Render: renderFirstTurn},
		{Name: "outreach", Order: 70, Render: renderOutreach},
		{Name: "knowledge", Order: 80, Render: renderKnowledge},
	}
}

func renderIdentity(in Input) string {
	if strings.TrimSpace(in.SystemOverride) != "" {
		return in.SystemOverride
	}
	return execTemplate(tmplSystem, in)
}

func renderPersonality(in Input) string {
	if strings.TrimSpace(in.PersonalityOverride) != "" {
		return in.PersonalityOverride
	}
	return execTemplate(tmplPersonality, in)
}

func renderRules(in Input) string {
	return execTemplate(tmplRules, in)
}

func renderPolicies(in Input) string {
	return execTemplate(tmplPolicies, in)
}

func renderTemporal(in Input) string {
	return fmt.Sprintf("Fecha y hora actual: %s.", nowInBusinessTZ())
}

func renderFirstTurn(in Input) string {
	if !in.IsFirstTurn {
		return ""
	}
	return "Es el primer mensaje real de esta conversación (no un saludo simple): en una línea, como parte natural de tu respuesta (sin que suene a plantilla), deja claro que te llamas Misti y que eres un asistente de inteligencia artificial — es obligatorio decirlo en esta primera interacción, no opcional ni solo si preguntan."
}

func renderOutreach(in Input) string {
	if strings.TrimSpace(in.OutreachGoal) == "" {
		return ""
	}
	return "Esta conversación la iniciaste tú (mensaje saliente de seguimiento). Objetivo de este contacto: " + in.OutreachGoal
}

func renderKnowledge(in Input) string {
	if len(in.KnowledgeSnippets) == 0 {
		return "No tienes fragmentos de la base de conocimiento para esta consulta. No inventes datos: si no tienes la información, dilo y ofrece derivar a un asesor humano."
	}
	return "Información de referencia (úsala para responder, no la copies literal si no encaja con la pregunta):\n" + strings.Join(in.KnowledgeSnippets, "\n---\n")
}
