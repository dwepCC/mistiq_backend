package utils

// ThemeData contiene las paletas de color de un tema para el panel del tenant.
// Sobrescribe la paleta "blue" de Tailwind CDN vía tailwind.config dinámico.
type ThemeData struct {
	Key       string
	Label     string
	Preview   string // hex para el swatch visual (600)
	SidebarBg string // color oscuro del sidebar
	NavActive string // bg del ítem activo en el sidebar
	C50       string
	C100      string
	C200      string
	C300      string
	C400      string
	C500      string
	C600      string // color primario principal
	C700      string // hover
	C800      string
	C900      string
	C950      string
}

var themes = map[string]ThemeData{
	"green": {
		Key: "green", Label: "Verde corporativo", Preview: "#16a34a",
		SidebarBg: "#14532d", NavActive: "#16a34a",
		C50: "#f0fdf4", C100: "#dcfce7", C200: "#bbf7d0",
		C300: "#86efac", C400: "#4ade80", C500: "#22c55e",
		C600: "#16a34a", C700: "#15803d", C800: "#166534",
		C900: "#14532d", C950: "#052e16",
	},
	"blue": {
		Key: "blue", Label: "Azul corporativo", Preview: "#2563eb",
		SidebarBg: "#172554", NavActive: "#2563eb",
		C50: "#eff6ff", C100: "#dbeafe", C200: "#bfdbfe",
		C300: "#93c5fd", C400: "#60a5fa", C500: "#3b82f6",
		C600: "#2563eb", C700: "#1d4ed8", C800: "#1e40af",
		C900: "#1e3a8a", C950: "#172554",
	},
	"violet": {
		Key: "violet", Label: "Violeta", Preview: "#7c3aed",
		SidebarBg: "#2e1065", NavActive: "#7c3aed",
		C50: "#f5f3ff", C100: "#ede9fe", C200: "#ddd6fe",
		C300: "#c4b5fd", C400: "#a78bfa", C500: "#8b5cf6",
		C600: "#7c3aed", C700: "#6d28d9", C800: "#5b21b6",
		C900: "#4c1d95", C950: "#2e1065",
	},
	"emerald": {
		Key: "emerald", Label: "Verde esmeralda", Preview: "#059669",
		SidebarBg: "#022c22", NavActive: "#059669",
		C50: "#ecfdf5", C100: "#d1fae5", C200: "#a7f3d0",
		C300: "#6ee7b7", C400: "#34d399", C500: "#10b981",
		C600: "#059669", C700: "#047857", C800: "#065f46",
		C900: "#064e3b", C950: "#022c22",
	},
	"rose": {
		Key: "rose", Label: "Rosa / Rojo", Preview: "#e11d48",
		SidebarBg: "#4c0519", NavActive: "#e11d48",
		C50: "#fff1f2", C100: "#ffe4e6", C200: "#fecdd3",
		C300: "#fda4af", C400: "#fb7185", C500: "#f43f5e",
		C600: "#e11d48", C700: "#be123c", C800: "#9f1239",
		C900: "#881337", C950: "#4c0519",
	},
	"orange": {
		Key: "orange", Label: "Naranja", Preview: "#ea580c",
		SidebarBg: "#431407", NavActive: "#ea580c",
		C50: "#fff7ed", C100: "#ffedd5", C200: "#fed7aa",
		C300: "#fdba74", C400: "#fb923c", C500: "#f97316",
		C600: "#ea580c", C700: "#c2410c", C800: "#9a3412",
		C900: "#7c2d12", C950: "#431407",
	},
	"teal": {
		Key: "teal", Label: "Verde azulado", Preview: "#0d9488",
		SidebarBg: "#042f2e", NavActive: "#0d9488",
		C50: "#f0fdfa", C100: "#ccfbf1", C200: "#99f6e4",
		C300: "#5eead4", C400: "#2dd4bf", C500: "#14b8a6",
		C600: "#0d9488", C700: "#0f766e", C800: "#115e59",
		C900: "#134e4a", C950: "#042f2e",
	},
	"sky": {
		Key: "sky", Label: "Cielo", Preview: "#0284c7",
		SidebarBg: "#082f49", NavActive: "#0284c7",
		C50: "#f0f9ff", C100: "#e0f2fe", C200: "#bae6fd",
		C300: "#7dd3fc", C400: "#38bdf8", C500: "#0ea5e9",
		C600: "#0284c7", C700: "#0369a1", C800: "#075985",
		C900: "#0c4a6e", C950: "#082f49",
	},
	"slate": {
		Key: "slate", Label: "Gris pizarra", Preview: "#475569",
		SidebarBg: "#0f172a", NavActive: "#475569",
		C50: "#f8fafc", C100: "#f1f5f9", C200: "#e2e8f0",
		C300: "#cbd5e1", C400: "#94a3b8", C500: "#64748b",
		C600: "#475569", C700: "#334155", C800: "#1e293b",
		C900: "#0f172a", C950: "#020617",
	},
}

// GetTheme retorna el tema por clave; si no existe, retorna verde corporativo por defecto.
func GetTheme(key string) ThemeData {
	if t, ok := themes[key]; ok {
		return t
	}
	return themes["green"]
}

// AllThemes retorna todos los temas disponibles en orden para la UI.
func AllThemes() []ThemeData {
	keys := []string{"green", "blue", "violet", "emerald", "rose", "orange", "teal", "sky", "slate"}
	result := make([]ThemeData, 0, len(keys))
	for _, k := range keys {
		result = append(result, themes[k])
	}
	return result
}
