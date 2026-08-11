package exchangerate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tukifac/pkg/tenantcache"
)

const redisKeyPrefix = "tukifac:exchange_rate:"

type redisCache struct{}

func newRedisCache() *redisCache { return &redisCache{} }

func redisKey(date string) string {
	return redisKeyPrefix + date
}

func (c *redisCache) get(ctx context.Context, date string) (*QueryResult, bool) {
	rdb := tenantcache.RDB()
	if rdb == nil {
		return nil, false
	}
	data, err := rdb.Get(ctx, redisKey(date)).Bytes()
	if err != nil || len(data) == 0 {
		return nil, false
	}
	var out QueryResult
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return &out, true
}

func (c *redisCache) set(ctx context.Context, date string, result *QueryResult, ttl time.Duration) {
	rdb := tenantcache.RDB()
	if rdb == nil || result == nil {
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	_ = rdb.Set(ctx, redisKey(date), data, ttl).Err()
}

func cacheTTL(requestedDate, todayLima string) time.Duration {
	if requestedDate == todayLima {
		return 48 * time.Hour
	}
	return 30 * 24 * time.Hour
}

func lockKey(date string) string {
	return fmt.Sprintf("%slock:%s", redisKeyPrefix, date)
}
