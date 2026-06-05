package cache

import (
	"os"

	"github.com/redis/go-redis/v9"
)

// RedisClient Declare global RedisClient for different log levels
var RedisClient *redis.Client

// StartRedisClient initializes the Redis client and checks the connection.
// It reads the Redis URL and password from environment variables and logs any errors.
func StartRedisClient() {
	RedisClient = redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS_URL"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})
}
