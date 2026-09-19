// Package cache is a thin Redis layer used to short-circuit hot read paths.
// It is optional: when Redis is not configured or unreachable every call is a
// no-op miss, so the service keeps working straight from Postgres.
package cache

import (
	"context"
	"time"

	"github.com/katuva/wallet/dpk/logger"
	"github.com/redis/go-redis/v9"
)

// Options configure the shared client.
type Options struct {
	Addr     string // host:port; empty disables caching
	Password string
	DB       int
	// DefaultTTL applies when a call passes ttl <= 0.
	DefaultTTL time.Duration
	// Timeout bounds each Redis round trip so a slow cache never stalls a request.
	Timeout time.Duration
}

// Client wraps go-redis with enable/disable semantics.
type Client struct {
	rdb     *redis.Client
	ttl     time.Duration
	timeout time.Duration
}

// Default is the process-wide client set by Start. A nil Default is safe: all
// package functions treat it as disabled.
var Default *Client

// Start connects, pings, and installs Default. Failure only logs: the app
// runs without a cache rather than refusing to start.
func Start(opts Options) *Client {
	if opts.DefaultTTL <= 0 {
		opts.DefaultTTL = 5 * time.Minute
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 150 * time.Millisecond
	}
	c := &Client{ttl: opts.DefaultTTL, timeout: opts.Timeout}
	Default = c

	if opts.Addr == "" {
		logger.InfoLog.Println("cache: REDIS_ADDR not set, caching disabled")
		return c
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:         opts.Addr,
		Password:     opts.Password,
		DB:           opts.DB,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  opts.Timeout,
		WriteTimeout: opts.Timeout,
		PoolSize:     32,
		MinIdleConns: 4,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.WarningLog.Printf("cache: redis at %s unreachable (%v), caching disabled", opts.Addr, err)
		_ = rdb.Close()
		return c
	}
	c.rdb = rdb
	logger.InfoLog.Printf("cache: connected to redis at %s (db %d, default ttl %s)", opts.Addr, opts.DB, opts.DefaultTTL)
	return c
}

// Enabled reports whether a live Redis connection exists.
func (c *Client) Enabled() bool { return c != nil && c.rdb != nil }

// Close releases the connection pool.
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

func (c *Client) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), c.timeout)
}
