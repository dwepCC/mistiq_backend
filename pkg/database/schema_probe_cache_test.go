package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestCachedSchemaProbe_SoloCacheaPositivos(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	// Negativo: no se cachea, cada llamada vuelve a sondear (el tenant puede migrar después).
	for i := 0; i < 3; i++ {
		if CachedSchemaProbe(db, "col", func() bool { calls++; return false }) {
			t.Fatal("debe devolver false")
		}
	}
	if calls != 3 {
		t.Fatalf("los negativos no se cachean: calls=%d", calls)
	}

	// Positivo: se sondea una vez y luego se responde desde caché.
	calls = 0
	for i := 0; i < 5; i++ {
		if !CachedSchemaProbe(db, "col", func() bool { calls++; return true }) {
			t.Fatal("debe devolver true")
		}
	}
	if calls != 1 {
		t.Fatalf("el positivo debe sondearse una sola vez: calls=%d", calls)
	}

	// Otro pool (otro tenant) no comparte el caché; otra clave tampoco.
	db2, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_2?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	calls = 0
	CachedSchemaProbe(db2, "col", func() bool { calls++; return true })
	CachedSchemaProbe(db, "otra", func() bool { calls++; return true })
	if calls != 2 {
		t.Fatalf("el caché es por pool y por clave: calls=%d", calls)
	}

	// El caché sobrevive a una sesión derivada (WithContext) del mismo pool.
	calls = 0
	if !CachedSchemaProbe(db.WithContext(context.Background()), "col", func() bool { calls++; return false }) || calls != 0 {
		t.Fatalf("una sesión del mismo pool debe leer el caché (calls=%d)", calls)
	}
}
