// Package branchscope es el "Global Scope" de sucursal para GORM: callbacks que, sobre las entidades registradas
// (las que tienen branch_id propio), restringen consultas, actualizaciones y borrados a las sucursales a las que el
// usuario tiene acceso, validan las altas y bloquean toda escritura en modo "Todas las sucursales".
//
// Cómo funciona
//   - El middleware de sucursal construye un Scope por solicitud y reemplaza el tenantDB de la solicitud por
//     db.WithContext(WithScope(ctx, scope)). Todo handler/servicio que use ese *gorm.DB (incluidas transacciones,
//     Preload y subconsultas encadenadas) queda acotado sin tocar sus consultas.
//   - Sin Scope en el contexto la consulta NO queda acotada (cron, workers, webhooks, migraciones, login): eso es
//     acceso interno y debe declararse con System(db, motivo). Mientras el modo estricto esté apagado, el acceso a una
//     entidad registrada sin Scope ni marca System se registra en el log (auditoría, una vez por tabla+origen); con
//     BRANCH_SCOPE_STRICT=1 falla con ErrScopeMissing.
//   - BRANCH_SCOPE_DISABLED=1 apaga los callbacks (válvula de emergencia; requiere reiniciar).
//
// Límites conocidos (ver docs/aislamiento-sucursal/FASE_3C.md): Raw()/Exec() con SQL manual y los JOIN a una tabla
// registrada no pasan por los callbacks; las entidades sin branch_id propio se protegen por la entidad padre.
package branchscope

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrScopeMissing = errors.New("branchscope: consulta sin contexto de sucursal sobre una entidad con sucursal")
	ErrReadOnly     = errors.New("branchscope: 'Todas las sucursales' es de solo consulta")
	ErrForbidden    = errors.New("branchscope: sucursal no permitida para este usuario")
)

// Scope describe a qué sucursales puede acceder la solicitud.
type Scope struct {
	UserID           uint
	ActiveBranchID   uint
	AllowedBranchIDs []uint
	ViewAll          bool // branches.view_all: no se filtra por sucursal
	ReadOnly         bool // modo ALL_BRANCHES: ninguna escritura
}

type ctxKey int

const (
	scopeKey ctxKey = iota
	systemKey
)

// settingScope/settingSystem: copia del Scope y de la marca System en los Settings del *gorm.DB. Muchos servicios
// hacen db.WithContext(ctxConTimeout), que reemplaza el contexto y perdería el Scope; los Settings sobreviven a
// WithContext y Session, y el contexto sobrevive a Session{NewDB:true}. Se consultan ambos.
const (
	settingScope  = "branchscope:scope"
	settingSystem = "branchscope:system"
)

// Attach devuelve db acotada por scope (contexto + Settings). Es lo que usa el middleware de sucursal.
func Attach(db *gorm.DB, s *Scope) *gorm.DB {
	// Set deja la instancia "usada"; Session la vuelve a hacer segura para encadenar y compartir.
	return db.WithContext(WithScope(contextOf(db), s)).Set(settingScope, s).Session(&gorm.Session{})
}

// WithScope devuelve un contexto con el Scope de la solicitud.
func WithScope(ctx context.Context, s *Scope) context.Context {
	return context.WithValue(ctx, scopeKey, s)
}

// FromContext obtiene el Scope del contexto (nil si no hay).
func FromContext(ctx context.Context) *Scope {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(scopeKey).(*Scope)
	return s
}

type systemMark struct{ reason string }

var systemSeen sync.Map

// System declara acceso interno explícito (cron, workers, webhooks, procesos del facturador): sin restricción por
// sucursal, identificado por un motivo y registrado en el log la primera vez que se usa. Nunca debe usarse con
// datos de un usuario de solicitud.
func System(db *gorm.DB, reason string) *gorm.DB {
	if _, loaded := systemSeen.LoadOrStore(reason, struct{}{}); !loaded {
		log.Printf("[branchscope] acceso interno sin restricción de sucursal: %s", reason)
	}
	return db.WithContext(context.WithValue(contextOf(db), systemKey, &systemMark{reason: reason})).Set(settingSystem, reason).Session(&gorm.Session{})
}

