package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// startBackgroundDownload starts background download for a session
func startBackgroundDownload(session *ConversionSession) {
	// Acquire download slot
	downloadSlots <- struct{}{}
	defer func() { <-downloadSlots }()

	logInfof("background_download_start session_id=%s url=%s", session.ID, session.URL)

	// Update session state
	session.State = StateDownloading
	session.UpdatedAt = time.Now()
	saveSession(session)

	// Create conversions directory
	if err := os.MkdirAll(ConversionsDir, os.ModePerm); err != nil {
		session.State = StateFailed
		session.Error = fmt.Sprintf("Error creating conversions directory: %v", err)
		session.UpdatedAt = time.Now()
		saveSession(session)
		return
	}

	// Download audio
	sourcePath := filepath.Join(ConversionsDir, session.ID+"_src")
	err := downloadAudioWithYTDLP(session.URL, sourcePath, "m4a")
	if err != nil {
		logErrorf("background_download_failed session_id=%s err=%v", session.ID, err)
		session.State = StateFailed
		session.Error = fmt.Sprintf("Download failed: %v", err)
		session.UpdatedAt = time.Now()
		saveSession(session)
		return
	}

	// Find downloaded file
	var actualSourcePath string
	for _, ext := range []string{"m4a", "webm", "mp4"} {
		p := sourcePath + "." + ext
		if _, err := os.Stat(p); err == nil {
			actualSourcePath = p
			break
		}
	}

	if actualSourcePath == "" {
		session.State = StateFailed
		session.Error = "Downloaded file not found"
		session.UpdatedAt = time.Now()
		saveSession(session)
		return
	}

	// Update session with source path
	session.SourcePath = actualSourcePath
	session.SourceExt = filepath.Ext(actualSourcePath)
	session.State = StateDownloaded
	session.DownloadProgress = 100
	session.UpdatedAt = time.Now()
	saveSession(session)

	logInfof("background_download_completed session_id=%s source=%s", session.ID, actualSourcePath)

	// If there's a queued conversion request, start it
	if session.State == StateQueued {
		go startConversion(session, session.RequestedStart, session.RequestedEnd)
	}
}

// startConversion starts conversion for a session
func startConversion(session *ConversionSession, startTime, endTime string) {
	// Acquire conversion slot
	convertSlots <- struct{}{}
	defer func() { <-convertSlots }()

	logInfof("conversion_start session_id=%s quality=%s", session.ID, session.Quality)

	// Update session state
	session.State = StateConverting
	session.UpdatedAt = time.Now()
	saveSession(session)

	// Create output path
	outputPath := filepath.Join(ConversionsDir, session.ID+".mp3")

	// Determine ffmpeg timeout based on duration
	ffTimeout := FFmpegMinTimeout
	if session.Meta.Duration > 0 {
		calc := time.Duration(session.Meta.Duration*2)*time.Second + 3*time.Minute
		if calc > ffTimeout {
			ffTimeout = calc
		}
	}

	// Convert to MP3
	var err error
	if session.SourcePath != "" {
		// Convert from local file
		err = convertLocalFileToMP3(session.SourcePath, outputPath, ffTimeout, session.Quality, startTime, endTime)
	} else {
		// Stream conversion (fallback)
		audioURL, _, err2 := getAudioStreamFromYTDLP(session.URL)
		if err2 != nil {
			err = err2
		} else {
			err = convertStreamToMP3(audioURL, outputPath, ffTimeout)
		}
	}

	if err != nil {
		logErrorf("conversion_failed session_id=%s err=%v", session.ID, err)
		session.State = StateFailed
		session.Error = fmt.Sprintf("Conversion failed: %v", err)
		session.UpdatedAt = time.Now()
		saveSession(session)
		return
	}

	// Update session with output path
	session.OutputPath = outputPath
	session.State = StateCompleted
	session.ConversionProgress = 100
	session.ConversionsCount++
	session.UpdatedAt = time.Now()
	saveSession(session)

	// Update counters
	atomic.AddInt64(&completedJobs, 1)

	logInfof("conversion_completed session_id=%s output=%s", session.ID, outputPath)

	// Schedule cleanup
	go scheduleSessionCleanup(session)
}

// convertLocalFileToMP3 converts a local file to MP3
func convertLocalFileToMP3(inputPath, outputPath string, timeout time.Duration, quality ConversionQuality, startTime, endTime string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"-y", "-loglevel", "error", "-nostdin", "-i", inputPath, "-vn", "-acodec", "libmp3lame", "-ar", "44100"}

	// Add quality settings
	if strings.EqualFold(FFmpegMode, "VBR") {
		args = append(args, "-q:a", fmt.Sprintf("%d", FFmpegVBRQ))
	} else {
		// Use quality-specific bitrate
		bitrate := getBitrateForQuality(quality)
		args = append(args, "-b:a", bitrate)
	}

	// Add time range if specified
	if startTime != "" {
		args = append(args, "-ss", startTime)
	}
	if endTime != "" {
		args = append(args, "-to", endTime)
	}

	if FFmpegThreads > 0 {
		args = append(args, "-threads", fmt.Sprintf("%d", FFmpegThreads))
	}

	args = append(args, outputPath)

	cmd := exec.CommandContext(ctxTimeout, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg error: %v | %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// getBitrateForQuality returns bitrate string for quality
func getBitrateForQuality(quality ConversionQuality) string {
	switch quality {
	case Quality128:
		return "128k"
	case Quality192:
		return "192k"
	case Quality256:
		return "256k"
	case Quality320:
		return "320k"
	default:
		return FFmpegCBRBitrate
	}
}

// scheduleSessionCleanup schedules cleanup for a session
func scheduleSessionCleanup(session *ConversionSession) {
	// Wait for TTL
	time.Sleep(ConvertedFileTTL)

	// Check if session still exists and is completed
	if sess, exists := getSession(session.ID); exists && sess.State == StateCompleted {
		// Clean up files
		if session.SourcePath != "" {
			os.Remove(session.SourcePath)
		}
		if session.OutputPath != "" {
			os.Remove(session.OutputPath)
		}

		// Delete session
		deleteSession(session.ID)
		logInfof("session_cleanup_completed session_id=%s", session.ID)
	}
}

// startSessionCleaner starts background session cleaner
func startSessionCleaner() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cleanupInactiveSessions()
	}
}

// cleanupInactiveSessions cleans up inactive sessions
func cleanupInactiveSessions() {
	now := time.Now()
	sessions.RLock()
	var toDelete []string
	for id, session := range sessions.m {
		// Clean up sessions that have been inactive for too long
		if now.Sub(session.LastActivityAt) > UnconvertedFileTTL && session.State != StateCompleted {
			toDelete = append(toDelete, id)
		}
	}
	sessions.RUnlock()

	for _, id := range toDelete {
		if session, exists := getSession(id); exists {
			// Clean up files
			if session.SourcePath != "" {
				os.Remove(session.SourcePath)
			}
			if session.OutputPath != "" {
				os.Remove(session.OutputPath)
			}
			deleteSession(id)
			logInfof("inactive_session_cleaned session_id=%s", id)
		}
	}
}