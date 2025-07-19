package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// TranscodingProfile defines parameters for video transcoding.
// It specifies codecs, resolution, bitrates, and other encoding settings.
type TranscodingProfile struct {
	Name             string            `json:"name"`              // Profile name
	VideoCodec       string            `json:"video_codec"`       // Video codec (h264, h265, vp8, vp9)
	AudioCodec       string            `json:"audio_codec"`       // Audio codec (aac, mp3, opus)
	Container        string            `json:"container"`         // Output container (mp4, webm, mkv)
	Resolution       string            `json:"resolution"`        // Resolution (e.g., 1920x1080)
	Bitrate          string            `json:"bitrate"`           // Video bitrate
	AudioBitrate     string            `json:"audio_bitrate"`     // Audio bitrate
	Framerate        int               `json:"framerate"`         // Frames per second
	KeyframeInterval int               `json:"keyframe_interval"` // GOP size
	Preset           string            `json:"preset"`            // Encoding speed/quality preset
	CRF              int               `json:"crf"`               // Constant Rate Factor
	TwoPass          bool              `json:"two_pass"`          // Enable two-pass encoding
	HWAccel          string            `json:"hw_accel"`          // Hardware acceleration type
	CustomParams     map[string]string `json:"custom_params"`     // Additional FFmpeg parameters
}

// DefaultProfiles returns a map of predefined transcoding profiles.
func DefaultProfiles() map[string]*TranscodingProfile {
	return map[string]*TranscodingProfile{
		"hd_h264": {
			Name:             "HD H.264",
			VideoCodec:       "libx264",
			AudioCodec:       "aac",
			Container:        "mp4",
			Resolution:       "1280x720",
			Bitrate:          "2500k",
			AudioBitrate:     "128k",
			Framerate:        30,
			KeyframeInterval: 30,
			Preset:           "medium",
			CRF:              23,
			TwoPass:          false,
		},
		"fhd_h264": {
			Name:             "Full HD H.264",
			VideoCodec:       "libx264",
			AudioCodec:       "aac",
			Container:        "mp4",
			Resolution:       "1920x1080",
			Bitrate:          "5000k",
			AudioBitrate:     "192k",
			Framerate:        30,
			KeyframeInterval: 30,
			Preset:           "medium",
			CRF:              23,
			TwoPass:          false,
		},
		"4k_h265": {
			Name:             "4K H.265",
			VideoCodec:       "libx265",
			AudioCodec:       "aac",
			Container:        "mp4",
			Resolution:       "3840x2160",
			Bitrate:          "15000k",
			AudioBitrate:     "256k",
			Framerate:        30,
			KeyframeInterval: 30,
			Preset:           "medium",
			CRF:              28,
			TwoPass:          true,
		},
		"web_optimized": {
			Name:             "Web Optimized",
			VideoCodec:       "libx264",
			AudioCodec:       "aac",
			Container:        "mp4",
			Resolution:       "854x480",
			Bitrate:          "1000k",
			AudioBitrate:     "96k",
			Framerate:        24,
			KeyframeInterval: 24,
			Preset:           "fast",
			CRF:              26,
			TwoPass:          false,
			CustomParams: map[string]string{
				"movflags": "faststart",
			},
		},
		"mobile_h264": {
			Name:             "Mobile H.264",
			VideoCodec:       "libx264",
			AudioCodec:       "aac",
			Container:        "mp4",
			Resolution:       "640x360",
			Bitrate:          "500k",
			AudioBitrate:     "64k",
			Framerate:        24,
			KeyframeInterval: 24,
			Preset:           "fast",
			CRF:              28,
			TwoPass:          false,
			CustomParams: map[string]string{
				"profile:v": "baseline",
				"level":     "3.0",
				"movflags":  "faststart",
			},
		},
		"hls_adaptive": {
			Name:             "HLS Adaptive",
			VideoCodec:       "libx264",
			AudioCodec:       "aac",
			Container:        "ts",
			Resolution:       "1280x720",
			Bitrate:          "2000k",
			AudioBitrate:     "128k",
			Framerate:        30,
			KeyframeInterval: 30,
			Preset:           "fast",
			CRF:              23,
			TwoPass:          false,
			CustomParams: map[string]string{
				"f":                 "hls",
				"hls_time":          "4",
				"hls_playlist_type": "vod",
			},
		},
	}
}

