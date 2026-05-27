package docseries

import "errors"

var (
	// ErrSeriesNotFound la serie no existe o fue eliminada.
	ErrSeriesNotFound = errors.New("la serie seleccionada no existe o fue eliminada")
	// ErrSeriesInactive la serie está desactivada.
	ErrSeriesInactive = errors.New("la serie seleccionada está inactiva")
	// ErrSeriesWrongBranch la serie pertenece a otra sucursal.
	ErrSeriesWrongBranch = errors.New("la serie seleccionada no pertenece a la sucursal actual")
	// ErrSeriesDuplicate el código de serie ya existe en el tenant (regla SUNAT: serie única global).
	ErrSeriesDuplicate = errors.New("la serie ya está registrada en el sistema; cada punto de emisión debe usar un código de serie distinto")
)
