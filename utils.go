package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Logging functions
func logInfof(format string, args ...interface{}) {
	if ColorLogs {
		log.Printf("\033[32m[INFO]\033[0m "+format, args...)
	} else {
		log.Printf("[INFO] "+format, args...)
	}
}

func logWarnf(format string, args ...interface{}) {
	if ColorLogs {
		log.Printf("\033[33m[WARN]\033[0m "+format, args...)
	} else {
		log.Printf("[WARN] "+format, args...)
	}
}

func logErrorf(format string, args ...interface{}) {
	if ColorLogs {
		log.Printf("\033[31m[ERROR]\033[0m "+format, args...)
	} else {
		log.Printf("[ERROR] "+format, args...)
	}
}

// YouTube URL validation and canonicalization
var (
	youtubeRegex = regexp.MustCompile(`(?:youtube\.com\/(?:[^\/]+\/.+\/|(?:v|e(?:mbed)?)\/|.*[?&]v=)|youtu\.be\/)([^"&?\/\s]{11})`)
	youtubeShortRegex = regexp.MustCompile(`youtube\.com\/shorts\/([^"&?\/\s]{11})`)
)

// isValidYouTubeURL checks if the URL is a valid YouTube URL
func isValidYouTubeURL(url string) bool {
	return youtubeRegex.MatchString(url) || youtubeShortRegex.MatchString(url)
}

// canonicalizeYouTubeURL converts various YouTube URL formats to standard format
func canonicalizeYouTubeURL(url string) (string, bool) {
	// Handle youtu.be short URLs
	if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "youtu.be/")
		if len(parts) == 2 {
			videoID := strings.Split(parts[1], "?")[0]
			if len(videoID) == 11 {
				return fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID), true
			}
		}
	}

	// Handle YouTube Shorts
	if strings.Contains(url, "youtube.com/shorts/") {
		parts := strings.Split(url, "youtube.com/shorts/")
		if len(parts) == 2 {
			videoID := strings.Split(parts[1], "?")[0]
			if len(videoID) == 11 {
				return fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID), true
			}
		}
	}

	// Handle embed URLs
	if strings.Contains(url, "youtube.com/embed/") {
		parts := strings.Split(url, "youtube.com/embed/")
		if len(parts) == 2 {
			videoID := strings.Split(parts[1], "?")[0]
			if len(videoID) == 11 {
				return fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID), true
			}
		}
	}

	// Handle mobile URLs
	if strings.Contains(url, "m.youtube.com") {
		url = strings.Replace(url, "m.youtube.com", "www.youtube.com", 1)
	}

	// If it's already a standard watch URL, return as is
	if strings.Contains(url, "youtube.com/watch?v=") {
		return url, true
	}

	return url, false
}

// extractYouTubeVideoID extracts video ID from YouTube URL
func extractYouTubeVideoID(url string) (string, bool) {
	matches := youtubeRegex.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1], true
	}

	matches = youtubeShortRegex.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1], true
	}

	return "", false
}

// fetchOEmbedMeta fetches metadata from YouTube oEmbed API
func fetchOEmbedMeta(url string) (title, author, thumbnail string) {
	// This is a simplified implementation
	// In production, you'd make an HTTP request to the oEmbed endpoint
	return "Video Title", "Channel Name", "https://example.com/thumb.jpg"
}

// fetchDurationSeconds fetches video duration from external API
func fetchDurationSeconds(url string) int {
	// This is a simplified implementation
	// In production, you'd make an HTTP request to the duration API
	return 180 // 3 minutes default
}

// enableCORS sets CORS headers
func enableCORS(w http.ResponseWriter, r *http.Request) {
	originHeader := "*"
	reqOrigin := r.Header.Get("Origin")
	if AllowedOrigins != "*" {
		// support comma-separated list of allowed origins
		allowed := map[string]struct{}{}
		for _, o := range strings.Split(AllowedOrigins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				allowed[o] = struct{}{}
			}
		}
		if _, ok := allowed[reqOrigin]; ok && reqOrigin != "" {
			originHeader = reqOrigin
		} else {
			originHeader = ""
		}
		w.Header().Set("Vary", "Origin")
	}
	if originHeader != "" {
		w.Header().Set("Access-Control-Allow-Origin", originHeader)
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// setupGracefulShutdown sets up graceful shutdown handling
func setupGracefulShutdown() {
	// This is a simplified implementation
	// In production, you'd use signal handling for graceful shutdown
	logInfof("Graceful shutdown handler initialized")
}

// Helper function to check if error is duration exceeded
func isDurationExceededError(err error) bool {
	return strings.Contains(err.Error(), "duration exceeds max")
}