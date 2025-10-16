package main

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "sync/atomic"
    "time"

    "github.com/google/uuid"
    "strings"
    "strconv"
)

func handleExtract(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)

    if r.Method == http.MethodOptions {
        w.WriteHeader(http.StatusOK)
        return
    }
    if r.Method != http.MethodPost {
        http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
        return
    }

    var req Request
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }
    if req.URL == "" || len(req.URL) > MaxURLLength {
        http.Error(w, "Missing YouTube URL", http.StatusBadRequest)
        return
    }

    if !isValidYouTubeURL(req.URL) {
        http.Error(w, "Invalid YouTube URL", http.StatusBadRequest)
        return
    }

    // Canonicalize URL (supports Shorts, embed, youtu.be, mobile)
    var videoID string
    if canon, ok := canonicalizeYouTubeURL(req.URL); ok {
        req.URL = canon
        if vid, ok2 := extractYouTubeVideoID(canon); ok2 {
            videoID = vid
        }
    }

    logInfof("extract_received job_id=~pending url=%s", req.URL)
    // Idempotency key check
    if req.IdempotencyKey != "" {
        if jid, err := getJobIDByIdempotency(req.IdempotencyKey); err == nil && jid != "" {
            if j, err2 := getJobFromRedis(jid); err2 == nil && j != nil {
                w.Header().Set("Content-Type", "application/json")
                json.NewEncoder(w).Encode(map[string]interface{}{
                    "job_id": j.ID,
                    "status": string(j.Status),
                    "download_url": j.DownloadURL,
                    "check_status_endpoint": fmt.Sprintf("http://localhost:8080/status/%s", j.ID),
                    "canonical_url": j.URL,
                })
                return
            }
        }
    }

    // Prefer Redis-based dedupe first
    if jobIDFromURL, err := getJobIDByURL(req.URL); err == nil && jobIDFromURL != "" {
        if jobByRedis, err2 := getJobFromRedis(jobIDFromURL); err2 == nil && jobByRedis != nil && jobByRedis.Status == StatusCompleted {
            w.Header().Set("Content-Type", "application/json")
            json.NewEncoder(w).Encode(map[string]string{
                "job_id": jobByRedis.ID,
                "status": string(jobByRedis.Status),
                "download_url": jobByRedis.DownloadURL,
                "check_status_endpoint": fmt.Sprintf("http://localhost:8080/status/%s", jobByRedis.ID),
            })
            return
        }
    }
    existingJob := findJobByURL(req.URL)
    if existingJob != nil && existingJob.Status == StatusCompleted {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]string{
            "job_id": existingJob.ID,
            "status": string(existingJob.Status),
            "download_url": existingJob.DownloadURL,
            "check_status_endpoint": fmt.Sprintf("http://localhost:8080/status/%s", existingJob.ID),
        })
        return
    }

    jobID := uuid.New().String()
    job := &ConversionJob{
        ID:         jobID,
        URL:        req.URL,
        VideoID:    videoID,
        Status:     StatusPending,
        CreatedAt:  time.Now(),
        MaxRetries: MaxJobRetries,
        Priority:   1,
        CallbackURL: req.CallbackURL,
    }

    jobStore.Lock()
    jobStore.jobs[jobID] = job
    jobStore.Unlock()

    saveJobToRedis(job)
    _ = saveURLMapping(req.URL, jobID)
    if req.IdempotencyKey != "" {
        _ = saveIdempotencyKey(req.IdempotencyKey, jobID)
    }
    atomic.AddInt64(&queuedJobs, 1)
    // derive queue length from channel length for accuracy
    qlen := len(jobQueue)
    if qlen < 0 { qlen = 0 }
    logInfof("queued job_id=%s queue_len=%d", jobID, qlen)

    resultCh := registerJobWaiter(jobID)

    select {
    case jobQueue <- job:
        w.Header().Set("Content-Type", "application/json")
        select {
        case doneJob := <-resultCh:
            if doneJob.Status == StatusCompleted {
                json.NewEncoder(w).Encode(map[string]string{
                    "job_id": jobID,
                    "status": string(doneJob.Status),
                    "download_url": doneJob.DownloadURL,
                    "check_status_endpoint": fmt.Sprintf("http://localhost:8080/status/%s", jobID),
                    "canonical_url": job.URL,
                })
            } else {
                // Do not surface immediate error; let background retries handle it.
                json.NewEncoder(w).Encode(map[string]interface{}{
                    "job_id": jobID,
                    "status": string(StatusProcessing),
                    "check_status_endpoint": fmt.Sprintf("http://localhost:8080/status/%s", jobID),
                    "canonical_url": job.URL,
                })
            }
        case <-time.After(FastPathWait):
            unregisterJobWaiter(jobID, resultCh)
            json.NewEncoder(w).Encode(map[string]string{
                "job_id": jobID,
                "status": string(job.Status),
                "check_status_endpoint": fmt.Sprintf("http://localhost:8080/status/%s", jobID),
                "canonical_url": job.URL,
            })
        }
    default:
        unregisterJobWaiter(jobID, resultCh)
        jobStore.Lock()
        delete(jobStore.jobs, jobID)
        jobStore.Unlock()
        atomic.AddInt64(&queuedJobs, -1)
        w.Header().Set("Retry-After", "1")
        http.Error(w, "Server busy, please try again later.", http.StatusServiceUnavailable)
    }
}

