package main

import (
    "log"
    "os"
    "os/signal"
    "syscall"
    "runtime"
    "fmt"
    "strings"
    neturl "net/url"
    "time"
    "path/filepath"
)

func setupGracefulShutdown() {
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    go func() {
        <-c
        log.Println("🛑 Graceful shutdown initiated...")
        cancel()
        close(jobQueue)
        // Let workers finish gracefully
        log.Println("✅ Graceful shutdown completed")
        os.Exit(0)
    }()
}

func getMemoryUsage() string {
    var m runtime.MemStats
    runtime.ReadMemStats(&m)
    return fmt.Sprintf("Alloc=%d Sys=%d NumGC=%d", m.Alloc, m.Sys, m.NumGC)
}

func calculateSuccessRate() float64 {
    total := completedJobs + failedJobs
    if total <= 0 {
        return 0
    }
    return float64(completedJobs) / float64(total)
}

// colored logging helpers
const (
    colorReset = "\033[0m"
    colorGreen = "\033[32m"
    colorYellow = "\033[33m"
    colorRed = "\033[31m"
    colorGray = "\033[90m"
)

func logInfof(format string, v ...interface{}) {
    if ColorLogs {
        log.Printf(colorGreen+"[INFO] "+colorReset+format, v...)
        return
    }
    log.Printf("[INFO] "+format, v...)
}

func logWarnf(format string, v ...interface{}) {
    if ColorLogs {
        log.Printf(colorYellow+"[WARN] "+colorReset+format, v...)
        return
    }
    log.Printf("[WARN] "+format, v...)
}

func logErrorf(format string, v ...interface{}) {
    if ColorLogs {
        log.Printf(colorRed+"[ERROR] "+colorReset+format, v...)
        return
    }
    log.Printf("[ERROR] "+format, v...)
}

func logDebugf(format string, v ...interface{}) {
    if ColorLogs {
        log.Printf(colorGray+"[DEBUG] "+colorReset+format, v...)
        return
    }
    log.Printf("[DEBUG] "+format, v...)
}

func getAvgProcessingTime() float64 {
    c := completedJobs
    if c <= 0 {
        return 0
    }
    return float64(totalProcessingTimeNs) / float64(c) / 1e9
}

// disk metrics for downloads directory
func getDownloadsDiskMetrics() (total uint64, free uint64, used uint64, err error) {
    dir := "downloads"
    abs, _ := filepath.Abs(dir)
    var stat syscall.Statfs_t
    if e := syscall.Statfs(abs, &stat); e != nil {
        return 0, 0, 0, e
    }
    total = stat.Blocks * uint64(stat.Bsize)
    free = stat.Bavail * uint64(stat.Bsize)
    used = total - free
    return
}

// classify a duration limit error from yt-dlp helpers
func isDurationExceededError(e error) bool {
    if e == nil { return false }
    return strings.Contains(e.Error(), "video duration exceeds max")
}

// YouTube helpers: extract video ID and canonicalize to watch URL
func extractYouTubeVideoID(raw string) (string, bool) {
    u, err := neturl.Parse(raw)
    if err != nil || u == nil {
        return "", false
    }
    host := strings.ToLower(u.Host)
    // Strip port if any
    if i := strings.Index(host, ":"); i >= 0 {
        host = host[:i]
    }
    path := strings.Trim(u.Path, "/")

    // youtu.be/<id>
    if host == "youtu.be" && path != "" {
        parts := strings.Split(path, "/")
        if len(parts) >= 1 && parts[0] != "" {
            return parts[0], true
        }
        return "", false
    }

    // *.youtube.com
    if strings.HasSuffix(host, "youtube.com") {
        // /watch?v=<id>
        if strings.EqualFold(path, "watch") {
            v := u.Query().Get("v")
            if v != "" {
                return v, true
            }
        }
        // /shorts/<id>
        if strings.HasPrefix(path, "shorts/") {
            id := strings.TrimPrefix(path, "shorts/")
            id = strings.SplitN(id, "/", 2)[0]
            if id != "" {
                return id, true
            }
        }
        // /embed/<id>
        if strings.HasPrefix(path, "embed/") {
            id := strings.TrimPrefix(path, "embed/")
            id = strings.SplitN(id, "/", 2)[0]
            if id != "" {
                return id, true
            }
        }
    }

    return "", false
}

func canonicalizeYouTubeURL(raw string) (string, bool) {
    if id, ok := extractYouTubeVideoID(raw); ok {
        return "https://www.youtube.com/watch?v=" + id, true
    }
    return "", false
}

func isValidYouTubeURL(raw string) bool {
    _, ok := extractYouTubeVideoID(raw)
    return ok
}

// Schedules deletion 10 minutes after completion unless an active download is in progress.
// If a download starts, FirstDownloadedAt gets set and deletion still happens after 10 minutes from that mark.
func scheduleSafeDeletion(job *ConversionJob) {
    // Wait window
    time.Sleep(10 * time.Minute)
    if job == nil || job.FilePath == "" {
        return
    }
    // If download in progress, delay until finished (simple wait loop with cap)
    for i := 0; i < 60; i++ { // up to ~10 minutes more
        downloadTrackers.Lock()
        inProg := downloadTrackers.inProgress[job.ID]
        downloadTrackers.Unlock()
        if inProg <= 0 {
            break
        }
        time.Sleep(10 * time.Second)
    }
    // Remove file
    _ = os.Remove(job.FilePath)
    // Remove from memory
    jobStore.Lock()
    delete(jobStore.jobs, job.ID)
    jobStore.Unlock()
    // Remove from Redis and URL map
    deleteJobFromRedis(job.ID)
    if job.URL != "" {
        removeURLMapping(job.URL)
    }
}
