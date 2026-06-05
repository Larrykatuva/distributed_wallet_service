package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type CacheService[T any] interface {
	WithExpiry(duration time.Duration) *Cache[T]
	WithData(data T) *Cache[T]
	WithKey(key string) *Cache[T]
	Save() error
	Clear() error
	Get() (*T, error)
}

type Cache[T any] struct {
	expiry time.Duration
	data   T
	key    *string
	client *redis.Client
	ctx    context.Context
}

func NewCacheService[T any]() *Cache[T] {
	return &Cache[T]{
		expiry: 1 * time.Minute,
		client: RedisClient,
		ctx:    context.Background(),
	}
}

func (c *Cache[T]) WithExpiry(duration time.Duration) *Cache[T] {
	c.expiry = duration
	return c
}

func (c *Cache[T]) WithData(data T) *Cache[T] {
	c.data = data
	return c
}

func (c *Cache[T]) WithKey(key string) *Cache[T] {
	c.key = &key
	return c
}

func (c *Cache[T]) Save() error {
	if c.key == nil {
		return errors.New("key to cache value must be set")
	}
	dataBytes, err := json.Marshal(c.data)
	if err != nil {
		return err
	}
	if err := c.client.Set(c.ctx, *c.key, dataBytes, c.expiry).Err(); err != nil {
		return err
	}
	return nil
}

func (c *Cache[T]) Clear() error {
	return c.client.FlushAll(c.ctx).Err()
}

func (c *Cache[T]) Get() (*T, error) {
	if c.key == nil {
		return nil, errors.New("key to cache value must be set")
	}
	dataBytes, err := c.client.Get(c.ctx, *c.key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}

	var result T
	if err := json.Unmarshal(dataBytes, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
