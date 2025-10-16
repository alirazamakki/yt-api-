package main

import (
    "bytes"
    "context"
    "bufio"
    "log"
    "os"
    "os/exec"
    "os/signal"
    "syscall"
    "runtime"
    "fmt"
    "strings"
    neturl "net/url"
    "time"
    "path/filepath"
    "net/http"
    "encoding/json"
    "regexp"
    "sync"
    "strconv"
    "io"
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

// External metadata helpers for /prepare
func fetchOEmbedMeta(videoURL string) (title string, author string, thumbnail string) {
    // Uses retry with backoff and circuit breaker
    if !oembedBreaker.Allow() { return "", "", "" }
    reqURL := OEmbedEndpoint + "?format=json&url=" + neturl.QueryEscape(videoURL)
    client := &http.Client{ Timeout: 5 * time.Second }
    var lastErr error
    for attempt := 1; attempt <= 3; attempt++ {
        resp, err := client.Get(reqURL)
        if err == nil && resp.StatusCode == 200 {
            defer resp.Body.Close()
            var data struct{ Title, AuthorName, ThumbnailURL string `json:"title" "author_name" "thumbnail_url"` }
            if err := json.NewDecoder(resp.Body).Decode(&data); err == nil {
                oembedBreaker.MarkSuccess()
                return data.Title, data.AuthorName, data.ThumbnailURL
            }
        }
        if resp != nil { resp.Body.Close() }
        lastErr = err
        time.Sleep(backoffForRetry(attempt))
    }
    oembedBreaker.MarkFailure(lastErr)
    return "", "", ""
}

func fetchDurationSeconds(videoURL string) int {
    if !durationBreaker.Allow() { return 0 }
    reqURL := DurationAPIEndpoint + "?url=" + neturl.QueryEscape(videoURL)
    client := &http.Client{ Timeout: 5 * time.Second }
    var lastErr error
    for attempt := 1; attempt <= 3; attempt++ {
        resp, err := client.Get(reqURL)
        if err == nil && resp.StatusCode == 200 {
            defer resp.Body.Close()
            var data struct{ Duration int `json:"duration"` }
            if err := json.NewDecoder(resp.Body).Decode(&data); err == nil {
                durationBreaker.MarkSuccess()
                return data.Duration
            }
        }
        if resp != nil { resp.Body.Close() }
        lastErr = err
        time.Sleep(backoffForRetry(attempt))
    }
    durationBreaker.MarkFailure(lastErr)
    return 0
}

// Simple circuit breaker
type CircuitBreaker struct {
    mu        sync.Mutex
    threshold int
    cooldown  time.Duration
    failures  int
    retryAt   time.Time
}

func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
    return &CircuitBreaker{threshold: threshold, cooldown: cooldown}
}

func (cb *CircuitBreaker) Allow() bool {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    return time.Now().After(cb.retryAt)
}

func (cb *CircuitBreaker) MarkSuccess() {
    cb.mu.Lock()
    cb.failures = 0
    cb.retryAt = time.Time{}
    cb.mu.Unlock()
}

func (cb *CircuitBreaker) MarkFailure(err error) {
    cb.mu.Lock()
    cb.failures++
    if cb.failures >= cb.threshold {
        cb.retryAt = time.Now().Add(cb.cooldown)
        cb.failures = 0
    }
    cb.mu.Unlock()
}

// Parse time strings like HH:MM:SS or MM:SS to duration
func parseHMS(s string) (time.Duration, bool) {
    if s == "" { return 0, false }
    parts := strings.Split(s, ":")
    if len(parts) == 2 { // MM:SS
        m, e1 := strconv.Atoi(parts[0])
        sec, e2 := strconv.Atoi(parts[1])
        if e1 == nil && e2 == nil { return time.Duration(m)*time.Minute + time.Duration(sec)*time.Second, true }
        return 0, false
    }
    if len(parts) == 3 { // HH:MM:SS
        h, e1 := strconv.Atoi(parts[0])
        m, e2 := strconv.Atoi(parts[1])
        sec, e3 := strconv.Atoi(parts[2])
        if e1 == nil && e2 == nil && e3 == nil { return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second, true }
        return 0, false
    }
    return 0, false
}

