package database

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"tukifac/config"
	"tukifac/pkg/logger"
	"tukifac/pkg/metrics"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

type tenantPoolEntry struct {
	db       *gorm.DB
	lastUsed time.Time
	inUse    atomic.Int32
}

// TenantDBManager gestiona pools GORM por tenant con singleflight, LRU y eviction por inactividad.
type TenantDBManager struct {
	mu        sync.Mutex
	pools     map[string]*tenantPoolEntry
	sf        singleflight.Group
	idleTTL   time.Duration
	maxPools  int
	evictTick time.Duration
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

var defaultManager *TenantDBManager

// InitTenantDBManager arranca el manager global (llamar tras ConnectCentral).
func InitTenantDBManager(cfg *config.Config) {
	defaultManager = &TenantDBManager{
		pools:     make(map[string]*tenantPoolEntry),
		idleTTL:   cfg.TenantPoolIdleTTL,
		maxPools:  cfg.TenantPoolMaxActive,
		evictTick: cfg.TenantPoolEvictInterval,
		stopCh:    make(chan struct{}),
	}
	defaultManager.wg.Add(1)
	go defaultManager.evictionLoop()
	logger.L.Info("tenant_db_manager_started",
		slog.Duration("idle_ttl", cfg.TenantPoolIdleTTL),
		slog.Int("max_pools", cfg.TenantPoolMaxActive),
		slog.Int("tenant_max_open", cfg.DBTenantMaxOpen),
	)
}

// Shutdown cierra todos los pools (graceful shutdown).
func ShutdownTenantDBManager() {
	if defaultManager == nil {
		return
	}
	close(defaultManager.stopCh)
	defaultManager.wg.Wait()
	defaultManager.mu.Lock()
	defer defaultManager.mu.Unlock()
	for name, e := range defaultManager.pools {
		closeDB(e.db)
		delete(defaultManager.pools, name)
	}
	defaultManager = nil
}

// ReleaseTenantDB decrementa el contador de uso del pool (llamar al final de cada request HTTP).
func ReleaseTenantDB(dbName string) {
	if defaultManager == nil || dbName == "" {
		return
	}
	defaultManager.release(dbName)
}

// ActivePoolCount pools tenant actualmente abiertos.
func ActivePoolCount() int {
	if defaultManager == nil {
		return 0
	}
	defaultManager.mu.Lock()
	defer defaultManager.mu.Unlock()
	return len(defaultManager.pools)
}

func (m *TenantDBManager) acquire(dbName string) (*gorm.DB, error) {
	if err := validateTenantDBName(dbName); err != nil {
		return nil, err
	}

	now := time.Now()
	m.mu.Lock()
	if e, ok := m.pools[dbName]; ok {
		e.lastUsed = now
		e.inUse.Add(1)
		db := e.db
		m.mu.Unlock()
		return db, nil
	}
	m.mu.Unlock()

	v, err, shared := m.sf.Do(dbName, func() (interface{}, error) {
		return openTenantDB(dbName)
	})
	if err != nil {
		return nil, err
	}
	db := v.(*gorm.DB)

	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.pools[dbName]; ok {
		if !shared {
			closeDB(db)
		}
		e.lastUsed = now
		e.inUse.Add(1)
		return e.db, nil
	}
	if m.maxPools > 0 && len(m.pools) >= m.maxPools {
		m.evictLRULocked()
	}
	m.pools[dbName] = &tenantPoolEntry{db: db, lastUsed: now}
	m.pools[dbName].inUse.Add(1)
	metrics.TenantPoolOpened.Add(1)
	metrics.TenantPoolActive.Store(int64(len(m.pools)))
	return db, nil
}

func (m *TenantDBManager) release(dbName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.pools[dbName]
	if !ok {
		return
	}
	for {
		cur := e.inUse.Load()
		if cur <= 0 {
			logger.L.Warn("tenant_pool_release_underflow", slog.String("db", dbName))
			return
		}
		if e.inUse.CompareAndSwap(cur, cur-1) {
			return
		}
	}
}

// acquireBackground es para jobs que recorren TODOS los tenants (scanners, reportes). Si el tenant ya
// tiene pool abierto lo reutiliza; si no, abre una conexión efímera que NO entra al manager y se cierra
// al liberar. Así un barrido de ~600 tenants no expulsa (LRU) los pools de los tenants que sí están
// operando, que de otro modo pagan reconexión + cache de prepared statements frío en su siguiente request.
func (m *TenantDBManager) acquireBackground(dbName string) (*gorm.DB, func(), error) {
	if err := validateTenantDBName(dbName); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	if e, ok := m.pools[dbName]; ok {
		e.lastUsed = time.Now()
		e.inUse.Add(1)
		db := e.db
		m.mu.Unlock()
		return db, func() { m.release(dbName) }, nil
	}
	m.mu.Unlock()

	db, err := openTenantDB(dbName)
	if err != nil {
		return nil, nil, err
	}
	return db, func() { closeDB(db) }, nil
}

// GetTenantDBBackground ver acquireBackground. El caller DEBE invocar el release devuelto.
func GetTenantDBBackground(dbName string) (*gorm.DB, func(), error) {
	if defaultManager == nil {
		return nil, nil, ErrNoTenantDBManager
	}
	return defaultManager.acquireBackground(dbName)
}

// Get obtiene un pool e incrementa inUse; el caller debe invocar ReleaseTenantDB al terminar el request.
func (m *TenantDBManager) Get(dbName string) (*gorm.DB, error) {
	return m.acquire(dbName)
}

func (m *TenantDBManager) Remove(dbName string) {
	const maxWait = 30 * time.Second
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		e, ok := m.pools[dbName]
		if !ok {
			m.mu.Unlock()
			return
		}
		if e.inUse.Load() == 0 {
			closeDB(e.db)
			delete(m.pools, dbName)
			metrics.TenantPoolEvicted.Add(1)
			metrics.TenantPoolActive.Store(int64(len(m.pools)))
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	logger.L.Warn("tenant_pool_remove_forced", slog.String("db", dbName))
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.pools[dbName]; ok {
		closeDB(e.db)
		delete(m.pools, dbName)
		metrics.TenantPoolEvicted.Add(1)
		metrics.TenantPoolActive.Store(int64(len(m.pools)))
	}
}

func (m *TenantDBManager) evictionLoop() {
	defer m.wg.Done()
	t := time.NewTicker(m.evictTick)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.evictIdle()
		case <-m.stopCh:
			return
		}
	}
}

