package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V200UbigeoDistritosUpdate agrega a cada tenant los distritos del catálogo INEI que se crearon después de la carga inicial (17 distritos,
// entre ellos San Antonio — Mariscal Nieto, Moquegua, ubigeo 180107, Ley 31216). Sin esto, un contribuyente con domicilio fiscal en un
// distrito nuevo no podía configurarlo ni emitir: «ubigeo … no encontrado en catálogo». Solo inserta lo que falta; no toca los existentes.
type V200UbigeoDistritosUpdate struct{}

func (V200UbigeoDistritosUpdate) Version() int { return 200 }
func (V200UbigeoDistritosUpdate) Name() string { return "ubigeo_distritos_update" }

func (V200UbigeoDistritosUpdate) Up(db *gorm.DB) error {
	if err := database.SeedUbigeoRegionesProvincias(db); err != nil {
		return err
	}
	if err := database.UpsertUbigeoRegionesProvincias(db); err != nil { // p. ej. la provincia Calca (080400) faltaba
		return err
	}
	_, err := database.UpsertUbigeoDistritos(db)
	return err
}
