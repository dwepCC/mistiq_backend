package reportgate

import (
	"sync"
	"testing"
	"time"
)

func TestGateLimitsConcurrencyPerKey(t *testing.T) {
	g := New(2)
	r1, ok1 := g.Acquire("a", 50*time.Millisecond)
	r2, ok2 := g.Acquire("a", 50*time.Millisecond)
	if !ok1 || !ok2 {
		t.Fatal("los dos primeros turnos deben concederse")
	}
	if _, ok := g.Acquire("a", 30*time.Millisecond); ok {
		t.Fatal("el tercer turno de la misma clave debe agotar la espera")
	}
	// Otra clave (otro tenant) no se ve afectada.
	rb, okb := g.Acquire("b", 30*time.Millisecond)
	if !okb {
		t.Fatal("otro tenant no debe bloquearse")
	}
	rb()

	r1()
	r1() // idempotente: no debe liberar dos veces
	if _, ok := g.Acquire("a", 200*time.Millisecond); !ok {
		t.Fatal("tras liberar debe haber un turno libre")
	}
	r2()
}

func TestGateWaitersAreServedWhenReleased(t *testing.T) {
	g := New(1)
	rel, _ := g.Acquire("t", time.Second)
	var wg sync.WaitGroup
	got := make(chan bool, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		r, ok := g.Acquire("t", time.Second)
		got <- ok
		r()
	}()
	time.Sleep(50 * time.Millisecond)
	rel()
	wg.Wait()
	if !<-got {
		t.Fatal("el que espera debe obtener el turno al liberarse")
	}
}
