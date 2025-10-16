package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	redisClient *redis.Client
	ctx         = context.Background()
)

// initRedis initializes Redis connection
func initRedis() {
	redisClient = redis.NewClient(&redis.Options{
		Addr:     RedisAddr,
		Password: RedisPassword,
		DB:       RedisDB,
	})

	// Test connection
	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		fmt.Printf("Redis connection failed: %v\n", err)
		redisClient = nil
	} else {
		fmt.Println("Redis connected successfully")
	}
}

// saveSession saves a conversion session to Redis
func saveSession(session *ConversionSession) error {
	if redisClient == nil {
		return nil // Redis not available, skip
	}

	data, err := json.Marshal(session)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("session:%s", session.ID)
	return redisClient.Set(ctx, key, data, JobExpiration).Err()
}

// getSession retrieves a conversion session from Redis
func getSession(sessionID string) (*ConversionSession, error) {
	if redisClient == nil {
		return nil, fmt.Errorf("Redis not available")
	}

	key := fmt.Sprintf("session:%s", sessionID)
	data, err := redisClient.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var session ConversionSession
	err = json.Unmarshal([]byte(data), &session)
	return &session, err
}

// deleteSessionFromRedis deletes a session from Redis
func deleteSessionFromRedis(sessionID string) error {
	if redisClient == nil {
		return nil
	}

	key := fmt.Sprintf("session:%s", sessionID)
	return redisClient.Del(ctx, key).Err()
}

// saveURLMapping saves URL to session ID mapping
func saveURLMapping(url, sessionID string) error {
	if redisClient == nil {
		return nil
	}

	key := fmt.Sprintf("url:%s", url)
	return redisClient.Set(ctx, key, sessionID, JobExpiration).Err()
}

// getSessionIDByURL retrieves session ID by URL
func getSessionIDByURL(url string) (string, error) {
	if redisClient == nil {
		return "", fmt.Errorf("Redis not available")
	}

	key := fmt.Sprintf("url:%s", url)
	return redisClient.Get(ctx, key).Result()
}

// removeURLMapping removes URL mapping
func removeURLMapping(url string) error {
	if redisClient == nil {
		return nil
	}

	key := fmt.Sprintf("url:%s", url)
	return redisClient.Del(ctx, key).Err()
}