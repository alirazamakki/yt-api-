package main

import "time"

type Metadata struct {
    Title    string  `json:"title"`
    Uploader string  `json:"uploader"`
    Duration float64 `json:"duration"`
    AudioURL string  `json:"audio_url"`
    Ext      string  `json:"ext"`
    Abr      int     `json:"abr"`
}

// Prepare/convert flow types
type ConversionQuality string

const (
    Quality128 ConversionQuality = "128"
    Quality192 ConversionQuality = "192"
    Quality256 ConversionQuality = "256"
    Quality320 ConversionQuality = "320"
)

type PrepareRequest struct {
    URL string `json:"url"`
}

type PrepareResponse struct {
    ConversionID string   `json:"conversion_id"`
    Status       string   `json:"status"`
    Metadata     MetaLite `json:"metadata"`
    Message      string   `json:"message"`
}

type MetaLite struct {
    Title     string  `json:"title"`
    Channel   string  `json:"channel"`
    Duration  int     `json:"duration"`
    Thumbnail string  `json:"thumbnail"`
}

type ConvertRequest struct {
    ConversionID string            `json:"conversion_id"`
    Quality      ConversionQuality `json:"quality"`
    StartTime    string            `json:"start_time,omitempty"`
    EndTime      string            `json:"end_time,omitempty"`
}

type ConversionState string

const (
    StateCreated     ConversionState = "created"
    StateDownloading ConversionState = "downloading"
    StateDownloaded  ConversionState = "downloaded"
    StateQueued      ConversionState = "queued_for_conversion"
    StateConverting  ConversionState = "converting"
    StateCompleted   ConversionState = "completed"
    StateFailed      ConversionState = "failed"
)

type ConversionSession struct {
    ID                 string           `json:"id"`
    URL                string           `json:"url"`
    State              ConversionState  `json:"state"`
    CreatedAt          time.Time        `json:"created_at"`
    UpdatedAt          time.Time        `json:"updated_at"`
    SourcePath         string           `json:"source_path"`
    SourceExt          string           `json:"source_ext"`
    DownloadProgress   int              `json:"download_progress"`
    ConversionProgress int              `json:"conversion_progress"`
    ConversionsCount   int              `json:"conversions_count"`
    LastActivityAt     time.Time        `json:"last_activity_at"`
    OutputPath         string           `json:"output_path"`
    Quality            ConversionQuality `json:"quality"`
    Error              string           `json:"error"`
    Meta               MetaLite         `json:"metadata"`
    // Deferred conversion request parameters (for queued conversion)
    RequestedStart     string           `json:"-"`
    RequestedEnd       string           `json:"-"`
}

type Request struct {
    URL          string `json:"url"`
    CaptchaToken string `json:"captcha_token,omitempty"`
    IdempotencyKey string `json:"idempotency_key,omitempty"`
    CallbackURL   string `json:"callback_url,omitempty"`
}

type JobStatus string

const (
    StatusPending    JobStatus = "pending"
    StatusProcessing JobStatus = "processing"
    StatusCompleted  JobStatus = "completed"
    StatusFailed     JobStatus = "failed"
)

type ConversionJob struct {
    ID          string     `json:"id"`
    URL         string     `json:"url"`
    VideoID     string     `json:"video_id"`
    Status      JobStatus  `json:"status"`
    CreatedAt   time.Time  `json:"created_at"`
    StartedAt   time.Time  `json:"started_at"`
    CompletedAt time.Time  `json:"completed_at"`
    FilePath    string     `json:"file_path"`
    DownloadURL string     `json:"download_url"`
    FirstDownloadedAt time.Time `json:"first_downloaded_at"`
    Error       string     `json:"error"`
    Metadata    *Metadata  `json:"metadata"`
    Retries     int        `json:"retries"`
    MaxRetries  int        `json:"max_retries"`
    Priority    int        `json:"priority"`
    CallbackURL string     `json:"callback_url,omitempty"`
}

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
