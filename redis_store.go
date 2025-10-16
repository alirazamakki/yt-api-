package main

import (
    "encoding/json"
    "fmt"
    "log"

    redis "github.com/redis/go-redis/v9"
    xxhash "github.com/cespare/xxhash/v2"
)

func initRedis() {
    redisClient = redis.NewClient(&redis.Options{
        Addr:     RedisAddr,
        Password: RedisPassword,
        DB:       RedisDB,
    })
    if _, err := redisClient.Ping(ctx).Result(); err != nil {
        log.Printf("⚠️  Redis not available, using in-memory storage: %v", err)
        redisClient = nil
    } else {
        log.Println("✅ Redis connected successfully")
    }
}

func saveJobToRedis(job *ConversionJob) error {
    if redisClient == nil {
        return nil
    }
    jobData, err := json.Marshal(job)
    if err != nil {
        return err
    }
    key := fmt.Sprintf("job:%s", job.ID)
    expiration := JobExpiration
    return redisClient.Set(ctx, key, jobData, expiration).Err()
}

func getJobFromRedis(jobID string) (*ConversionJob, error) {
    if redisClient == nil {
        return nil, nil
    }
    key := fmt.Sprintf("job:%s", jobID)
    val, err := redisClient.Get(ctx, key).Result()
    if err != nil {
        return nil, err
    }
    var job ConversionJob
    if err := json.Unmarshal([]byte(val), &job); err != nil {
        return nil, err
    }
    return &job, nil
}

// URL mapping for deduplication across restarts
func saveURLMapping(videoURL, jobID string) error {
    if redisClient == nil {
        return nil
    }
    key := fmt.Sprintf("url:%x", xxhashString(videoURL))
    expiration := JobExpiration
    return redisClient.Set(ctx, key, jobID, expiration).Err()
}

func getJobIDByURL(videoURL string) (string, error) {
    if redisClient == nil {
        return "", nil
    }
    key := fmt.Sprintf("url:%x", xxhashString(videoURL))
    return redisClient.Get(ctx, key).Result()
}

func removeURLMapping(videoURL string) {
    if redisClient == nil {
        return
    }
    key := fmt.Sprintf("url:%x", xxhashString(videoURL))
    _ = redisClient.Del(ctx, key).Err()
}

func deleteJobFromRedis(jobID string) {
    if redisClient == nil {
        return
    }
    key := fmt.Sprintf("job:%s", jobID)
    _ = redisClient.Del(ctx, key).Err()
}

// Session store (prepare/convert flow)
func saveSessionToRedis(sess *ConversionSession) error {
    if redisClient == nil || sess == nil {
        return nil
    }
    key := fmt.Sprintf("sess:%s", sess.ID)
    data, err := json.Marshal(sess)
    if err != nil { return err }
    return redisClient.Set(ctx, key, data, JobExpiration).Err()
}

func getSessionFromRedis(id string) (*ConversionSession, error) {
    if redisClient == nil {
        return nil, nil
    }
    key := fmt.Sprintf("sess:%s", id)
    val, err := redisClient.Get(ctx, key).Result()
    if err != nil { return nil, err }
    var s ConversionSession
    if err := json.Unmarshal([]byte(val), &s); err != nil { return nil, err }
    return &s, nil
}

func deleteSessionFromRedis(id string) {
    if redisClient == nil { return }
    key := fmt.Sprintf("sess:%s", id)
    _ = redisClient.Del(ctx, key).Err()
}

// Combined helpers to keep in-memory and Redis in sync
func saveSession(sess *ConversionSession) {
    if sess == nil { return }
    sessions.Lock()
    sessions.m[sess.ID] = sess
    sessions.Unlock()
    _ = saveSessionToRedis(sess)
}

func getSession(id string) (*ConversionSession, bool) {
    sessions.RLock()
    s, ok := sessions.m[id]
    sessions.RUnlock()
    if ok && s != nil { return s, true }
    // Try Redis
    if rs, err := getSessionFromRedis(id); err == nil && rs != nil {
        sessions.Lock(); sessions.m[id] = rs; sessions.Unlock()
        return rs, true
    }
    return nil, false
}

func deleteSession(id string) {
    sessions.Lock(); delete(sessions.m, id); sessions.Unlock()
    deleteSessionFromRedis(id)
}

func xxhashString(s string) uint64 {
    return xxhash.Sum64String(s)
}

// Idempotency key mapping
func saveIdempotencyKey(idemKey, jobID string) error {
    if redisClient == nil {
        return nil
    }
    key := fmt.Sprintf("idem:%x", xxhashString(idemKey))
    return redisClient.Set(ctx, key, jobID, JobExpiration).Err()
}

func getJobIDByIdempotency(idemKey string) (string, error) {
    if redisClient == nil {
        return "", nil
    }
    key := fmt.Sprintf("idem:%x", xxhashString(idemKey))
    return redisClient.Get(ctx, key).Result()
}
