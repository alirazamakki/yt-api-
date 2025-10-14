package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "os"
    "os/exec"
    "strconv"
    "sort"
    "strings"
    "time"
)

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

type ytdlpInfo struct {
    Title    string        `json:"title"`
    Uploader string        `json:"uploader"`
    Duration float64       `json:"duration"`
    Formats  []ytdlpFormat `json:"formats"`
}

func getAudioStreamFromYTDLP(videoURL string) (string, *Metadata, error) {
    ctxTimeout, cancel := context.WithTimeout(ctx, YTDLPTimeout)
    defer cancel()

    args := []string{"-J", "--no-warnings", "--skip-download"}
    if YTDLPCookies != "" {
        if strings.HasPrefix(YTDLPCookies, "browser:") {
            // e.g., browser:chrome
            args = append(args, "--cookies-from-browser", strings.TrimPrefix(YTDLPCookies, "browser:"))
        } else {
            args = append(args, "--cookies", YTDLPCookies)
        }
    }
    if YTDLPExtractorArgs != "" {
        args = append(args, "--extractor-args", YTDLPExtractorArgs)
    }
    if YTDLPExtraArgs != "" {
        // split on spaces; simple parser
        parts := strings.Fields(YTDLPExtraArgs)
        args = append(args, parts...)
    }
    args = append(args, videoURL)
    cmd := exec.CommandContext(ctxTimeout, "yt-dlp", args...)
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        // Fallback 1: minimal args (strip extras)
        var fb1Out, fb1Err bytes.Buffer
        base := []string{"-J", "--no-warnings", "--skip-download", videoURL}
        cmd2 := exec.CommandContext(ctxTimeout, "yt-dlp", base...)
        cmd2.Stdout = &fb1Out
        cmd2.Stderr = &fb1Err
        if err2 := cmd2.Run(); err2 != nil {
            // Fallback 2: try android client extractor
            var fb2Out, fb2Err bytes.Buffer
            base2 := []string{"-J", "--no-warnings", "--skip-download", "--extractor-args", "youtube:player_client=android", videoURL}
            cmd3 := exec.CommandContext(ctxTimeout, "yt-dlp", base2...)
            cmd3.Stdout = &fb2Out
            cmd3.Stderr = &fb2Err
            if err3 := cmd3.Run(); err3 != nil {
                return "", nil, fmt.Errorf("yt-dlp metadata error: %v | %s | fb1: %v | %s | fb2: %v | %s", err, strings.TrimSpace(stderr.String()), err2, strings.TrimSpace(fb1Err.String()), err3, strings.TrimSpace(fb2Err.String()))
            }
            stdout = fb2Out
        } else {
            stdout = fb1Out
        }
    }

    var info ytdlpInfo
    if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
        return "", nil, fmt.Errorf("yt-dlp metadata parse error: %v", err)
    }

    // Enforce optional max duration (e.g., 90 minutes) via env guard
    if info.Duration > 0 {
        // Max duration in seconds if env MAX_DURATION_MIN is set
        if maxMinStr := os.Getenv("MAX_DURATION_MIN"); maxMinStr != "" {
            if mm, err := strconv.Atoi(maxMinStr); err == nil && mm > 0 {
                if info.Duration > float64(mm*60) {
                    return "", nil, fmt.Errorf("video duration exceeds max %d minutes", mm)
                }
            }
        }
    }

    candidates := make([]ytdlpFormat, 0, len(info.Formats))
    for _, f := range info.Formats {
        if f.URL == "" {
            continue
        }
        isAudioOnly := (f.VCodec == "none" || f.VCodec == "") && f.ACodec != "none"
        if isAudioOnly {
            candidates = append(candidates, f)
            continue
        }
    }
    if len(candidates) == 0 {
        for _, f := range info.Formats {
            if f.URL == "" {
                continue
            }
            if f.ACodec != "none" {
                candidates = append(candidates, f)
            }
        }
    }
    if len(candidates) == 0 {
        return "", nil, fmt.Errorf("no usable audio formats found")
    }

    sort.SliceStable(candidates, func(i, j int) bool {
        si := scoreFormat(formatInfoForScore{Ext: candidates[i].Ext, Protocol: candidates[i].Protocol, ABR: candidates[i].ABR, TBR: candidates[i].TBR})
        sj := scoreFormat(formatInfoForScore{Ext: candidates[j].Ext, Protocol: candidates[j].Protocol, ABR: candidates[j].ABR, TBR: candidates[j].TBR})
        if si == sj {
            return candidates[i].ABR > candidates[j].ABR
        }
        return si > sj
    })

    best := candidates[0]
    meta := &Metadata{
        Title:    info.Title,
        Uploader: info.Uploader,
        Duration: info.Duration,
        AudioURL: best.URL,
        Ext:      best.Ext,
        Abr:      int(best.ABR),
    }
    return best.URL, meta, nil
}

