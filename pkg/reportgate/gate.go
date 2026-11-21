// Package reportgate limita cuántos reportes pesados corren a la vez por tenant.
//
// Cada tenant tiene un pool de pocas conexiones MySQL (DB_TENANT_MAX_OPEN, 3 en producción) que
// comparten el login, el middleware de sesión y todo el resto de requests. Un listado/reporte
// ejecuta varias sentencias seguidas, así que unos pocos reportes simultáneos (varias pestañas,
// cada tecla del buscador, reintentos) se quedan con todas las conexiones y el tenant entero deja de
// responder, incluido el login (incidente 2026-10-09, gylconcesioneseirl). Aquí los reportes
// esperan turno un tiempo acotado y, si no llega, se rechazan con un error claro.
package reportgate

import (
	"sync"
	"time"
)

// Gate semáforo por clave (slug del tenant).
type Gate struct {
	mu   sync.Mutex
	sems map[string]chan struct{}
	max  int
}

// New crea un Gate que permite `max` reportes simultáneos por clave.
func New(max int) *Gate {
	if max < 1 {
		max = 1
	}
	return &Gate{sems: make(map[string]chan struct{}), max: max}
}

func (g *Gate) sem(key string) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	ch, ok := g.sems[key]
	if !ok {
		ch = make(chan struct{}, g.max)
		g.sems[key] = ch
	}
	return ch
}

// Acquire espera hasta `wait` por un turno. Devuelve la función de liberación (idempotente) y true
// si lo obtuvo; false si se agotó la espera.
func (g *Gate) Acquire(key string, wait time.Duration) (release func(), ok bool) {
	ch := g.sem(key)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case ch <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-ch }) }, true
	case <-timer.C:
		return func() {}, false
	}
}