// POST /metadata
// Fast metadata endpoint that returns basic info within 1-2 seconds and starts background download
func handleMetadata(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    if r.Method == http.MethodOptions { w.WriteHeader(http.StatusOK); return }
    if r.Method != http.MethodPost { http.Error(w, "Invalid request method", http.StatusMethodNotAllowed); return }

    var req PrepareRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "Invalid JSON", http.StatusBadRequest); return }
    if req.URL == "" || !isValidYouTubeURL(req.URL) { http.Error(w, "Invalid YouTube URL", http.StatusBadRequest); return }

    // Canonicalize URL
    if canon, ok := canonicalizeYouTubeURL(req.URL); ok { req.URL = canon }

    // Generate unique session ID
    sessionID := "meta_" + uuid.New().String()
    
    // Create session for tracking
    sess := &ConversionSession{ 
        ID: sessionID, 
        URL: req.URL, 
        State: StateFetching, 
        CreatedAt: time.Now(), 
        UpdatedAt: time.Now(), 
        LastActivityAt: time.Now(),
    }

    // Fetch metadata via OEmbed + duration API (fast)
    meta := MetaLite{}
    title, author, thumb := fetchOEmbedMeta(req.URL)
    durSec := fetchDurationSeconds(req.URL)
    meta.Title = title
    meta.Channel = author
    meta.Duration = durSec
    meta.Thumbnail = thumb
    sess.Meta = meta

    // Update session state
    sess.State = StateCreated
    sess.UpdatedAt = time.Now()
    saveSession(sess)

    // Start background download immediately (non-blocking)
    go startBackgroundDownload(sess)

    // Return fast response with metadata
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "session_id": sessionID,
        "status": "metadata_ready",
        "metadata": meta,
        "message": "Metadata fetched. Audio is downloading in background.",
        "check_status_endpoint": fmt.Sprintf("http://localhost:8080/status/%s", sessionID),
    })
}

