package cache

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/katuva/wallet/dpk/logger"
	"github.com/redis/go-redis/v9"
)

// Get loads key into dest. found is false on a miss or when caching is off.
// Errors are logged and reported as misses so callers never fail on cache.
func Get(key string, dest any) (found bool) {
	c := Default
	if !c.Enabled() {
		return false
	}
	ctx, cancel := c.ctx()
	defer cancel()

	b, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			logger.WarningLog.Printf("cache: get %s: %v", key, err)
		}
		return false
	}
	if err = json.Unmarshal(b, dest); err != nil {
		logger.WarningLog.Printf("cache: decode %s: %v", key, err)
		return false
	}
	return true
}

// Set stores v under key. ttl <= 0 uses the default TTL.
func Set(key string, v any, ttl time.Duration) {
	c := Default
	if !c.Enabled() {
		return
	}
	if ttl <= 0 {
		ttl = c.ttl
	}
	b, err := json.Marshal(v)
	if err != nil {
		logger.WarningLog.Printf("cache: encode %s: %v", key, err)
		return
	}
	ctx, cancel := c.ctx()
	defer cancel()
	if err = c.rdb.Set(ctx, key, b, ttl).Err(); err != nil {
		logger.WarningLog.Printf("cache: set %s: %v", key, err)
	}
}

// Delete removes keys (invalidation after a write). Missing keys are fine.
func Delete(keys ...string) {
	c := Default
	if !c.Enabled() || len(keys) == 0 {
		return
	}
	ctx, cancel := c.ctx()
	defer cancel()
	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		logger.WarningLog.Printf("cache: delete %v: %v", keys, err)
	}
}

// Exists reports whether key is present. False when caching is off.
func Exists(key string) bool {
	c := Default
	if !c.Enabled() {
		return false
	}
	ctx, cancel := c.ctx()
	defer cancel()
	n, err := c.rdb.Exists(ctx, key).Result()
	return err == nil && n > 0
}

// Remember returns the cached value for key or loads, stores and returns it.
// A load error is returned as-is and nothing is cached.
func Remember[T any](key string, ttl time.Duration, load func() (T, error)) (T, error) {
	var v T
	if Get(key, &v) {
		return v, nil
	}
	v, err := load()
	if err != nil {
		return v, err
	}
	Set(key, v, ttl)
	return v, nil
}