// Download with yt-dlp while parsing progress and updating session
func startYTDLPDownloadWithProgress(sess *ConversionSession, tmpNoExt string) error {
    // Build args similar to downloadAudioWithYTDLP but with progress output
    args := []string{"-f", "bestaudio[acodec!=none]/bestaudio", "-N", fmt.Sprintf("%d", YTDLPDownloadConcurrency),
        "-o", tmpNoExt + ".%(ext)s", "--no-playlist", "--no-warnings", "--newline"}
    if YTDLPCookies != "" {
        if strings.HasPrefix(YTDLPCookies, "browser:") {
            args = append(args, "--cookies-from-browser", strings.TrimPrefix(YTDLPCookies, "browser:"))
        } else { args = append(args, "--cookies", YTDLPCookies) }
    }
    if YTDLPExtraArgs != "" { args = append(args, strings.Fields(YTDLPExtraArgs)...)}
    args = append(args, sess.URL)

    ctxTimeout, cancel := context.WithTimeout(ctx, YTDLPDownloadTimeout)
    defer cancel()
    cmd := exec.CommandContext(ctxTimeout, "yt-dlp", args...)
    stderr, _ := cmd.StderrPipe()
    stdout, _ := cmd.StdoutPipe()
    if err := cmd.Start(); err != nil { return err }

    // Regex to parse percentage like "[download]  23.1%"
    rePct := regexp.MustCompile(`(?i)download\]\s+([0-9]{1,3}\.?[0-9]*)%`)

    scan := func(r io.Reader) {
        s := bufio.NewScanner(r)
        s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
        for s.Scan() {
            line := s.Text()
            if m := rePct.FindStringSubmatch(line); len(m) == 2 {
                if p, err := strconv.ParseFloat(m[1], 64); err == nil {
                    if p < 0 { p = 0 } else if p > 100 { p = 100 }
                    sess.DownloadProgress = int(p + 0.5)
                    sess.State = StateDownloading
                    sess.LastActivityAt = time.Now()
                    sess.UpdatedAt = time.Now()
                    saveSession(sess)
                }
            }
        }
    }
    go scan(stderr)
    go scan(stdout)
    if err := cmd.Wait(); err != nil { return err }
    return nil
}

// Convert with ffmpeg while parsing progress
func convertToMP3WithProgress(sess *ConversionSession, srcPath, outPath, startTime, endTime string, timeout time.Duration) error {
    if timeout <= 0 { timeout = FFmpegMinTimeout }
    if timeout > FFmpegMaxTimeout { timeout = FFmpegMaxTimeout }

    // Estimate total duration for progress
    var total time.Duration
    if d := sess.Meta.Duration; d > 0 { total = time.Duration(d) * time.Second }
    if st, ok := parseHMS(startTime); ok { if et, ok2 := parseHMS(endTime); ok2 && et > st { total = et - st } }
    if total <= 0 { total = 0 }

    args := []string{"-y", "-hide_banner", "-nostdin", "-v", "error", "-progress", "pipe:1"}
    if startTime != "" { args = append(args, "-ss", startTime) }
    args = append(args, "-i", srcPath)
    if endTime != "" { args = append(args, "-to", endTime) }
    args = append(args, "-vn", "-acodec", "libmp3lame", "-ar", "44100")
    if strings.EqualFold(FFmpegMode, "VBR") { args = append(args, "-q:a", fmt.Sprintf("%d", FFmpegVBRQ)) } else { args = append(args, "-b:a", FFmpegCBRBitrate) }
    if FFmpegThreads > 0 { args = append(args, "-threads", fmt.Sprintf("%d", FFmpegThreads)) }
    args = append(args, outPath)

    ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    cmd := exec.CommandContext(ctxTimeout, "ffmpeg", args...)
    stdout, _ := cmd.StdoutPipe()
    var stderr bytes.Buffer
    cmd.Stderr = &stderr

    if err := cmd.Start(); err != nil { return err }
    go func(){
        s := bufio.NewScanner(stdout)
        for s.Scan() {
            line := s.Text()
            // Parse out_time_ms=123456
            if strings.HasPrefix(line, "out_time_ms=") {
                val := strings.TrimPrefix(line, "out_time_ms=")
                if ms, err := strconv.ParseInt(val, 10, 64); err == nil && total > 0 {
                    cur := time.Duration(ms) * time.Microsecond // value is microseconds
                    pct := int(float64(cur) / float64(total) * 100.0)
                    if pct < 0 { pct = 0 } else if pct > 100 { pct = 100 }
                    sess.ConversionProgress = pct
                    sess.State = StateConverting
                    sess.LastActivityAt = time.Now()
                    sess.UpdatedAt = time.Now()
                    saveSession(sess)
                }
            }
        }
    }()
    err := cmd.Wait()
    if err != nil {
        return fmt.Errorf("ffmpeg error: %v | %s", err, strings.TrimSpace(stderr.String()))
    }
    return nil
}

