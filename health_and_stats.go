package main

import (
    "encoding/json"
    "net/http"
    "path/filepath"
    "sync/atomic"
    "time"
    "os"
    "fmt"
)

func handleHealth(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    status := "healthy"
    // Consider both active and queued jobs as load indicator
    if atomic.LoadInt64(&activeJobs) >= int64(WorkerPoolSize) || atomic.LoadInt64(&queuedJobs) > int64(JobQueueCapacity/2) {
        status = "overloaded"
    }
    health := HealthStatus{
        Status:        status,
        ActiveJobs:    atomic.LoadInt64(&activeJobs),
        QueuedJobs:    atomic.LoadInt64(&queuedJobs),
        CompletedJobs: atomic.LoadInt64(&completedJobs),
        FailedJobs:    atomic.LoadInt64(&failedJobs),
        Workers:       WorkerPoolSize,
        Uptime:        time.Since(serverStartTime).String(),
        MemoryUsage:   getMemoryUsage(),
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(health)
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    // Disk metrics
    total, free, used, _ := getDownloadsDiskMetrics()
    // Download folder counts best-effort
    jobStore.RLock()
    filesCount := 0
    jobCount := len(jobStore.jobs)
    jobStore.RUnlock()
    metrics := map[string]interface{}{
        "active_jobs":    atomic.LoadInt64(&activeJobs),
        "queued_jobs":    atomic.LoadInt64(&queuedJobs),
        "completed_jobs": atomic.LoadInt64(&completedJobs),
        "failed_jobs":    atomic.LoadInt64(&failedJobs),
        "workers":        WorkerPoolSize,
        "queue_capacity": JobQueueCapacity,
        "rate_limit":     RequestsPerSecond,
        "uptime_seconds": time.Since(serverStartTime).Seconds(),
        "success_rate":   calculateSuccessRate(),
        "avg_processing_s": getAvgProcessingTime(),
        "downloads_disk_total_bytes": total,
        "downloads_disk_used_bytes":  used,
        "downloads_disk_free_bytes":  free,
        "downloads_files_count":      filesCount,
        "jobs_in_memory":             jobCount,
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(metrics)
}

func handleStats(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    jobStore.RLock()
    totalJobs := len(jobStore.jobs)
    jobStore.RUnlock()

    stats := map[string]interface{}{
        "total_jobs":           totalJobs,
        "active_jobs":          atomic.LoadInt64(&activeJobs),
        "queued_jobs":          atomic.LoadInt64(&queuedJobs),
        "completed_jobs":       atomic.LoadInt64(&completedJobs),
        "failed_jobs":          atomic.LoadInt64(&failedJobs),
        "success_rate":         calculateSuccessRate(),
        "avg_processing_time":  getAvgProcessingTime(),
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(stats)
}

// DELETE /delete/{job_id}
func handleDelete(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    if r.Method == http.MethodOptions {
        w.WriteHeader(http.StatusOK)
        return
    }
    if r.Method != http.MethodDelete {
        http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
        return
    }
    jobID := filepath.Base(r.URL.Path)
    if jobID == "" {
        http.Error(w, "Missing job ID", http.StatusBadRequest)
        return
    }
    // Load job from memory or Redis if available
    var job *ConversionJob
    jobStore.RLock()
    j, exists := jobStore.jobs[jobID]
    jobStore.RUnlock()
    if exists {
        job = j
    } else if rj, err := getJobFromRedis(jobID); err == nil && rj != nil {
        job = rj
    }

    // Prevent deletion during active download
    downloadTrackers.Lock()
    inProg := downloadTrackers.inProgress[jobID]
    downloadTrackers.Unlock()
    if inProg > 0 {
        http.Error(w, "Download in progress; try again later", http.StatusConflict)
        return
    }

    // Remove file (best effort)
    if job != nil && job.FilePath != "" {
        _ = os.Remove(job.FilePath)
    } else {
        _ = os.Remove(filepath.Join("downloads", jobID+".mp3"))
    }

    // Remove from memory store
    jobStore.Lock()
    delete(jobStore.jobs, jobID)
    jobStore.Unlock()

    // Remove from Redis and URL map
    deleteJobFromRedis(jobID)
    if job != nil && job.URL != "" {
        removeURLMapping(job.URL)
    }

    // Clear download trackers
    downloadTrackers.Lock()
    delete(downloadTrackers.inProgress, jobID)
    delete(downloadTrackers.scheduled, jobID)
    downloadTrackers.Unlock()

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"deleted": jobID})
}

// Prometheus exposition format metrics
// GET /metrics/prom
func handlePromMetrics(w http.ResponseWriter, r *http.Request) {
    // No CORS; intended for internal scraping via nginx allow-list
    w.Header().Set("Content-Type", "text/plain; version=0.0.4")

    // Snapshot metrics
    aj := atomic.LoadInt64(&activeJobs)
    qj := atomic.LoadInt64(&queuedJobs)
    cj := atomic.LoadInt64(&completedJobs)
    fj := atomic.LoadInt64(&failedJobs)
    up := time.Since(serverStartTime).Seconds()
    sr := calculateSuccessRate()
    avg := getAvgProcessingTime()
    cap := float64(JobQueueCapacity)
    fill := 0.0
    if cap > 0 { fill = float64(qj) / cap }
    over80 := 0.0
    if fill >= 0.8 { over80 = 1.0 }
    redisAvail := 0.0
    if redisClient != nil { redisAvail = 1.0 }

    // Write metrics
    fmt.Fprintf(w, "# HELP ytmp3_active_jobs Number of active conversion jobs.\n")
    fmt.Fprintf(w, "# TYPE ytmp3_active_jobs gauge\n")
    fmt.Fprintf(w, "ytmp3_active_jobs %d\n", aj)

    fmt.Fprintf(w, "# HELP ytmp3_queued_jobs Number of queued jobs.\n")
    fmt.Fprintf(w, "# TYPE ytmp3_queued_jobs gauge\n")
    fmt.Fprintf(w, "ytmp3_queued_jobs %d\n", qj)

    fmt.Fprintf(w, "# HELP ytmp3_completed_jobs Total completed jobs.\n")
    fmt.Fprintf(w, "# TYPE ytmp3_completed_jobs counter\n")
    fmt.Fprintf(w, "ytmp3_completed_jobs %d\n", cj)

    fmt.Fprintf(w, "# HELP ytmp3_failed_jobs Total failed jobs.\n")
    fmt.Fprintf(w, "# TYPE ytmp3_failed_jobs counter\n")
    fmt.Fprintf(w, "ytmp3_failed_jobs %d\n", fj)

    fmt.Fprintf(w, "# HELP ytmp3_worker_pool_size Configured worker pool size.\n")
    fmt.Fprintf(w, "# TYPE ytmp3_worker_pool_size gauge\n")
    fmt.Fprintf(w, "ytmp3_worker_pool_size %d\n", WorkerPoolSize)

    fmt.Fprintf(w, "# HELP ytmp3_queue_capacity Job queue capacity.\n")
    fmt.Fprintf(w, "# TYPE ytmp3_queue_capacity gauge\n")
    fmt.Fprintf(w, "ytmp3_queue_capacity %d\n", JobQueueCapacity)

    fmt.Fprintf(w, "# HELP ytmp3_queue_fill_ratio Queue fill ratio (0..1).\n")
    fmt.Fprintf(w, "# TYPE ytmp3_queue_fill_ratio gauge\n")
    fmt.Fprintf(w, "ytmp3_queue_fill_ratio %.6f\n", fill)

    fmt.Fprintf(w, "# HELP ytmp3_queue_over_80 Queue over 80 percent (0/1).\n")
    fmt.Fprintf(w, "# TYPE ytmp3_queue_over_80 gauge\n")
    fmt.Fprintf(w, "ytmp3_queue_over_80 %.0f\n", over80)

    fmt.Fprintf(w, "# HELP ytmp3_requests_per_second Admission rate limit.\n")
    fmt.Fprintf(w, "# TYPE ytmp3_requests_per_second gauge\n")
    fmt.Fprintf(w, "ytmp3_requests_per_second %d\n", RequestsPerSecond)

    fmt.Fprintf(w, "# HELP ytmp3_uptime_seconds Uptime in seconds.\n")
    fmt.Fprintf(w, "# TYPE ytmp3_uptime_seconds counter\n")
    fmt.Fprintf(w, "ytmp3_uptime_seconds %.0f\n", up)

    fmt.Fprintf(w, "# HELP ytmp3_success_rate Success rate (0..1).\n")
    fmt.Fprintf(w, "# TYPE ytmp3_success_rate gauge\n")
    fmt.Fprintf(w, "ytmp3_success_rate %.6f\n", sr)

    fmt.Fprintf(w, "# HELP ytmp3_avg_processing_seconds Average processing time seconds.\n")
    fmt.Fprintf(w, "# TYPE ytmp3_avg_processing_seconds gauge\n")
    fmt.Fprintf(w, "ytmp3_avg_processing_seconds %.6f\n", avg)

    fmt.Fprintf(w, "# HELP ytmp3_redis_available Redis availability (0/1).\n")
    fmt.Fprintf(w, "# TYPE ytmp3_redis_available gauge\n")
    fmt.Fprintf(w, "ytmp3_redis_available %.0f\n", redisAvail)
}
