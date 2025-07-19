# FFmpeg Go Package

This package provides a Go wrapper for FFmpeg, enabling video transcoding and streaming operations with a clean, modern API.

## Features
- Video transcoding with customizable profiles
- Streaming support for multiple formats (HLS, RTSP, DASH, etc.)
- Process management with progress monitoring
- Media information extraction
- Concurrent job handling
- Hardware acceleration support
- Comprehensive error handling and logging

## Installation
```bash
go get github.com/mdcreativenetwork/ffmpeg-go
```

## Usage
### Transcoding
```go
package main

import (
    "context"
    "github.com/mdcreativenetwork/ffmpeg-go"
    "github.com/rs/zerolog"
    "os"
)

func main() {
    logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
    
    // Initialize transcoder
    transcoder := ffmpeg.NewTranscoder("ffmpeg", "ffprobe", 4, logger)
    
    // Start transcoding job
    job, err := transcoder.TranscodeAsync(
        context.Background(),
        "job1",
        "input.mp4",
        "output.mp4",
        "hd_h264",
        func(progress, speed float64, eta time.Duration) {
            // Progress callback
            logger.Info().
                Float64("progress", progress).
                Float64("speed", speed).
                Str("eta", eta.String()).
                Msg("Transcoding progress")
        },
    )
    if err != nil {
        logger.Fatal().Err(err).Msg("Failed to start transcoding")
    }
}
```

### Streaming
```go
package main

import (
    "context"
    "github.com/mdcreativenetwork/ffmpeg-go"
    "github.com/rs/zerolog"
    "os"
)

func main() {
    logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
    
    // Initialize wrapper and stream manager
    config := ffmpeg.DefaultConfig()
    wrapper, _ := ffmpeg.NewWrapper(config, logger)
    streamManager := ffmpeg.NewStreamManager(wrapper, logger)
    
    // Configure streaming
    streamConfig := ffmpeg.StreamConfig{
        Input:  "rtsp://input-stream",
        Output: "hls/output.m3u8",
        Format: ffmpeg.FormatHLS,
        Profiles: []ffmpeg.StreamProfile{
            ffmpeg.GetDefaultProfiles()[ffmpeg.QualityMedium],
        },
        HLSSegmentTime: 4,
        HLSListSize:    5,
    }
    
    // Start streaming
    session, err := streamManager.StartStream(context.Background(), "stream1", streamConfig)
    if err != nil {
        logger.Fatal().Err(err).Msg("Failed to start streaming")
    }
}
```

## Documentation
Full GoDoc documentation is available for all public types and functions. Key components include:

- `Transcoder`: Handles video transcoding operations
- `StreamManager`: Manages streaming sessions
- `Wrapper`: Low-level FFmpeg process management
- `TranscodingProfile`: Defines transcoding parameters
- `StreamConfig`: Configures streaming parameters
- `MediaInfo`: Provides media file information

## Dependencies
- github.com/rs/zerolog
- Standard Go libraries

## Building
```bash
go build ./...
```

## Testing
```bash
go test ./...
```

## License
MIT License