// POST /prepare
// Fetches basic metadata without yt-dlp and starts a background download of best audio
func handlePrepare(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    if r.Method == http.MethodOptions { w.WriteHeader(http.StatusOK); return }
    if r.Method != http.MethodPost { http.Error(w, "Invalid request method", http.StatusMethodNotAllowed); return }

    var req PrepareRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "Invalid JSON", http.StatusBadRequest); return }
    if req.URL == "" || !isValidYouTubeURL(req.URL) { http.Error(w, "Invalid YouTube URL", http.StatusBadRequest); return }

    // Canonicalize
    if canon, ok := canonicalizeYouTubeURL(req.URL); ok { req.URL = canon }

    convID := "conv_" + uuid.New().String()
    sess := &ConversionSession{ ID: convID, URL: req.URL, State: StatePreparing, CreatedAt: time.Now(), UpdatedAt: time.Now(), LastActivityAt: time.Now() }

    // Fetch metadata via OEmbed + duration API
    sess.State = StateFetching
    meta := MetaLite{}
    title, author, thumb := fetchOEmbedMeta(req.URL)
    durSec := fetchDurationSeconds(req.URL)
    meta.Title = title
    meta.Channel = author
    meta.Duration = durSec
    meta.Thumbnail = thumb
    sess.Meta = meta

    // Persist (in-memory + Redis if available)
    saveSession(sess)

    // Start background download with progress piping
    go startBackgroundDownload(sess)

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(PrepareResponse{
        ConversionID: convID,
        Status: string(StateCreated),
        Metadata: meta,
        Message: "Metadata fetched successfully. Stream is downloading in background.",
    })
}

// POST /convert-audio
// Convert the downloaded audio to MP3 with specified quality and optional trimming
func handleConvertAudio(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    if r.Method == http.MethodOptions { w.WriteHeader(http.StatusOK); return }
    if r.Method != http.MethodPost { http.Error(w, "Invalid request method", http.StatusMethodNotAllowed); return }

    var req ConvertRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "Invalid JSON", http.StatusBadRequest); return }
    if req.ConversionID == "" { http.Error(w, "Missing conversion_id", http.StatusBadRequest); return }
    if req.Quality == "" { req.Quality = Quality320 }

    sess, ok := getSession(req.ConversionID)
    if !ok || sess == nil { http.Error(w, "Session not found", http.StatusNotFound); return }

    // Check if audio is downloaded and ready
    if sess.State != StateDownloaded {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]interface{}{
            "session_id": sess.ID,
            "status": string(sess.State),
            "message": "Audio not ready for conversion yet",
        })
        return
    }

    // Start conversion
    sess.Quality = req.Quality
    sess.RequestedStart = req.StartTime
    sess.RequestedEnd = req.EndTime
    sess.LastActivityAt = time.Now()
    sess.UpdatedAt = time.Now()
    saveSession(sess)

    go startConversion(sess, req.StartTime, req.EndTime)

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "session_id": sess.ID,
        "status": "converting",
        "message": "Conversion started",
    })
}