func contextOf(db *gorm.DB) context.Context {
	if db != nil && db.Statement != nil && db.Statement.Context != nil {
		return db.Statement.Context
	}
	return context.Background()
}

func isSystem(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(systemKey).(*systemMark)
	return ok
}

// ---- registro de entidades ----

var (
	registryMu sync.RWMutex
	registry   = map[string]struct{}{} // nombre de tabla con branch_id propio
)

// Install registra las entidades y los callbacks en db. models son los modelos con columna branch_id propia.
func Install(db *gorm.DB, models ...interface{}) error {
	for _, m := range models {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(m); err != nil {
			return fmt.Errorf("branchscope: modelo %T: %w", m, err)
		}
		if stmt.Schema.LookUpField("BranchID") == nil {
			return fmt.Errorf("branchscope: %T no tiene BranchID", m)
		}
		registryMu.Lock()
		registry[stmt.Schema.Table] = struct{}{}
		registryMu.Unlock()
	}
	cb := db.Callback()
	if err := cb.Query().Before("gorm:query").Register("branchscope:query", readFilter); err != nil {
		return err
	}
	if err := cb.Row().Before("gorm:row").Register("branchscope:row", readFilter); err != nil {
		return err
	}
	if err := cb.Update().Before("gorm:update").Register("branchscope:update", writeFilter); err != nil {
		return err
	}
	if err := cb.Delete().Before("gorm:delete").Register("branchscope:delete", writeFilter); err != nil {
		return err
	}
	return cb.Create().Before("gorm:create").Register("branchscope:create", createCheck)
}

// IsRegistered indica si la tabla tiene scope de sucursal.
func IsRegistered(table string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[table]
	return ok
}

func disabled() bool { return os.Getenv("BRANCH_SCOPE_DISABLED") == "1" }
func strict() bool   { return os.Getenv("BRANCH_SCOPE_STRICT") == "1" }

// resolve devuelve el scope a aplicar; ok=false si no hay que restringir (tabla sin scope, acceso interno, sin contexto).
func resolve(tx *gorm.DB) (s *Scope, ok bool) {
	if disabled() || tx.Statement == nil || tx.Error != nil {
		return nil, false
	}
	table := tx.Statement.Table
	if !IsRegistered(table) {
		// db.Table("tenant_sales ts") / "tenant_sales AS ts": GORM deja Statement.Table = alias; la tabla real está en TableExpr.
		real := realTable(tx.Statement)
		if real == "" || !IsRegistered(real) {
			return nil, false
		}
		table = real
	}
	ctx := tx.Statement.Context
	if _, sys := tx.Get(settingSystem); sys || isSystem(ctx) {
		return nil, false
	}
	s = FromContext(ctx)
	if s == nil {
		if v, found := tx.Get(settingScope); found {
			s, _ = v.(*Scope)
		}
	}
	if s == nil {
		auditUnscoped(table)
		if strict() {
			_ = tx.AddError(ErrScopeMissing)
		}
		return nil, false
	}
	return s, true
}

var tableExprRe = regexp.MustCompile("^\\s*`?(\\w+)`?(?:\\s|$)")

