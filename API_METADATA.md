# Fast Metadata API

This API provides a fast metadata response (1-2 seconds) and handles background audio downloading/processing.

## New Endpoints

### POST /metadata
Returns basic metadata immediately and starts background download.

**Request:**
```json
{
  "url": "https://www.youtube.com/watch?v=VIDEO_ID"
}
```

**Response:**
```json
{
  "session_id": "meta_12345-67890-abcdef",
  "status": "metadata_ready",
  "metadata": {
    "title": "Video Title",
    "channel": "Channel Name", 
    "duration": 180,
    "thumbnail": "https://img.youtube.com/vi/VIDEO_ID/maxresdefault.jpg"
  },
  "message": "Metadata fetched. Audio is downloading in background.",
  "check_status_endpoint": "http://localhost:8080/status/meta_12345-67890-abcdef"
}
```

### GET /status/{session_id}
Check the status of a session.

**Response:**
```json
{
  "session_id": "meta_12345-67890-abcdef",
  "status": "downloaded", // or "downloading", "converting", "completed", "failed"
  "metadata": {
    "title": "Video Title",
    "channel": "Channel Name",
    "duration": 180,
    "thumbnail": "https://img.youtube.com/vi/VIDEO_ID/maxresdefault.jpg"
  },
  "download_progress": 75 // if status is "downloading"
}
```

### POST /convert-audio
Convert the downloaded audio to MP3.

**Request:**
```json
{
  "conversion_id": "meta_12345-67890-abcdef",
  "quality": "320", // optional: "128", "192", "256", "320"
  "start_time": "00:01:30", // optional: start time for trimming
  "end_time": "00:03:45"    // optional: end time for trimming
}
```

**Response:**
```json
{
  "session_id": "meta_12345-67890-abcdef",
  "status": "converting",
  "message": "Conversion started"
}
```

### GET /download/{session_id}.mp3
Download the converted MP3 file (supports range requests).

## Usage Flow

1. **Get metadata**: `POST /metadata` - Returns immediately with basic info
2. **Check status**: `GET /status/{session_id}` - Poll until status is "downloaded"
3. **Convert audio**: `POST /convert-audio` - Start MP3 conversion
4. **Check conversion**: `GET /status/{session_id}` - Poll until status is "completed"
5. **Download**: `GET /download/{session_id}.mp3` - Download the final MP3

## Status Values

- `metadata_ready` - Metadata fetched, download starting
- `downloading` - Audio file downloading in background
- `downloaded` - Audio ready for conversion
- `converting` - Converting to MP3
- `completed` - MP3 ready for download
- `failed` - Error occurred

## Features

- **Fast response**: Metadata returned in 1-2 seconds
- **Background processing**: Audio download happens without blocking
- **Progress tracking**: Real-time download and conversion progress
- **Quality control**: Multiple MP3 quality options
- **Audio trimming**: Optional start/end time parameters
- **Range requests**: Efficient MP3 downloading