// POST /convert
func handleConvert(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    if r.Method == http.MethodOptions { w.WriteHeader(http.StatusOK); return }
    if r.Method != http.MethodPost { http.Error(w, "Invalid request method", http.StatusMethodNotAllowed); return }

    var req ConvertRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "Invalid JSON", http.StatusBadRequest); return }
    if req.ConversionID == "" { http.Error(w, "Missing conversion_id", http.StatusBadRequest); return }
    if req.Quality == "" { req.Quality = Quality320 }

    sess, ok := getSession(req.ConversionID)
    if !ok || sess == nil { http.Error(w, "Conversion not found", http.StatusNotFound); return }

    // Enforce per-ID conversion limit
    if sess.ConversionsCount >= 2 {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusTooManyRequests)
        json.NewEncoder(w).Encode(map[string]string{"error": "Conversion limit reached for this ID."})
        return
    }

    sess.Quality = req.Quality
    sess.LastActivityAt = time.Now()

    // If still downloading, queue for conversion
    if sess.State == StateDownloading {
        sess.State = StateQueued
        sess.RequestedStart = req.StartTime
        sess.RequestedEnd = req.EndTime
        sess.UpdatedAt = time.Now()
        saveSession(sess)
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]interface{}{
            "conversion_id": sess.ID,
            "status": string(StateQueued),
            "message": "Stream is downloading. Conversion will start automatically.",
        })
        return
    }

    // If downloaded, start conversion immediately
    if sess.State == StateDownloaded {
        go startConversion(sess, req.StartTime, req.EndTime)
        sess.State = StateConverting
        sess.UpdatedAt = time.Now()
        saveSession(sess)
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]interface{}{
            "conversion_id": sess.ID,
            "status": string(StateConverting),
            "message": "Conversion started.",
        })
        return
    }

    // If already converting or completed, return state
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "conversion_id": sess.ID,
        "status": string(sess.State),
    })
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)

    if r.Method == http.MethodOptions {
        w.WriteHeader(http.StatusOK)
        return
    }

    jobID := filepath.Base(r.URL.Path)
    if jobID == "" {
        http.Error(w, "Missing job ID", http.StatusBadRequest)
        return
    }

    // Prefer new conversion session if present
    sessions.RLock()
    if sess, ok := sessions.m[jobID]; ok && sess != nil {
        sessions.RUnlock()
        resp := map[string]interface{}{
            "session_id": sess.ID,
            "status": string(sess.State),
        }
        
        // Add metadata if available
        if sess.Meta.Title != "" {
            resp["metadata"] = map[string]interface{}{
                "title": sess.Meta.Title,
                "channel": sess.Meta.Channel,
                "duration": sess.Meta.Duration,
                "thumbnail": sess.Meta.Thumbnail,
            }
        }
        
        if sess.State == StateDownloading {
            resp["download_progress"] = sess.DownloadProgress
        }
        if sess.State == StateConverting {
            resp["conversion_progress"] = sess.ConversionProgress
        }
        if sess.State == StateCompleted && sess.OutputPath != "" {
            resp["download_url"] = fmt.Sprintf("http://localhost:8080/download/%s.mp3", sess.ID)
            if resp["metadata"] == nil {
                resp["metadata"] = map[string]interface{}{}
            }
            if metadata, ok := resp["metadata"].(map[string]interface{}); ok {
                metadata["quality"] = string(sess.Quality)
            }
        }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(resp)
        return
    }
    sessions.RUnlock()

    job, err := getJobFromRedis(jobID)
    if err != nil || job == nil {
        jobStore.RLock()
        jobMem, exists := jobStore.jobs[jobID]
        jobStore.RUnlock()
        if !exists {
            http.Error(w, "Job not found", http.StatusNotFound)
            return
        }
        job = jobMem
    }

    response := struct {
        JobID       string    `json:"job_id"`
        Status      JobStatus `json:"status"`
        Progress    string    `json:"progress,omitempty"`
        DownloadURL string    `json:"download_url,omitempty"`
        Error       string    `json:"error,omitempty"`
        Metadata    *Metadata `json:"metadata,omitempty"`
        CreatedAt   time.Time `json:"created_at"`
        CompletedAt time.Time `json:"completed_at,omitempty"`
    }{
        JobID:       job.ID,
        Status:      job.Status,
        DownloadURL: job.DownloadURL,
        Error:       job.Error,
        Metadata:    job.Metadata,
        CreatedAt:   job.CreatedAt,
        CompletedAt: job.CompletedAt,
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)

    if r.Method == http.MethodOptions {
        w.WriteHeader(http.StatusOK)
        return
    }

    filenameWithExt := filepath.Base(r.URL.Path)
    if !strings.HasSuffix(filenameWithExt, ".mp3") {
        http.Error(w, "Invalid filename", http.StatusBadRequest)
        return
    }
    jobID := filenameWithExt[:len(filenameWithExt)-len(".mp3")]

    // New conversion session path
    sessions.RLock()
    if sess, ok := sessions.m[jobID]; ok && sess != nil {
        // Check if we have a completed conversion
        if sess.State == StateCompleted && sess.OutputPath != "" {
            path := sess.OutputPath
            sessions.RUnlock()
            file, err := os.Open(path)
            if err != nil {
                http.Error(w, "Error opening file", http.StatusInternalServerError)
                return
            }
            defer file.Close()

            fi, _ := file.Stat()
            size := fi.Size()
            w.Header().Set("Accept-Ranges", "bytes")
            w.Header().Set("Content-Type", "audio/mpeg")
            w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filenameWithExt))
            w.Header().Set("Cache-Control", "public, max-age=3600")

            if rng := r.Header.Get("Range"); rng != "" {
                if strings.HasPrefix(rng, "bytes=") {
                    parts := strings.TrimPrefix(rng, "bytes=")
                    if strings.HasSuffix(parts, "-") {
                        startStr := strings.TrimSuffix(parts, "-")
                        if start, err := strconv.ParseInt(startStr, 10, 64); err == nil && start < size {
                            w.WriteHeader(http.StatusPartialContent)
                            w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, size-1, size))
                            file.Seek(start, 0)
                            io.Copy(w, file)
                            return
                        }
                    }
                }
            }
            w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
            io.Copy(w, file)
            return
        } else {
            // Session exists but not ready for download
            sessions.RUnlock()
            http.Error(w, "File not ready for download", http.StatusNotFound)
            return
        }
    }
    sessions.RUnlock()

    job, err := getJobFromRedis(jobID)
    if err != nil || job == nil {
        jobStore.RLock()
        job, exists := jobStore.jobs[jobID]
        jobStore.RUnlock()
        if !exists || job.Status != StatusCompleted {
            http.Error(w, "File not found or conversion not completed", http.StatusNotFound)
            return
        }
    }

    if job.FilePath == "" {
        http.Error(w, "File path not available", http.StatusInternalServerError)
        return
    }

    // Mark download in progress to avoid deletion during streaming
    downloadTrackers.Lock()
    downloadTrackers.inProgress[job.ID]++
    downloadTrackers.Unlock()

    file, err := os.Open(job.FilePath)
    if err != nil {
        downloadTrackers.Lock()
        downloadTrackers.inProgress[job.ID]--
        if downloadTrackers.inProgress[job.ID] <= 0 { delete(downloadTrackers.inProgress, job.ID) }
        downloadTrackers.Unlock()
        http.Error(w, "Error opening file", http.StatusInternalServerError)
        return
    }
    defer func() {
        file.Close()
        downloadTrackers.Lock()
        downloadTrackers.inProgress[job.ID]--
        if downloadTrackers.inProgress[job.ID] <= 0 { delete(downloadTrackers.inProgress, job.ID) }
        downloadTrackers.Unlock()
    }()

    // Range support
    fi, _ := file.Stat()
    size := fi.Size()
    w.Header().Set("Accept-Ranges", "bytes")
    w.Header().Set("Content-Type", "audio/mpeg")
    w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filenameWithExt))
    w.Header().Set("Cache-Control", "public, max-age=3600")

    if rng := r.Header.Get("Range"); rng != "" {
        // Simple bytes=START-
        if strings.HasPrefix(rng, "bytes=") {
            parts := strings.TrimPrefix(rng, "bytes=")
            if strings.HasSuffix(parts, "-") {
                startStr := strings.TrimSuffix(parts, "-")
                if start, err := strconv.ParseInt(startStr, 10, 64); err == nil && start < size {
                    w.WriteHeader(http.StatusPartialContent)
                    w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, size-1, size))
                    file.Seek(start, 0)
                    io.Copy(w, file)
                    return
                }
            }
        }
    }

    // Full body
    w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
    io.Copy(w, file)

    // Schedule deletion 10 minutes after first successful download
    if job.FirstDownloadedAt.IsZero() {
        job.FirstDownloadedAt = time.Now()
        saveJobToRedis(job)
        // Schedule deletion if not already scheduled
        downloadTrackers.Lock()
        already := downloadTrackers.scheduled[job.ID]
        if !already { downloadTrackers.scheduled[job.ID] = true }
        downloadTrackers.Unlock()
        if !already {
            go scheduleSafeDeletion(job)
        }
    }
}

