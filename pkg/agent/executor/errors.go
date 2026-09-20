package executor

// Class clasifica un error de acción para decidir qué mensaje llega al
// modelo y si conviene reintentar. Pieza central de seguridad: para
// ClassFatal/ClassTransient NUNCA se deja pasar el texto real del error al
// modelo (fuga de detalles técnicos internos); solo ClassValidation/
// ClassBusiness exponen su texto porque lo escribió quien programó la
// acción, para consumo humano/del modelo.
type Class int

const (
	// ClassFatal es el default: error inesperado, mensaje genérico fijo,
	// nunca se reintenta.
	ClassFatal Class = iota
	// ClassValidation: argumentos inválidos — el modelo puede corregirlos
	// y reintentar la tool call por su cuenta. Mensaje real expuesto.
	ClassValidation
	// ClassBusiness: regla de negocio real (p. ej. "el RUC ya tiene
	// cuenta") — mensaje real expuesto, no se reintenta.
	ClassBusiness
	// ClassTransient: error de infraestructura (timeout, red) — se
	// reintenta automáticamente SOLO si la acción es además Idempotent.
	// Mensaje genérico, nunca el real.
	ClassTransient
)

// ClassifiedError envuelve un error de acción con su clasificación.
type ClassifiedError struct {
	Class   Class
	Message string
	Cause   error
}

func (e *ClassifiedError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *ClassifiedError) Unwrap() error { return e.Cause }

func Validation(msg string, cause error) error {
	return &ClassifiedError{Class: ClassValidation, Message: msg, Cause: cause}
}

func Business(msg string, cause error) error {
	return &ClassifiedError{Class: ClassBusiness, Message: msg, Cause: cause}
}

func Transient(msg string, cause error) error {
	return &ClassifiedError{Class: ClassTransient, Message: msg, Cause: cause}
}

func Fatal(msg string, cause error) error {
	return &ClassifiedError{Class: ClassFatal, Message: msg, Cause: cause}
}

func classify(err error) *ClassifiedError {
	if err == nil {
		return nil
	}
	var ce *ClassifiedError
	if errorsAs(err, &ce) {
		return ce
	}
	return &ClassifiedError{Class: ClassFatal, Message: "error inesperado", Cause: err}
}

// errorsAs es un wrapper trivial de errors.As para evitar el import extra
// en cada archivo que llama classify.
func errorsAs(err error, target **ClassifiedError) bool {
	for err != nil {
		if ce, ok := err.(*ClassifiedError); ok {
			*target = ce
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// Sanitize decide qué mensaje puede reinyectarse al modelo como resultado
// de una tool call fallida — la regla dura descrita arriba.
func Sanitize(err error) string {
	ce := classify(err)
	switch ce.Class {
	case ClassValidation, ClassBusiness:
		return ce.Message
	default: // ClassFatal, ClassTransient
		return "Hubo un inconveniente ejecutando esta acción. Intenta de otra forma o deriva a un asesor humano."
	}
}

// IsTransient indica si el error clasificado habilita reintento (además de
// requerir que la acción sea Idempotent — ver executor.go).
func IsTransient(err error) bool {
	return classify(err).Class == ClassTransient
}