// Background audio download and conversion for metadata endpoint
func startBackgroundAudioDownload(sess *ConversionSession) {
    if sess == nil { return }
    
    // Acquire download slot
    select {
    case downloadSlots <- struct{}{}:
        defer func(){ <-downloadSlots }()
    default:
        sess.State = StateFailed
        sess.Error = "server busy; too many concurrent downloads"
        sess.UpdatedAt = time.Now()
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
        return
    }
    
    sess.State = StateDownloading
    sess.UpdatedAt = time.Now()
    sess.LastActivityAt = time.Now()
    sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()

    _ = os.MkdirAll(ConversionsDir, 0o755)
    basePath := filepath.Join(ConversionsDir, sess.ID)

    // Use yt-dlp to download best audio format
    tmpNoExt := basePath
    if err := startYTDLPDownloadWithProgress(sess, tmpNoExt); err != nil {
        sess.State = StateFailed
        sess.Error = err.Error()
        sess.UpdatedAt = time.Now()
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
        return
    }
    
    // Find the downloaded file
    var srcPath string
    for _, ext := range []string{"m4a","webm","mp4","opus","ogg"} {
        p := tmpNoExt + "." + ext
        if _, err := os.Stat(p); err == nil { 
            srcPath = p; 
            sess.SourceExt = ext; 
            break 
        }
    }
    if srcPath == "" { 
        sess.State = StateFailed
        sess.Error = "downloaded file not found"
        sess.UpdatedAt = time.Now()
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
        return
    }
    
    sess.SourcePath = srcPath
    sess.State = StateDownloaded
    sess.UpdatedAt = time.Now()
    sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
    
    // Convert to MP3 using FFmpeg
    go convertDownloadedAudio(sess)
}

// Convert downloaded audio to MP3
func convertDownloadedAudio(sess *ConversionSession) {
    if sess == nil || sess.SourcePath == "" { return }
    
    // Acquire conversion slot
    select {
    case convertSlots <- struct{}{}:
        defer func(){ <-convertSlots }()
    default:
        sess.State = StateQueued
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
        // Retry after delay
        go func(){ time.Sleep(2 * time.Second); convertDownloadedAudio(sess) }()
        return
    }
    
    sess.State = StateConverting
    sess.UpdatedAt = time.Now()
    sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()

    // Convert to MP3
    outPath := filepath.Join(ConversionsDir, sess.ID+".mp3")
    var timeout time.Duration = FFmpegMinTimeout
    if sess.Meta.Duration > 0 {
        d := time.Duration(sess.Meta.Duration) * time.Second
        calc := d*2 + 3*time.Minute
        if calc > timeout { timeout = calc }
        if timeout > FFmpegMaxTimeout { timeout = FFmpegMaxTimeout }
    }

    // Use default quality (192k CBR)
    prevCBR := FFmpegCBRBitrate
    FFmpegCBRBitrate = "192k"
    
    err := convertToMP3WithProgress(sess, sess.SourcePath, outPath, "", "", timeout)
    
    // Restore global bitrate
    FFmpegCBRBitrate = prevCBR

    if err == nil && fileExists(outPath) {
        sess.OutputPath = outPath
        sess.State = StateCompleted
        sess.ConversionsCount++
        sess.UpdatedAt = time.Now()
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
        
        // Schedule deletion after TTL
        go func(id, path string){
            time.Sleep(ConvertedFileTTL)
            _ = os.Remove(path)
            sessions.Lock()
            if s, ok := sessions.m[id]; ok && s != nil && s.OutputPath == path { 
                s.OutputPath = "" 
            }
            sessions.Unlock()
        }(sess.ID, outPath)
    } else {
        sess.State = StateFailed
        if err != nil {
            sess.Error = err.Error()
        } else {
            sess.Error = "ffmpeg conversion failed"
        }
        sess.UpdatedAt = time.Now()
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
    }
}

