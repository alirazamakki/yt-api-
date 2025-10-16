package main

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

var (
	// Server state
	serverStartTime = time.Now()
	
	// Job counters
	activeJobs    int64
	queuedJobs    int64
	completedJobs int64
	failedJobs    int64
	totalProcessingTimeNs int64

	// Rate limiting
	rateLimiter *rate.Limiter
	ipLimiters  = struct {
		sync.RWMutex
		m map[string]*rate.Limiter
	}{m: make(map[string]*rate.Limiter)}

	// API keys
	apiKeys = make(map[string]struct{})

	// Session management
	sessions = struct {
		sync.RWMutex
		m map[string]*ConversionSession
	}{m: make(map[string]*ConversionSession)}

	// Job queue
	jobQueue chan *ConversionJob

	// Concurrency control
	downloadSlots  chan struct{}
	convertSlots   chan struct{}

	// Download tracking
	downloadTrackers = struct {
		sync.RWMutex
		inProgress map[string]int
		scheduled  map[string]bool
	}{
		inProgress: make(map[string]int),
		scheduled:  make(map[string]bool),
	}
)

// ConversionJob represents a legacy job (for compatibility)
type ConversionJob struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	VideoID     string     `json:"video_id"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt time.Time  `json:"completed_at"`
	FilePath    string     `json:"file_path"`
	DownloadURL string     `json:"download_url"`
	FirstDownloadedAt time.Time `json:"first_downloaded_at"`
	Error       string     `json:"error"`
	Metadata    *MetaLite  `json:"metadata"`
	Retries     int        `json:"retries"`
	MaxRetries  int        `json:"max_retries"`
	Priority    int        `json:"priority"`
	CallbackURL string     `json:"callback_url,omitempty"`
}

// getSession retrieves a session from memory
func getSession(sessionID string) (*ConversionSession, bool) {
	sessions.RLock()
	defer sessions.RUnlock()
	session, exists := sessions.m[sessionID]
	return session, exists
}

// saveSession saves a session to memory and Redis
func saveSession(session *ConversionSession) {
	sessions.Lock()
	sessions.m[session.ID] = session
	sessions.Unlock()
	
	// Also save to Redis
	_ = saveSession(session)
}

// deleteSession deletes a session from memory and Redis
func deleteSession(sessionID string) {
	sessions.Lock()
	delete(sessions.m, sessionID)
	sessions.Unlock()
	
	// Also delete from Redis
	_ = deleteSessionFromRedis(sessionID)
}

// generateSessionID generates a new session ID
func generateSessionID() string {
	return "conv_" + uuid.New().String()
}

// getMemoryUsage returns current memory usage as string
func getMemoryUsage() string {
	// Simple memory usage calculation
	// In a real implementation, you'd use runtime.MemStats
	return "N/A"
}

// calculateSuccessRate calculates the success rate
func calculateSuccessRate() float64 {
	total := atomic.LoadInt64(&completedJobs) + atomic.LoadInt64(&failedJobs)
	if total == 0 {
		return 0.0
	}
	return float64(atomic.LoadInt64(&completedJobs)) / float64(total)
}

// getAvgProcessingTime returns average processing time in seconds
func getAvgProcessingTime() float64 {
	completed := atomic.LoadInt64(&completedJobs)
	if completed == 0 {
		return 0.0
	}
	totalNs := atomic.LoadInt64(&totalProcessingTimeNs)
	return float64(totalNs) / float64(completed) / 1e9
}

// getDownloadsDiskMetrics returns disk usage metrics
func getDownloadsDiskMetrics() (total, free, used int64, err error) {
	// Placeholder implementation
	// In a real implementation, you'd use syscall.Statfs or similar
	return 0, 0, 0, nil
}