// TranscodingJob represents a single transcoding task with its status and progress.
type TranscodingJob struct {
	ID           string              `json:"id"`            // Unique job identifier
	InputPath    string              `json:"input_path"`    // Input file path
	OutputPath   string              `json:"output_path"`   // Output file path
	Profile      *TranscodingProfile `json:"profile"`       // Transcoding profile
	Status       string              `json:"status"`        // Current job status
	Progress     float64             `json:"progress"`      // Progress percentage
	Duration     time.Duration       `json:"duration"`      // Total job duration
	StartTime    time.Time           `json:"start_time"`    // Job start time
	EndTime      time.Time           `json:"end_time"`      // Job end time
	ErrorMessage string              `json:"error_message"` // Error message, if any
	InputInfo    *MediaInfo          `json:"input_info"`    // Input media information
	OutputInfo   *MediaInfo          `json:"output_info"`   // Output media information
}

// MediaInfo contains detailed information about a media file.
type MediaInfo struct {
	Format   string        `json:"format"`   // File format
	Duration time.Duration `json:"duration"` // Media duration
	Size     int64         `json:"size"`     // File size in bytes
	Bitrate  int64         `json:"bitrate"`  // Overall bitrate
	Video    *VideoInfo    `json:"video"`    // Video stream information
	Audio    *AudioInfo    `json:"audio"`    // Audio stream information
}

// VideoInfo contains video stream information.
type VideoInfo struct {
	Codec       string  `json:"codec"`        // Video codec
	Width       int     `json:"width"`        // Video width
	Height      int     `json:"height"`       // Video height
	Framerate   float64 `json:"framerate"`    // Frames per second
	Bitrate     int64   `json:"bitrate"`      // Video bitrate
	PixelFormat string  `json:"pixel_format"` // Pixel format
}

// AudioInfo contains audio stream information.
type AudioInfo struct {
	Codec      string `json:"codec"`       // Audio codec
	SampleRate int    `json:"sample_rate"` // Sample rate
	Channels   int    `json:"channels"`    // Number of channels
	Bitrate    int64  `json:"bitrate"`     // Audio bitrate
}

// ProgressCallback is a function type for reporting transcoding progress.
type ProgressCallback func(progress float64, speed float64, eta time.Duration)

// Transcoder manages video transcoding operations with FFmpeg.
type Transcoder struct {
	ffmpegPath    string
	ffprobePath   string
	logger        zerolog.Logger
	profiles      map[string]*TranscodingProfile
	activeJobs    sync.Map
	maxConcurrent int
	semaphore     chan struct{}
}

// NewTranscoder creates a new Transcoder instance.
// If ffmpegPath or ffprobePath is empty, defaults to "ffmpeg" and "ffprobe".
// maxConcurrent limits simultaneous transcoding jobs; defaults to 4 if <= 0.
func NewTranscoder(ffmpegPath, ffprobePath string, maxConcurrent int, logger zerolog.Logger) *Transcoder {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}

	return &Transcoder{
		ffmpegPath:    ffmpegPath,
		ffprobePath:   ffprobePath,
		logger:        logger.With().Str("component", "transcoder").Logger(),
		profiles:      DefaultProfiles(),
		maxConcurrent: maxConcurrent,
		semaphore:     make(chan struct{}, maxConcurrent),
	}
}

// AddProfile adds a custom transcoding profile to the Transcoder.
func (t *Transcoder) AddProfile(name string, profile *TranscodingProfile) {
	t.profiles[name] = profile
	t.logger.Info().Str("profile", name).Msg("Added transcoding profile")
}

// GetProfile retrieves a transcoding profile by name.
func (t *Transcoder) GetProfile(name string) (*TranscodingProfile, bool) {
	profile, exists := t.profiles[name]
	return profile, exists
}

// GetProfiles returns all available transcoding profiles.
func (t *Transcoder) GetProfiles() map[string]*TranscodingProfile {
	return t.profiles
}

