package prepayment

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// Códigos catálogo 51 SUNAT relevantes para emisión de anticipos.
const (
	OpVentaInterna          = "0101"
	OpVentaInternaAnticipos = "0104"
)

// defaultEmitOperationType es el único lugar donde se define el tipoOperacion por defecto
// para comprobantes de anticipo. Cambiar aquí (o vía PREPAYMENT_EMIT_OPERATION_TYPE) alterna
// entre 0101 (legacy PHP + SUNAT Beta) y 0104 (cat. 51 normativo) sin tocar el resto del sistema.
const defaultEmitOperationType = OpVentaInterna

var (
	emitOperationMu     sync.RWMutex
	emitOperationType   = resolveEmitOperationTypeFromEnv()
	emitOperationLabels = map[string]string{
		OpVentaInterna:          "Venta interna",
		OpVentaInternaAnticipos: "Venta Interna – Anticipos",
	}
)

func resolveEmitOperationTypeFromEnv() string {
	raw := strings.TrimSpace(os.Getenv("PREPAYMENT_EMIT_OPERATION_TYPE"))
	if raw == "" {
		return defaultEmitOperationType
	}
	code, err := NormalizeEmitOperationType(raw)
	if err != nil {
		return defaultEmitOperationType
	}
	return code
}

// EmitOperationTypeCode devuelve el tipoOperacion configurado para emisión de anticipos.
func EmitOperationTypeCode() string {
	emitOperationMu.RLock()
	defer emitOperationMu.RUnlock()
	return emitOperationType
}

// SetEmitOperationTypeForTest permite fijar el código en tests (no usar en producción).
func SetEmitOperationTypeForTest(code string) {
	emitOperationMu.Lock()
	defer emitOperationMu.Unlock()
	if c, err := NormalizeEmitOperationType(code); err == nil {
		emitOperationType = c
	}
}

// NormalizeEmitOperationType valida códigos permitidos para emisión de anticipo.
func NormalizeEmitOperationType(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	switch code {
	case OpVentaInterna, OpVentaInternaAnticipos:
		return code, nil
	default:
		return "", fmt.Errorf("tipo de operación de anticipo no válido: %s (use %s o %s)", code, OpVentaInterna, OpVentaInternaAnticipos)
	}
}

// IsEmitOperationType indica si el código corresponde a una emisión de anticipo configurada.
func IsEmitOperationType(code string) bool {
	c := strings.TrimSpace(code)
	return c == EmitOperationTypeCode()
}

// IsAllowedEmitOperationType indica si el código es uno de los valores soportados por el módulo.
func IsAllowedEmitOperationType(code string) bool {
	c := strings.TrimSpace(code)
	return c == OpVentaInterna || c == OpVentaInternaAnticipos
}

// EmitOperationLabel descripción corta del tipoOperacion configurado.
func EmitOperationLabel() string {
	return operationLabel(EmitOperationTypeCode())
}

// EmitOperationFullLabel etiqueta completa para UI/PDF (código + descripción).
func EmitOperationFullLabel() string {
	code := EmitOperationTypeCode()
	return fmt.Sprintf("%s (%s)", operationLabel(code), code)
}

func operationLabel(code string) string {
	if l, ok := emitOperationLabels[code]; ok {
		return l
	}
	return code
}