func downloadAudioWithYTDLP(videoURL, outputPath string, expectedExt string) error {
    // Build output template to desired path
    ctxTimeout, cancel := context.WithTimeout(ctx, YTDLPDownloadTimeout)
    defer cancel()

    // Helper to attempt a download with specific args
    try := func(args []string) error {
        cmd := exec.CommandContext(ctxTimeout, "yt-dlp", args...)
        var stderr bytes.Buffer
        cmd.Stderr = &stderr
        if err := cmd.Run(); err != nil {
            return fmt.Errorf("yt-dlp download error: %v | %s", err, strings.TrimSpace(stderr.String()))
        }
        return nil
    }

    // Attempt 1: configured args + strict bestaudio
    args1 := []string{"-f", "bestaudio[acodec!=none]/bestaudio", "-N", fmt.Sprintf("%d", YTDLPDownloadConcurrency),
        "-o", outputPath + ".%(ext)s", "--no-playlist", "--no-warnings"}
    if YTDLPCookies != "" {
        if strings.HasPrefix(YTDLPCookies, "browser:") {
            args1 = append(args1, "--cookies-from-browser", strings.TrimPrefix(YTDLPCookies, "browser:"))
        } else { args1 = append(args1, "--cookies", YTDLPCookies) }
    }
    if YTDLPExtraArgs != "" { args1 = append(args1, strings.Fields(YTDLPExtraArgs)...)}
    args1 = append(args1, videoURL)
    if err := try(args1); err != nil {
        // Attempt 2: minimal args, looser format selector
        args2 := []string{"-f", "bestaudio/best", "-N", fmt.Sprintf("%d", YTDLPDownloadConcurrency),
            "-o", outputPath + ".%(ext)s", "--no-playlist", "--no-warnings", videoURL}
        if err2 := try(args2); err2 != nil {
            // Attempt 3: force android client extractor
            args3 := []string{"-f", "bestaudio/best", "--extractor-args", "youtube:player_client=android",
                "-N", fmt.Sprintf("%d", YTDLPDownloadConcurrency), "-o", outputPath + ".%(ext)s",
                "--no-playlist", "--no-warnings", videoURL}
            if err3 := try(args3); err3 != nil {
                return fmt.Errorf("%v | fallback1: %v | fallback2: %v", err, err2, err3)
            }
        }
    }

    // Try to find produced file with known extensions
    for _, ext := range []string{"m4a", "webm", "mp4", expectedExt} {
        if ext == "" { continue }
        p := outputPath + "." + ext
        if _, err := os.Stat(p); err == nil { return nil }
    }
    return fmt.Errorf("yt-dlp download succeeded but file not found")
}

func convertStreamToMP3(audioURL, outputPath string, timeout time.Duration) error {
    if timeout <= 0 {
        timeout = FFmpegMinTimeout
    }
    if timeout > FFmpegMaxTimeout {
        timeout = FFmpegMaxTimeout
    }
    ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    args := []string{"-y", "-loglevel", "error", "-nostdin"}
    if strings.HasPrefix(audioURL, "http") {
        // Network resilience only for URLs
        args = append(args,
            "-reconnect", "1",
            "-reconnect_streamed", "1",
            "-reconnect_on_network_error", "1",
            "-reconnect_delay_max", "10",
            "-rw_timeout", "60000000",
        )
    }
    args = append(args, "-i", audioURL, "-vn", "-acodec", "libmp3lame", "-ar", "44100")
    if strings.EqualFold(FFmpegMode, "VBR") {
        args = append(args, "-q:a", fmt.Sprintf("%d", FFmpegVBRQ))
    } else {
        args = append(args, "-b:a", FFmpegCBRBitrate)
    }
    if FFmpegThreads > 0 { args = append(args, "-threads", fmt.Sprintf("%d", FFmpegThreads)) }
    args = append(args, outputPath)
    cmd := exec.CommandContext(ctxTimeout, "ffmpeg", args...)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("ffmpeg error: %v | %s", err, strings.TrimSpace(stderr.String()))
    }
    return nil
}

type formatInfoForScore struct {
    Ext      string
    Protocol string
    ABR      float64
    TBR      float64
}

func scoreFormat(f formatInfoForScore) int {
    score := 0
    switch strings.ToLower(f.Ext) {
    case "m4a":
        score += 100
    case "webm":
        score += 90
    case "ogg", "opus":
        score += 85
    case "mp4":
        score += 70
    default:
        score += 60
    }
    p := strings.ToLower(f.Protocol)
    if strings.HasPrefix(p, "https") {
        score += 30
    } else if strings.HasPrefix(p, "http") {
        score += 25
    } else if strings.Contains(p, "m3u8") || strings.Contains(p, "hls") {
        score += 20
    } else if strings.Contains(p, "dash") {
        score += 15
    }
    if f.ABR > 0 {
        score += int(f.ABR)
    } else if f.TBR > 0 {
        score += int(f.TBR / 2)
    }
    return score
}