func (m *TenantDBManager) evictIdle() {
	cutoff := time.Now().Add(-m.idleTTL)
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, e := range m.pools {
		if e.inUse.Load() > 0 {
			continue
		}
		if e.lastUsed.Before(cutoff) {
			closeDB(e.db)
			delete(m.pools, name)
			metrics.TenantPoolEvicted.Add(1)
			logger.L.Debug("tenant_pool_evicted_idle", slog.String("db", name))
		}
	}
	metrics.TenantPoolActive.Store(int64(len(m.pools)))
}

func (m *TenantDBManager) evictLRULocked() {
	var oldestName string
	var oldest time.Time
	for name, e := range m.pools {
		if e.inUse.Load() > 0 {
			continue
		}
		if oldestName == "" || e.lastUsed.Before(oldest) {
			oldestName, oldest = name, e.lastUsed
		}
	}
	if oldestName == "" {
		return
	}
	// Cierre asíncrono: closeDB hace I/O de red y se llamaba con m.mu tomado, bloqueando a todos los
	// requests (acquire/release de cualquier tenant) mientras un barrido evictaba cientos de pools.
	go closeDB(m.pools[oldestName].db)
	delete(m.pools, oldestName)
	metrics.TenantPoolEvicted.Add(1)
	logger.L.Info("tenant_pool_evicted_lru", slog.String("db", oldestName), slog.Int("max_pools", m.maxPools))
}

// ErrNoTenantDBManager si el manager no fue inicializado.
var ErrNoTenantDBManager = fmt.Errorf("tenant db manager not initialized")
