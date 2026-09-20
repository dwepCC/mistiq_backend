package prompt

// Input son los datos disponibles para renderizar una sección.
type Input struct {
	Locale              string
	SystemOverride      string
	PersonalityOverride string
	IsFirstTurn         bool   // primer mensaje real (no un saludo desnudo)
	OutreachGoal        string // no vacío si la conversación la inició la empresa
	KnowledgeSnippets   []string
}

// Section es una pieza del system prompt, con un Order que determina el
// lugar en la concatenación final (de más estable/cacheable a más volátil).
// Protected=true: se re-inyecta aunque un Composer a medida no la declare
// (pkg/agent/prompt/prompt.go).
type Section struct {
	Name      string
	Order     int
	Protected bool
	Render    func(Input) string
}
