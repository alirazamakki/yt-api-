package main

import (
	"os"
	"strconv"
	"time"
)

// Centralized configuration values (env-overridable)
var (
	// Worker Configuration
	WorkerPoolSize   = 20
	MaxJobRetries    = 3
	JobQueueCapacity = 1000

	// Rate Limiting
	RequestsPerSecond = 100
	BurstSize         = 200

	// Redis Configuration
	RedisAddr     = "localhost:6379"
	RedisPassword = ""
	RedisDB       = 0

	// Job Expiration (as duration)
	JobExpiration = 24 * time.Hour

	// Health Check
	HealthCheckInterval = 30 * time.Second

	// Fast-path response: wait briefly for quick jobs
	FastPathWait = 8 * time.Second

	// Security & Abuse
	AllowedOrigins     = "*" // comma-separated
	RequireAPIKey      = false
	APIKeysCSV         = ""   // comma-separated list
	PerIPRPS           = 10
	PerIPBurst         = 20
	MaxURLLength       = 2048

    // External tool timeouts
    // Timeout for yt-dlp metadata fetch
    YTDLPTimeout       = 90 * time.Second
    // ffmpeg conversion timeout bounds; actual timeout is dynamic per video
    FFmpegMinTimeout   = 15 * time.Minute
    FFmpegMaxTimeout   = 60 * time.Minute

	// Retry/backoff
	BackoffBaseSeconds = 5
	BackoffMaxSeconds  = 60

	// Admin UI credentials
	AdminUser = ""
	AdminPass = ""

    // Logging
    ColorLogs = true

    // yt-dlp tuning
    YTDLPExtraArgs      = ""
    YTDLPExtractorArgs  = ""
    YTDLPCookies        = "" // "browser:chrome" or "/path/cookies.txt"

    // Download-then-convert strategy
    AlwaysDownload      = false
    DownloadThreshold   = 10 * time.Minute // if duration >= threshold, download first
    YTDLPDownloadConcurrency = 8
    YTDLPDownloadTimeout     = 30 * time.Minute

    // ffmpeg audio settings
    FFmpegMode       = "CBR"   // CBR or VBR
    FFmpegCBRBitrate = "192k"  // used when CBR
    FFmpegVBRQ       = 5        // used when VBR (0..9, lower=better)
    FFmpegThreads    = 0        // 0 = auto

    // Skip metadata (-J) and start immediately
    SkipMetadata = false
)

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		// fallback: seconds as int
		if i, err := strconv.Atoi(v); err == nil {
			return time.Duration(i) * time.Second
		}
	}
	return def
}

// InitConfigFromEnv should be called early in main() before using these values
func InitConfigFromEnv() {
	WorkerPoolSize = envInt("WORKER_POOL_SIZE", WorkerPoolSize)
	MaxJobRetries = envInt("MAX_JOB_RETRIES", MaxJobRetries)
	JobQueueCapacity = envInt("JOB_QUEUE_CAPACITY", JobQueueCapacity)

	RequestsPerSecond = envInt("REQUESTS_PER_SECOND", RequestsPerSecond)
	BurstSize = envInt("BURST_SIZE", BurstSize)

	RedisAddr = envString("REDIS_ADDR", RedisAddr)
	RedisPassword = envString("REDIS_PASSWORD", RedisPassword)
	RedisDB = envInt("REDIS_DB", RedisDB)

	// Prefer JOB_EXPIRATION (duration like "24h"); fallback to JOB_EXPIRATION_HOURS (int hours)
	JobExpiration = envDuration("JOB_EXPIRATION", JobExpiration)
	if os.Getenv("JOB_EXPIRATION") == "" {
		defHours := int(JobExpiration / time.Hour)
		h := envInt("JOB_EXPIRATION_HOURS", defHours)
		JobExpiration = time.Duration(h) * time.Hour
	}

	HealthCheckInterval = envDuration("HEALTH_CHECK_INTERVAL", HealthCheckInterval)
	FastPathWait = envDuration("FAST_PATH_WAIT", FastPathWait)

	AllowedOrigins = envString("ALLOWED_ORIGINS", AllowedOrigins)
	RequireAPIKey = envString("REQUIRE_API_KEY", "false") == "true"
	APIKeysCSV = envString("API_KEYS", APIKeysCSV)
	PerIPRPS = envInt("PER_IP_RPS", PerIPRPS)
	PerIPBurst = envInt("PER_IP_BURST", PerIPBurst)
	MaxURLLength = envInt("MAX_URL_LENGTH", MaxURLLength)

	BackoffBaseSeconds = envInt("BACKOFF_BASE_SECONDS", BackoffBaseSeconds)
	BackoffMaxSeconds = envInt("BACKOFF_MAX_SECONDS", BackoffMaxSeconds)

	AdminUser = envString("ADMIN_USER", AdminUser)
	AdminPass = envString("ADMIN_PASS", AdminPass)

    // External tool timeouts
    YTDLPTimeout = envDuration("YTDLP_TIMEOUT", YTDLPTimeout)
    FFmpegMinTimeout = envDuration("FFMPEG_MIN_TIMEOUT", FFmpegMinTimeout)
    FFmpegMaxTimeout = envDuration("FFMPEG_MAX_TIMEOUT", FFmpegMaxTimeout)

    // Logging
    ColorLogs = envString("COLOR_LOGS", "true") == "true"

    // yt-dlp tuning
    YTDLPExtraArgs = envString("YTDLP_EXTRA_ARGS", YTDLPExtraArgs)
    YTDLPExtractorArgs = envString("YTDLP_EXTRACTOR_ARGS", YTDLPExtractorArgs)
    YTDLPCookies = envString("YTDLP_COOKIES", YTDLPCookies)

    // Download-then-convert strategy
    AlwaysDownload = envString("ALWAYS_DOWNLOAD", "false") == "true"
    DownloadThreshold = envDuration("DOWNLOAD_THRESHOLD", DownloadThreshold)
    YTDLPDownloadConcurrency = envInt("YTDLP_DOWNLOAD_CONCURRENCY", YTDLPDownloadConcurrency)
    YTDLPDownloadTimeout = envDuration("YTDLP_DOWNLOAD_TIMEOUT", YTDLPDownloadTimeout)

    // ffmpeg audio settings
    FFmpegMode = envString("FFMPEG_MODE", FFmpegMode)
    FFmpegCBRBitrate = envString("FFMPEG_CBR_BITRATE", FFmpegCBRBitrate)
    FFmpegVBRQ = envInt("FFMPEG_VBR_Q", FFmpegVBRQ)
    FFmpegThreads = envInt("FFMPEG_THREADS", FFmpegThreads)

    // Skip metadata
    SkipMetadata = envString("SKIP_METADATA", "false") == "true"
}
