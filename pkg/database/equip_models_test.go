package database

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// La columna movement_type debe caber el tipo más largo: «transferencia_ingreso» (21) rompía con size:20 en MySQL estricto
// (SQLite de los tests no valida el largo, por eso no se detectó).
func TestEquipStockMovementTypeColumnFitsEveryType(t *testing.T) {
	f, _ := reflect.TypeOf(EquipStockMovement{}).FieldByName("MovementType")
	size := 0
	for _, part := range strings.Split(f.Tag.Get("gorm"), ";") {
		if strings.HasPrefix(part, "size:") {
			size, _ = strconv.Atoi(strings.TrimPrefix(part, "size:"))
		}
	}
	for _, v := range []string{EquipMoveApertura, EquipMoveIngreso, EquipMoveSalida, EquipMoveReenvio, EquipMoveReingreso, EquipMoveAjuste, EquipMoveBaja, EquipMoveTransferOut, EquipMoveTransferIn} {
		if len(v) > size {
			t.Errorf("movement_type %q (%d) no cabe en size:%d", v, len(v), size)
		}
	}
}