// realTable extrae el nombre real de la tabla de un Table("nombre alias") (TableExpr). "" si no aplica (subconsulta, etc.).
func realTable(st *gorm.Statement) string {
	if st.TableExpr == nil {
		return ""
	}
	m := tableExprRe.FindStringSubmatch(st.TableExpr.SQL)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

var aliasRe = regexp.MustCompile("^\\s*`?\\w+`?(?:\\s+(?:[Aa][Ss]\\s+)?`?(\\w+)`?)?\\s*$")

// qualifier devuelve el calificador de la columna branch_id: el alias o el nombre de la tabla principal de la consulta.
// GORM no siempre rellena Statement.Table con el alias (p. ej. Table("`tenant_sales` s")): se deduce del TableExpr.
func qualifier(st *gorm.Statement) string {
	if st.TableExpr != nil {
		if m := aliasRe.FindStringSubmatch(st.TableExpr.SQL); len(m) == 2 && m[1] != "" {
			return m[1]
		}
		if real := realTable(st); real != "" && IsRegistered(real) && st.Table == "" {
			return real
		}
	}
	return clause.CurrentTable
}

var auditSeen sync.Map

func auditUnscoped(table string) {
	origin := callerOutsideGorm()
	key := table + "|" + origin
	if _, loaded := auditSeen.LoadOrStore(key, struct{}{}); !loaded {
		log.Printf("[branchscope] AUDITORÍA: acceso sin contexto de sucursal a %s desde %s", table, origin)
	}
}

func callerOutsideGorm() string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if strings.Contains(f.File, "tukifac") && !strings.Contains(f.File, "pkg/branchscope") &&
			!strings.Contains(f.Function, "gorm.io") {
			parts := strings.Split(f.File, "/")
			if len(parts) > 3 {
				return fmt.Sprintf("%s:%d", strings.Join(parts[len(parts)-3:], "/"), f.Line)
			}
			return fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		if !more {
			return "desconocido"
		}
	}
}

// whereBranch agrega la restricción de sucursal (los registros sin sucursal —0— son del tenant y se conservan).
func whereBranch(tx *gorm.DB, s *Scope) {
	if s.ViewAll {
		return
	}
	ids := s.AllowedBranchIDs
	if len(ids) == 0 && s.ActiveBranchID > 0 {
		ids = []uint{s.ActiveBranchID}
	}
	if len(ids) == 0 {
		// Sin ninguna sucursal permitida no se devuelve nada (fail-closed), salvo registros sin sucursal.
		ids = []uint{0}
	}
	col := tx.Statement.Quote(clause.Column{Table: qualifier(tx.Statement), Name: "branch_id"})
	tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{
		clause.Expr{SQL: "(" + col + " IN ? OR " + col + " IS NULL OR " + col + " = 0)", Vars: []interface{}{ids}},
	}})
}

func readFilter(tx *gorm.DB) {
	if s, ok := resolve(tx); ok {
		whereBranch(tx, s)
	}
}

func writeFilter(tx *gorm.DB) {
	s, ok := resolve(tx)
	if !ok {
		return
	}
	if s.ReadOnly {
		_ = tx.AddError(ErrReadOnly)
		return
	}
	whereBranch(tx, s)
}

func createCheck(tx *gorm.DB) {
	s, ok := resolve(tx)
	if !ok {
		return
	}
	if s.ReadOnly {
		_ = tx.AddError(ErrReadOnly)
		return
	}
	if tx.Statement.Schema == nil {
		return
	}
	f := tx.Statement.Schema.LookUpField("BranchID")
	if f == nil {
		return
	}
	allowed := func(id uint) bool {
		if s.ViewAll || id == s.ActiveBranchID {
			return true
		}
		for _, a := range s.AllowedBranchIDs {
			if a == id {
				return true
			}
		}
		return false
	}
	forEachElem(tx, func(rv reflect.Value) {
		v, zero := f.ValueOf(tx.Statement.Context, rv)
		if zero {
			// El branch_id sale del contexto validado, no del cliente.
			if s.ActiveBranchID > 0 {
				_ = f.Set(tx.Statement.Context, rv, s.ActiveBranchID)
			}
			return
		}
		var id uint
		switch x := v.(type) {
		case uint:
			id = x
		case *uint:
			if x != nil {
				id = *x
			}
		}
		if id > 0 && !allowed(id) {
			_ = tx.AddError(ErrForbidden)
		}
	})
}

// forEachElem recorre el/los registros que se van a insertar (struct, puntero o slice).
func forEachElem(tx *gorm.DB, fn func(reflect.Value)) {
	rv := tx.Statement.ReflectValue
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			e := rv.Index(i)
			for e.Kind() == reflect.Ptr {
				e = e.Elem()
			}
			if e.Kind() == reflect.Struct {
				fn(e)
			}
		}
	case reflect.Struct:
		fn(rv)
	}
}
