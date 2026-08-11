package exchangerate

import (
	"context"
	"fmt"
	"os"
	"time"

	"tukifac/pkg/tenantcache"
)

type distributedLock struct{}

func newDistributedLock() *distributedLock { return &distributedLock{}}

func (l *distributedLock) tryAcquire(ctx context.Context, date string) (release func(), acquired bool) {
	noop := func() {}
	rdb := tenantcache.RDB()
	if rdb == nil {
		return noop, true
	}
	owner := fmt.Sprintf("%s:%d", hostname(), os.Getpid())
	ok, err := rdb.SetNX(ctx, lockKey(date), owner, lockTTL).Result()
	if err != nil || !ok {
		return noop, false
	}
	return func() {
		script := `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`
		_, _ = rdb.Eval(context.Background(), script, []string{lockKey(date)}, owner).Result()
	}, true
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

func (l *distributedLock) waitForPeer(ctx context.Context, date string, cache *redisCache) *QueryResult {
	deadline := time.Now().Add(waitForPeerMax)
	for time.Now().Before(deadline) {
		if res, ok := cache.get(ctx, date); ok && res != nil && res.Success {
			return res
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(waitForPeerStep):
		}
	}
	return nil
}