// Helper function to check if file exists
func fileExists(path string) bool {
    _, err := os.Stat(path)
    return err == nil
}

// Background download using yt-dlp bestaudio to ConversionsDir/{id}.{ext}
func startBackgroundDownload(sess *ConversionSession) {
    if sess == nil { return }
    // Acquire download slot (drop if saturated to protect server)
    select {
    case downloadSlots <- struct{}{}:
        defer func(){ <-downloadSlots }()
    default:
        // Too many concurrent downloads; mark failed quickly to keep API responsive
        sess.State = StateFailed
        sess.Error = "server busy; too many concurrent downloads"
        sess.UpdatedAt = time.Now()
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
        return
    }
    sess.State = StateDownloading
    sess.UpdatedAt = time.Now()
    sess.LastActivityAt = time.Now()
    sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()

    _ = os.MkdirAll(ConversionsDir, 0o755)
    basePath := filepath.Join(ConversionsDir, sess.ID)

    // Use yt-dlp with progress piping
    tmpNoExt := basePath
    if err := startYTDLPDownloadWithProgress(sess, tmpNoExt); err != nil {
        sess.State = StateFailed
        sess.Error = err.Error()
        sess.UpdatedAt = time.Now()
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
        return
    }
    // Determine saved file extension
    var srcPath string
    for _, ext := range []string{"m4a","webm","mp4","opus","ogg"} {
        p := tmpNoExt + "." + ext
        if _, err := os.Stat(p); err == nil { srcPath = p; sess.SourceExt = ext; break }
    }
    if srcPath == "" { srcPath = tmpNoExt + ".m4a" }
    sess.SourcePath = srcPath
    // If a convert was queued meanwhile, kick it off
    queued := false
    sessions.Lock()
    if s, ok := sessions.m[sess.ID]; ok && s != nil && s.State == StateQueued {
        queued = true
        s.State = StateDownloaded
        s.SourcePath = sess.SourcePath
        s.SourceExt = sess.SourceExt
        s.UpdatedAt = time.Now()
        sessions.m[sess.ID] = s
        sess = s
    } else {
        sess.State = StateDownloaded
        sess.UpdatedAt = time.Now()
        sessions.m[sess.ID] = sess
    }
    sessions.Unlock()
    if queued {
        go startConversion(sess, sess.RequestedStart, sess.RequestedEnd)
    }
}

