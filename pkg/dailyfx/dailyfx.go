// Package dailyfx: punto de acceso al tipo de cambio guardado de cada día, desacoplado del módulo exchangerate (que
// lo registra en la BD central) para que las ventas, cotizaciones, compras y órdenes completen el TC de sus documentos
// sin importar la capa de red/BD central, y para poder sustituirlo en las pruebas.
package dailyfx

// Lookup devuelve el TC venta del día (yyyy-mm-dd, hora de Lima). estimated=true si el valor no es el publicado ese día
// sino el último disponible (SUNAT aún no lo publica o es fin de semana/feriado). ok=false si no hay ninguno.
// El módulo exchangerate lo asigna al iniciar; nil = no configurado (pruebas): los servicios conservan su
// comportamiento anterior.
var Lookup func(day string) (rate float64, estimated bool, ok bool)

// SaleRate envuelve Lookup; ok=false si no está configurado o no hay TC.
func SaleRate(day string) (rate float64, estimated bool, ok bool) {
	if Lookup == nil {
		return 0, false, false
	}
	return Lookup(day)
}

// Configured indica si hay un proveedor de TC diario conectado.
func Configured() bool { return Lookup != nil }