// GetMediaInfo extracts media information using ffprobe.
func (t *Transcoder) GetMediaInfo(ctx context.Context, inputPath string) (*MediaInfo, error) {
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		inputPath,
	}

	cmd := exec.CommandContext(ctx, t.ffprobePath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	return parseMediaInfo(output)
}

// TranscodeAsync starts an asynchronous transcoding job.
func (t *Transcoder) TranscodeAsync(ctx context.Context, jobID, inputPath, outputPath, profileName string, callback ProgressCallback) (*TranscodingJob, error) {
	profile, exists := t.profiles[profileName]
	if !exists {
		return nil, fmt.Errorf("profile '%s' not found", profileName)
	}

	job := &TranscodingJob{
		ID:         jobID,
		InputPath:  inputPath,
		OutputPath: outputPath,
		Profile:    profile,
		Status:     "queued",
		StartTime:  time.Now(),
	}

	t.activeJobs.Store(jobID, job)

	go func() {
		defer t.activeJobs.Delete(jobID)

		// Acquire semaphore
		select {
		case t.semaphore <- struct{}{}:
			defer func() { <-t.semaphore }()
		case <-ctx.Done():
			job.Status = "cancelled"
			job.ErrorMessage = "context cancelled"
			return
		}

		if err := t.transcode(ctx, job, callback); err != nil {
			job.Status = "failed"
			job.ErrorMessage = err.Error()
			job.EndTime = time.Now()
			t.logger.Error().
				Err(err).
				Str("job_id", jobID).
				Str("input", inputPath).
				Str("output", outputPath).
				Msg("Transcoding failed")
		} else {
			job.Status = "completed"
			job.EndTime = time.Now()
			job.Duration = job.EndTime.Sub(job.StartTime)
			t.logger.Info().
				Str("job_id", jobID).
				Str("input", inputPath).
				Str("output", outputPath).
				Dur("duration", job.Duration).
				Msg("Transcoding completed")
		}
	}()

	return job, nil
}

// TranscodeSync performs synchronous transcoding.
func (t *Transcoder) TranscodeSync(ctx context.Context, inputPath, outputPath, profileName string, callback ProgressCallback) error {
	profile, exists := t.profiles[profileName]
	if !exists {
		return fmt.Errorf("profile '%s' not found", profileName)
	}

	job := &TranscodingJob{
		ID:         fmt.Sprintf("sync_%d", time.Now().UnixNano()),
		InputPath:  inputPath,
		OutputPath: outputPath,
		Profile:    profile,
		Status:     "processing",
		StartTime:  time.Now(),
	}

	// Acquire semaphore
	select {
	case t.semaphore <- struct{}{}:
		defer func() { <-t.semaphore }()
	case <-ctx.Done():
		return fmt.Errorf("context cancelled")
	}

	return t.transcode(ctx, job, callback)
}

// GetJob retrieves a transcoding job by ID.
func (t *Transcoder) GetJob(jobID string) (*TranscodingJob, bool) {
	if job, exists := t.activeJobs.Load(jobID); exists {
		return job.(*TranscodingJob), true
	}
	return nil, false
}

// GetActiveJobs returns all active transcoding jobs.
func (t *Transcoder) GetActiveJobs() []*TranscodingJob {
	var jobs []*TranscodingJob
	t.activeJobs.Range(func(key, value interface{}) bool {
		jobs = append(jobs, value.(*TranscodingJob))
		return true
	})
	return jobs
}

// CancelJob cancels a transcoding job by ID.
func (t *Transcoder) CancelJob(jobID string) bool {
	if job, exists := t.activeJobs.Load(jobID); exists {
		transcodeJob := job.(*TranscodingJob)
		transcodeJob.Status = "cancelled"
		return true
	}
	return false
}

