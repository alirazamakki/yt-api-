package main

import "time"

// ConversionQuality represents audio quality options
type ConversionQuality string

const (
	Quality128 ConversionQuality = "128"
	Quality192 ConversionQuality = "192"
	Quality256 ConversionQuality = "256"
	Quality320 ConversionQuality = "320"
)

// ConversionState represents the state of a conversion session
type ConversionState string

const (
	StatePreparing  ConversionState = "preparing"
	StateFetching   ConversionState = "fetching_metadata"
	StateCreated     ConversionState = "created"
	StateDownloading ConversionState = "downloading"
	StateDownloaded  ConversionState = "downloaded"
	StateQueued      ConversionState = "queued_for_conversion"
	StateConverting  ConversionState = "converting"
	StateCompleted   ConversionState = "completed"
	StateFailed      ConversionState = "failed"
)

// MetaLite represents lightweight metadata
type MetaLite struct {
	Title     string  `json:"title"`
	Channel   string  `json:"channel"`
	Duration  int     `json:"duration"`
	Thumbnail string  `json:"thumbnail"`
}

// PrepareRequest represents the request for /prepare endpoint
type PrepareRequest struct {
	URL string `json:"url"`
}

// PrepareResponse represents the response for /prepare endpoint
type PrepareResponse struct {
	ConversionID string   `json:"conversion_id"`
	Status       string   `json:"status"`
	Metadata     MetaLite `json:"metadata"`
	Message      string   `json:"message"`
}

// ConvertRequest represents the request for /convert endpoint
type ConvertRequest struct {
	ConversionID string            `json:"conversion_id"`
	Quality      ConversionQuality `json:"quality"`
	StartTime    string            `json:"start_time,omitempty"`
	EndTime      string            `json:"end_time,omitempty"`
}

// ConversionSession represents a conversion session
type ConversionSession struct {
	ID                 string            `json:"id"`
	URL                string            `json:"url"`
	State              ConversionState   `json:"state"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	SourcePath         string            `json:"source_path"`
	SourceExt          string            `json:"source_ext"`
	DownloadProgress   int               `json:"download_progress"`
	ConversionProgress int               `json:"conversion_progress"`
	ConversionsCount   int               `json:"conversions_count"`
	LastActivityAt     time.Time         `json:"last_activity_at"`
	OutputPath         string            `json:"output_path"`
	Quality            ConversionQuality `json:"quality"`
	Error              string            `json:"error"`
	Meta               MetaLite          `json:"metadata"`
	// Deferred conversion request parameters (for queued conversion)
	RequestedStart     string            `json:"-"`
	RequestedEnd       string            `json:"-"`
}

// HealthStatus represents server health status
type HealthStatus struct {
	Status        string `json:"status"`
	ActiveJobs    int64  `json:"active_jobs"`
	QueuedJobs    int64  `json:"queued_jobs"`
	CompletedJobs int64  `json:"completed_jobs"`
	FailedJobs    int64  `json:"failed_jobs"`
	Workers       int    `json:"workers"`
	Uptime        string `json:"uptime"`
	MemoryUsage   string `json:"memory_usage"`
}

// ytdlpFormat represents yt-dlp format information
type ytdlpFormat struct {
	FormatID string  `json:"format_id"`
	ACodec   string  `json:"acodec"`
	VCodec   string  `json:"vcodec"`
	Ext      string  `json:"ext"`
	Protocol string  `json:"protocol"`
	URL      string  `json:"url"`
	ABR      float64 `json:"abr"`
	TBR      float64 `json:"tbr"`
}

// ytdlpInfo represents yt-dlp video information
type ytdlpInfo struct {
	Title    string        `json:"title"`
	Uploader string        `json:"uploader"`
	Duration float64       `json:"duration"`
	Formats  []ytdlpFormat `json:"formats"`
}

// formatInfoForScore represents format information for scoring
type formatInfoForScore struct {
	Ext      string
	Protocol string
	ABR      float64
	TBR      float64
}