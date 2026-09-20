// Package prompt compone el system prompt del agente por secciones
// ordenadas (ver section.go/sections.go), en vez de un string fijo con ifs.
package prompt

import (
	"bytes"
	_ "embed"
	"sort"
	"strings"
	"text/template"
	"time"
)

//go:embed templates/system.tmpl
var tmplSystem string

//go:embed templates/personality.tmpl
var tmplPersonality string

//go:embed templates/rules.tmpl
var tmplRules string

//go:embed templates/policies.tmpl
var tmplPolicies string

// businessTZ: zona horaria fija del negocio para la sección `temporal`
// (evita que el modelo agende horas ya pasadas). Bendey la trae fija a
// Perú (UTC-5); Mistiq opera igual hoy — si en el futuro hace falta por
// tenant, este es el punto a extender.
var businessTZ = time.FixedZone("America/Lima", -5*60*60)

func nowInBusinessTZ() string {
	return time.Now().In(businessTZ).Format("Monday 2 January 2006, 15:04")
}

func execTemplate(tmpl string, in Input) string {
	t, err := template.New("section").Parse(tmpl)
	if err != nil {
		return tmpl // fallback: texto crudo si la plantilla no parsea
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, in); err != nil {
		return tmpl
	}
	return buf.String()
}

// Composer arma el system prompt final concatenando secciones no vacías.
type Composer struct {
	sections []Section
}

// NewComposer usa el set de secciones por defecto.
func NewComposer() *Composer {
	return NewComposerWith(nil)
}

// NewComposerWith permite a un tipo de agente añadir secciones propias sin
// poder eliminar las protegidas: si `extra` no declara una sección
// Protected del set por defecto, se re-inyecta automáticamente.
func NewComposerWith(extra []Section) *Composer {
	byName := make(map[string]Section)
	for _, s := range DefaultSections() {
		byName[s.Name] = s
	}
	for _, s := range extra {
		if existing, ok := byName[s.Name]; ok && existing.Protected && !s.Protected {
			// no se puede "desproteger" una sección por defecto
			continue
		}
		byName[s.Name] = s
	}
	merged := make([]Section, 0, len(byName))
	for _, s := range byName {
		merged = append(merged, s)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Order < merged[j].Order })
	return &Composer{sections: merged}
}

// BuildSystem renderiza todas las secciones no vacías y las concatena con
// doble salto de línea, sin dejar separadores sueltos por secciones vacías.
func (c *Composer) BuildSystem(in Input) string {
	parts := make([]string, 0, len(c.sections))
	for _, s := range c.sections {
		body := strings.TrimSpace(s.Render(in))
		if body == "" {
			continue
		}
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n\n")
}