// Start ffmpeg conversion to MP3 with optional trimming and quality
func startConversion(sess *ConversionSession, startTime, endTime string) {
    if sess == nil || sess.SourcePath == "" { return }
    // Acquire conversion slot
    select {
    case convertSlots <- struct{}{}:
        defer func(){ <-convertSlots }()
    default:
        // Queue remains in converting state only after we can start; report queued
        sess.State = StateQueued
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
        // Try again shortly without blocking API threads
        go func(){ time.Sleep(2 * time.Second); startConversion(sess, startTime, endTime) }()
        return
    }
    sess.State = StateConverting
    sess.UpdatedAt = time.Now()
    sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()

    // Build ffmpeg args using convertStreamToMP3 helper by passing source path
    outPath := filepath.Join(ConversionsDir, sess.ID+".mp3")
    // Prepare a timeout based on metadata duration if available
    var timeout time.Duration = FFmpegMinTimeout
    if sess.Meta.Duration > 0 {
        d := time.Duration(sess.Meta.Duration) * time.Second
        calc := d*2 + 3*time.Minute
        if calc > timeout { timeout = calc }
        if timeout > FFmpegMaxTimeout { timeout = FFmpegMaxTimeout }
    }

    // Build quality override
    prevCBR := FFmpegCBRBitrate
    if sess.Quality != "" {
        switch sess.Quality {
        case Quality128: FFmpegCBRBitrate = "128k"
        case Quality192: FFmpegCBRBitrate = "192k"
        case Quality256: FFmpegCBRBitrate = "256k"
        case Quality320: FFmpegCBRBitrate = "320k"
        }
    }
    // Handle trim by constructing direct ffmpeg command when needed
    if startTime != "" || endTime != "" {
        // Custom command with -ss/-to then encode
        // Reuse convertStreamToMP3 after we slice via input arguments
        // For simplicity, run ffmpeg directly here
        args := []string{"-y", "-loglevel", "error", "-nostdin"}
        if startTime != "" { args = append(args, "-ss", startTime) }
        args = append(args, "-i", sess.SourcePath)
        if endTime != "" { args = append(args, "-to", endTime) }
        args = append(args, "-vn", "-acodec", "libmp3lame", "-ar", "44100", "-b:a", string(FFmpegCBRBitrate), outPath)
        _ = convertToMP3WithProgress(sess, sess.SourcePath, outPath, startTime, endTime, timeout)
    } else {
        _ = convertToMP3WithProgress(sess, sess.SourcePath, outPath, "", "", timeout)
    }
    // Restore global bitrate
    FFmpegCBRBitrate = prevCBR

    if _, err := os.Stat(outPath); err == nil {
        sess.OutputPath = outPath
        sess.State = StateCompleted
        sess.ConversionsCount++
        sess.UpdatedAt = time.Now()
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
        // schedule deletion of MP3 after TTL
        go func(id, path string){
            time.Sleep(ConvertedFileTTL)
            _ = os.Remove(path)
            sessions.Lock()
            if s, ok := sessions.m[id]; ok && s != nil && s.OutputPath == path { s.OutputPath = "" }
            sessions.Unlock()
        }(sess.ID, outPath)
    } else {
        sess.State = StateFailed
        sess.Error = "ffmpeg conversion failed"
        sess.UpdatedAt = time.Now()
        sessions.Lock(); sessions.m[sess.ID] = sess; sessions.Unlock()
    }
}

func runFFmpeg(args []string, deadline time.Time) error {
    // Simple wrapper that enforces timeout
    d := time.Until(deadline)
    if d <= 0 { d = FFmpegMinTimeout }
    ctxTimeout, cancel := context.WithTimeout(ctx, d)
    defer cancel()
    cmd := exec.CommandContext(ctxTimeout, "ffmpeg", args...)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("ffmpeg error: %v | %s", err, strings.TrimSpace(stderr.String()))
    }
    return nil
}

// Periodic cleanup for unconverted source files
func startSessionCleaner() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            now := time.Now()
            sessions.Lock()
            for id, s := range sessions.m {
                if s == nil { continue }
                if s.OutputPath == "" && s.SourcePath != "" && now.Sub(s.LastActivityAt) > UnconvertedFileTTL {
                    _ = os.Remove(s.SourcePath)
                    s.SourcePath = ""
                    if s.State == StateDownloading || s.State == StateDownloaded || s.State == StateQueued {
                        s.State = StateFailed
                        s.Error = "expired due to inactivity"
                    }
                }
                // Remove whole session if fully cleaned up and old
                if s.OutputPath == "" && s.SourcePath == "" && now.Sub(s.UpdatedAt) > 30*time.Minute {
                    delete(sessions.m, id)
                }
            }
            sessions.Unlock()
        case <-ctx.Done():
            return
        }
    }
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