// Simple docs pages
func handleDocs(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    if r.Method != http.MethodGet { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>YT MP3 API Docs</title><style>body{font-family:sans-serif;max-width:900px;margin:2rem auto;padding:0 1rem;}</style></head><body>
    <h1>YouTube to MP3 API - Documentation</h1>
    <p>High-level guide for backend integration.</p>
    <h2>Endpoints</h2>
    <ul>
      <li><code>POST /extract</code> - Start conversion. Body: { url, idempotency_key?, callback_url? }</li>
      <li><code>POST /metadata</code> - Fast metadata + background download. Body: { url }</li>
      <li><code>POST /convert-audio</code> - Convert downloaded audio. Body: { conversion_id, quality?, start_time?, end_time? }</li>
      <li><code>GET /status/{session_id}</code> - Check session status.</li>
      <li><code>GET /download/{session_id}.mp3</code> - Download MP3 (Range supported).</li>
      <li><code>GET /health</code>, <code>/metrics</code>, <code>/stats</code> - Monitoring.</li>
    </ul>
    <h2>Auth</h2>
    <p>If enabled, send <code>X-API-Key: &lt;your_key&gt;</code> header.</p>
    <h2>Notes</h2>
    <ul>
      <li>Provide valid YouTube URL (shorts and embed supported).</li>
      <li>Repeated requests for same video are deduped.</li>
      <li>Files are short-lived and may be deleted ~10 minutes after completion.</li>
    </ul>
    <p>Frontend-focused docs: <a href="/docs/frontend">/docs/frontend</a></p>
    </body></html>`)
}

func handleDocsFrontend(w http.ResponseWriter, r *http.Request) {
    enableCORS(w, r)
    if r.Method != http.MethodGet { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>Frontend Integration</title><style>body{font-family:sans-serif;max-width:900px;margin:2rem auto;padding:0 1rem;}</style></head><body>
    <h1>Frontend Integration</h1>
    <p>Use fetch with CORS. For fast metadata + background processing:</p>
    <pre><code>// Step 1: Get metadata and start background download
fetch('/metadata',{method:'POST',headers:{'Content-Type':'application/json','X-API-Key':'YOUR_KEY'},body:JSON.stringify({url})})
 .then(r=>r.json())
 .then(({session_id,metadata})=>{
   console.log('Metadata:', metadata);
   return pollStatus(session_id);
 })

// Step 2: Poll status until downloaded
function pollStatus(sessionId) {
  return fetch('/status/' + sessionId)
   .then(r=>r.json())
   .then(data=>{
     if(data.status==='downloaded') {
       // Step 3: Convert to MP3
       return fetch('/convert-audio',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({conversion_id:sessionId,quality:'320'})})
        .then(r=>r.json())
        .then(()=>pollConversionStatus(sessionId));
     }
     return new Promise(resolve=>setTimeout(()=>resolve(pollStatus(sessionId)), 2000));
   });
}

// Step 4: Poll conversion status
function pollConversionStatus(sessionId) {
  return fetch('/status/' + sessionId)
   .then(r=>r.json())
   .then(data=>{
     if(data.status==='completed') {
       window.location.href = '/download/' + sessionId + '.mp3';
     } else {
       setTimeout(()=>pollConversionStatus(sessionId), 2000);
     }
   });
}</code></pre>
    <p>For simple one-step conversion, use <code>/extract</code> endpoint.</p>
    </body></html>`)
}

// Minimal admin dashboard (basic auth protected)
func handleAdmin(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>Admin</title>
    <style>body{font-family:sans-serif;max-width:1100px;margin:2rem auto;padding:0 1rem;}table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:6px}</style>
    <script>
    async function refresh(){
      const h = await fetch('/health',{headers:{'Authorization':localStorage.auth||''}}).then(r=>r.json()).catch(()=>({}));
      const m = await fetch('/metrics',{headers:{'Authorization':localStorage.auth||''}}).then(r=>r.json()).catch(()=>({}));
      document.getElementById('health').textContent = JSON.stringify(h,null,2);
      document.getElementById('metrics').textContent = JSON.stringify(m,null,2);
    }
    setInterval(refresh, 3000);
    window.onload=refresh;
    </script></head><body>
    <h1>Admin Dashboard</h1>
    <p>Live server state, health, and metrics.</p>
    <h2>Health</h2><pre id="health">loading...</pre>
    <h2>Metrics</h2><pre id="metrics">loading...</pre>
    </body></html>`)
}