// transcode performs the actual transcoding operation.
func (t *Transcoder) transcode(ctx context.Context, job *TranscodingJob, callback ProgressCallback) error {
	job.Status = "processing"

	// Get input media info
	inputInfo, err := t.GetMediaInfo(ctx, job.InputPath)
	if err != nil {
		return fmt.Errorf("failed to get input media info: %w", err)
	}
	job.InputInfo = inputInfo

	// Create output directory
	outputDir := filepath.Dir(job.OutputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build FFmpeg command
	args, err := t.buildFFmpegArgs(job)
	if err != nil {
		return fmt.Errorf("failed to build ffmpeg arguments: %w", err)
	}

	t.logger.Debug().
		Str("job_id", job.ID).
		Strs("args", args).
		Msg("Starting FFmpeg transcoding")

	// Start FFmpeg process
	cmd := exec.CommandContext(ctx, t.ffmpegPath, args...)

	// Get stderr pipe for progress monitoring
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Monitor progress
	go t.monitorProgress(stderr, job, inputInfo.Duration, callback)

	// Wait for completion
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	// Get output media info
	outputInfo, err := t.GetMediaInfo(ctx, job.OutputPath)
	if err != nil {
		t.logger.Warn().
			Err(err).
			Str("output_path", job.OutputPath).
			Msg("Failed to get output media info")
	} else {
		job.OutputInfo = outputInfo
	}

	job.Progress = 100.0
	return nil
}

// buildFFmpegArgs constructs FFmpeg command arguments for transcoding.
func (t *Transcoder) buildFFmpegArgs(job *TranscodingJob) ([]string, error) {
	profile := job.Profile
	args := []string{
		"-i", job.InputPath,
		"-y", // overwrite output files
	}

	// Hardware acceleration
	if profile.HWAccel != "" {
		args = append(args, "-hwaccel", profile.HWAccel)
	}

	// Video codec and settings
	if profile.VideoCodec != "" {
		args = append(args, "-c:v", profile.VideoCodec)

		if profile.Bitrate != "" {
			args = append(args, "-b:v", profile.Bitrate)
		}

		if profile.CRF > 0 {
			args = append(args, "-crf", strconv.Itoa(profile.CRF))
		}

		if profile.Preset != "" {
			args = append(args, "-preset", profile.Preset)
		}

		if profile.KeyframeInterval > 0 {
			args = append(args, "-g", strconv.Itoa(profile.KeyframeInterval))
		}

		if profile.Resolution != "" {
			args = append(args, "-s", profile.Resolution)
		}

		if profile.Framerate > 0 {
			args = append(args, "-r", strconv.Itoa(profile.Framerate))
		}
	}

	// Audio codec and settings
	if profile.AudioCodec != "" {
		args = append(args, "-c:a", profile.AudioCodec)

		if profile.AudioBitrate != "" {
			args = append(args, "-b:a", profile.AudioBitrate)
		}
	}

	// Custom parameters
	for key, value := range profile.CustomParams {
		args = append(args, "-"+key, value)
	}

	// Progress reporting
	args = append(args, "-progress", "pipe:2")

	// Output file
	args = append(args, job.OutputPath)

	return args, nil
}

// monitorProgress monitors FFmpeg progress output.
func (t *Transcoder) monitorProgress(stderr io.Reader, job *TranscodingJob, totalDuration time.Duration, callback ProgressCallback) {
	scanner := bufio.NewScanner(stderr)
	progressRegex := regexp.MustCompile(`out_time_ms=(\d+)`)
	speedRegex := regexp.MustCompile(`speed=\s*([0-9.]+)x`)

	var currentTime time.Duration
	var speed float64

	for scanner.Scan() {
		line := scanner.Text()

		// Parse progress time
		if matches := progressRegex.FindStringSubmatch(line); len(matches) > 1 {
			if microseconds, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
				currentTime = time.Duration(microseconds) * time.Microsecond

				if totalDuration > 0 {
					progress := float64(currentTime) / float64(totalDuration) * 100
					if progress > 100 {
						progress = 100
					}
					job.Progress = progress
				}
			}
		}

		// Parse speed
		if matches := speedRegex.FindStringSubmatch(line); len(matches) > 1 {
			if s, err := strconv.ParseFloat(matches[1], 64); err == nil {
				speed = s
			}
		}

		// Calculate ETA
		var eta time.Duration
		if speed > 0 && totalDuration > 0 && currentTime < totalDuration {
			remainingTime := totalDuration - currentTime
			eta = time.Duration(float64(remainingTime) / speed)
		}

		// Call progress callback
		if callback != nil && job.Progress > 0 {
			callback(job.Progress, speed, eta)
		}
	}
}
