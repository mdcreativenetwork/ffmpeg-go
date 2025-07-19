package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// StreamFormat represents different streaming formats.
type StreamFormat string

const (
	FormatRTSP StreamFormat = "rtsp"
	FormatHLS  StreamFormat = "hls"
	FormatDASH StreamFormat = "dash"
	FormatFLV  StreamFormat = "flv"
	FormatTS   StreamFormat = "ts"
	FormatMP4  StreamFormat = "mp4"
	FormatWebM StreamFormat = "webm"
)

// StreamQuality represents different streaming quality levels.
type StreamQuality string

const (
	QualityLow    StreamQuality = "low"    // 480p
	QualityMedium StreamQuality = "medium" // 720p
	QualityHigh   StreamQuality = "high"   // 1080p
	QualityUHD    StreamQuality = "uhd"    // 4K
	QualityAuto   StreamQuality = "auto"   // Adaptive
)

// StreamProfile defines parameters for streaming.
type StreamProfile struct {
	Format       StreamFormat  `json:"format"`        // Streaming format
	Quality      StreamQuality `json:"quality"`       // Quality level
	VideoBitrate int           `json:"video_bitrate"` // Video bitrate in kbps
	AudioBitrate int           `json:"audio_bitrate"` // Audio bitrate in kbps
	Width        int           `json:"width"`         // Video width
	Height       int           `json:"height"`        // Video height
	FPS          int           `json:"fps"`           // Frames per second
	GOPSize      int           `json:"gop_size"`      // Group of Pictures size
	Preset       string        `json:"preset"`        // Encoding preset
	Profile      string        `json:"profile"`       // Video profile
	Level        string        `json:"level"`         // Video level
}

// StreamConfig holds configuration for a streaming session.
type StreamConfig struct {
	Input          string            `json:"input"`            // Input source
	Output         string            `json:"output"`           // Output destination
	Format         StreamFormat      `json:"format"`           // Output format
	Profiles       []StreamProfile   `json:"profiles"`         // Streaming profiles
	HLSSegmentTime int               `json:"hls_segment_time"` // HLS segment duration
	HLSListSize    int               `json:"hls_list_size"`    // HLS playlist size
	HLSFlags       []string          `json:"hls_flags"`        // HLS flags
	RTSPTransport  string            `json:"rtsp_transport"`   // RTSP transport protocol
	RTSPTimeout    time.Duration     `json:"rtsp_timeout"`     // RTSP timeout
	BufferSize     string            `json:"buffer_size"`      // Buffer size
	ThreadQueue    int               `json:"thread_queue"`     // Thread queue size
	Reconnect      bool              `json:"reconnect"`        // Enable reconnection
	ReconnectDelay time.Duration     `json:"reconnect_delay"`  // Reconnect delay
	MaxRetries     int               `json:"max_retries"`      // Maximum reconnect attempts
	CustomOptions  map[string]string `json:"custom_options"`   // Additional FFmpeg options
	AudioEnabled   bool              `json:"audio_enabled"`    // Enable audio
	VideoEnabled   bool              `json:"video_enabled"`    // Enable video
}

// StreamSession represents an active streaming session.
type StreamSession struct {
	ID           string        `json:"id"`         // Session ID
	Config       StreamConfig  `json:"config"`     // Session configuration
	Process      *exec.Cmd     `json:"-"`          // FFmpeg process
	Status       ProcessStatus `json:"status"`     // Current status
	StartTime    time.Time     `json:"start_time"` // Start time
	EndTime      *time.Time    `json:"end_time"`   // End time
	Stats        *StreamStats  `json:"stats"`      // Streaming statistics
	ctx          context.Context
	cancel       context.CancelFunc
	mutex        sync.RWMutex
	logger       zerolog.Logger
	errorChan    chan error
	statsChan    chan StreamStats
	progressChan chan ProgressInfo
}

// StreamStats contains streaming statistics.
type StreamStats struct {
	Duration         time.Duration `json:"duration"`          // Streaming duration
	VideoBitrate     float64       `json:"video_bitrate"`     // Video bitrate
	AudioBitrate     float64       `json:"audio_bitrate"`     // Audio bitrate
	FPS              float64       `json:"fps"`               // Frames per second
	DroppedFrames    int64         `json:"dropped_frames"`    // Dropped frames count
	BytesTransferred int64         `json:"bytes_transferred"` // Bytes transferred
	Errors           int64         `json:"errors"`            // Error count
	Quality          string        `json:"quality"`           // Current quality
	LastUpdate       time.Time     `json:"last_update"`       // Last stats update
}

// ProgressInfo contains progress information during streaming.
type ProgressInfo struct {
	Frame     int64         `json:"frame"`     // Current frame
	FPS       float64       `json:"fps"`       // Frames per second
	Bitrate   string        `json:"bitrate"`   // Current bitrate
	Time      time.Duration `json:"time"`      // Current time
	Speed     string        `json:"speed"`     // Encoding speed
	Size      int64         `json:"size"`      // Output size
	Timestamp time.Time     `json:"timestamp"` // Update timestamp
}

// StreamManager manages multiple streaming sessions.
type StreamManager struct {
	sessions map[string]*StreamSession
	mutex    sync.RWMutex
	logger   zerolog.Logger
	wrapper  *Wrapper
}

// NewStreamManager creates a new StreamManager instance.
func NewStreamManager(wrapper *Wrapper, logger zerolog.Logger) *StreamManager {
	return &StreamManager{
		sessions: make(map[string]*StreamSession),
		logger:   logger.With().Str("component", "stream_manager").Logger(),
		wrapper:  wrapper,
	}
}

// GetDefaultProfiles returns default streaming profiles for different quality levels.
func GetDefaultProfiles() map[StreamQuality]StreamProfile {
	return map[StreamQuality]StreamProfile{
		QualityLow: {
			Format:       FormatHLS,
			Quality:      QualityLow,
			VideoBitrate: 800,
			AudioBitrate: 128,
			Width:        854,
			Height:       480,
			FPS:          25,
			GOPSize:      50,
			Preset:       "veryfast",
			Profile:      "baseline",
			Level:        "3.0",
		},
		QualityMedium: {
			Format:       FormatHLS,
			Quality:      QualityMedium,
			VideoBitrate: 2500,
			AudioBitrate: 192,
			Width:        1280,
			Height:       720,
			FPS:          30,
			GOPSize:      60,
			Preset:       "fast",
			Profile:      "main",
			Level:        "3.1",
		},
		QualityHigh: {
			Format:       FormatHLS,
			Quality:      QualityHigh,
			VideoBitrate: 5000,
			AudioBitrate: 256,
			Width:        1920,
			Height:       1080,
			FPS:          30,
			GOPSize:      60,
			Preset:       "medium",
			Profile:      "high",
			Level:        "4.0",
		},
		QualityUHD: {
			Format:       FormatHLS,
			Quality:      QualityUHD,
			VideoBitrate: 15000,
			AudioBitrate: 320,
			Width:        3840,
			Height:       2160,
			FPS:          30,
			GOPSize:      60,
			Preset:       "slow",
			Profile:      "high",
			Level:        "5.1",
		},
	}
}

// StartStream initiates a new streaming session.
func (sm *StreamManager) StartStream(ctx context.Context, sessionID string, config StreamConfig) (*StreamSession, error) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if _, exists := sm.sessions[sessionID]; exists {
		return nil, fmt.Errorf("session %s already exists", sessionID)
	}

	if err := sm.validateStreamConfig(config); err != nil {
		return nil, fmt.Errorf("invalid stream config: %w", err)
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	session := &StreamSession{
		ID:           sessionID,
		Config:       config,
		Status:       StatusPending,
		StartTime:    time.Now(),
		ctx:          sessionCtx,
		cancel:       cancel,
		logger:       sm.logger.With().Str("session_id", sessionID).Logger(),
		errorChan:    make(chan error, 10),
		statsChan:    make(chan StreamStats, 10),
		progressChan: make(chan ProgressInfo, 100),
	}

	sm.sessions[sessionID] = session
	go sm.runStreamSession(session)

	session.logger.Info().
		Str("input", config.Input).
		Str("output", config.Output).
		Str("format", string(config.Format)).
		Msg("Stream session started")

	return session, nil
}

// StopStream terminates a streaming session.
func (sm *StreamManager) StopStream(sessionID string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}

	session.mutex.Lock()
	defer session.mutex.Unlock()

	if session.Status == StatusCancelled {
		return nil
	}

	session.Status = StatusCancelled
	session.logger.Info().Msg("Stopping stream session")

	session.cancel()
	if session.Process != nil && session.Process.Process != nil {
		if err := session.Process.Process.Kill(); err != nil {
			session.logger.Warn().Err(err).Msg("Failed to kill process")
		}
	}

	now := time.Now()
	session.EndTime = &now
	session.Status = StatusCancelled

	close(session.errorChan)
	close(session.statsChan)
	close(session.progressChan)
	delete(sm.sessions, sessionID)

	session.logger.Info().Msg("Stream session stopped")
	return nil
}

// GetSession retrieves a streaming session by ID.
func (sm *StreamManager) GetSession(sessionID string) (*StreamSession, bool) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	session, exists := sm.sessions[sessionID]
	return session, exists
}

// GetAllSessions returns all active streaming sessions.
func (sm *StreamManager) GetAllSessions() map[string]*StreamSession {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	sessions := make(map[string]*StreamSession)
	for id, session := range sm.sessions {
		sessions[id] = session
	}
	return sessions
}

// runStreamSession manages the streaming session lifecycle.
func (sm *StreamManager) runStreamSession(session *StreamSession) {
	session.mutex.Lock()
	session.Status = StatusPending
	session.mutex.Unlock()

	defer func() {
		if r := recover(); r != nil {
			session.logger.Error().
				Interface("panic", r).
				Msg("Stream session panicked")
			session.mutex.Lock()
			session.Status = StatusFailed
			session.mutex.Unlock()
		}
	}()

	retryCount := 0
	maxRetries := session.Config.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}

	for retryCount <= maxRetries {
		if session.ctx.Err() != nil {
			break
		}

		if retryCount > 0 {
			session.mutex.Lock()
			session.Status = StatusRunning
			session.mutex.Unlock()

			session.logger.Info().
				Int("attempt", retryCount).
				Int("max_retries", maxRetries).
				Msg("Reconnecting stream")

			delay := session.Config.ReconnectDelay
			if delay == 0 {
				delay = 5 * time.Second
			}

			select {
			case <-session.ctx.Done():
				return
			case <-time.After(delay):
			}
		}

		cmd, err := sm.buildStreamCommand(session)
		if err != nil {
			session.logger.Error().Err(err).Msg("Failed to build stream command")
			retryCount++
			continue
		}

		session.mutex.Lock()
		session.Process = cmd
		session.Status = StatusRunning
		session.mutex.Unlock()

		err = sm.executeStreamCommand(session, cmd)
		if err != nil && session.ctx.Err() == nil {
			session.logger.Error().
				Err(err).
				Int("attempt", retryCount).
				Msg("Stream process failed")

			if !session.Config.Reconnect {
				break
			}
		}

		retryCount++
	}

	session.mutex.Lock()
	if session.Status != StatusCancelled {
		session.Status = StatusFailed
	}
	session.mutex.Unlock()

	session.logger.Info().Msg("Stream session ended")
}

// buildStreamCommand constructs FFmpeg command for streaming.
func (sm *StreamManager) buildStreamCommand(session *StreamSession) (*exec.Cmd, error) {
	config := session.Config
	args := []string{}

	if config.RTSPTransport != "" {
		args = append(args, "-rtsp_transport", config.RTSPTransport)
	}
	if config.RTSPTimeout > 0 {
		args = append(args, "-timeout", strconv.Itoa(int(config.RTSPTimeout.Seconds())))
	}
	if config.BufferSize != "" {
		args = append(args, "-buffer_size", config.BufferSize)
	}
	if config.ThreadQueue > 0 {
		args = append(args, "-thread_queue_size", strconv.Itoa(config.ThreadQueue))
	}

	if strings.HasPrefix(config.Input, "rtsp://") && config.Reconnect {
		args = append(args, "-reconnect", "1", "-reconnect_at_eof", "1", "-reconnect_streamed", "1")
	}

	args = append(args, "-i", config.Input)

	for i, profile := range config.Profiles {
		args = append(args, sm.buildProfileArgs(profile, i)...)
	}

	switch config.Format {
	case FormatHLS:
		args = append(args, sm.buildHLSArgs(config)...)
	case FormatDASH:
		args = append(args, sm.buildDASHArgs(config)...)
	case FormatRTSP:
		args = append(args, sm.buildRTSPArgs(config)...)
	}

	args = append(args, "-y")
	for key, value := range config.CustomOptions {
		args = append(args, fmt.Sprintf("-%s", key), value)
	}

	args = append(args, config.Output)
	cmd := exec.CommandContext(session.ctx, sm.wrapper.FFmpegPath, args...)
	cmd.Env = os.Environ()

	session.logger.Debug().
		Strs("args", args).
		Msg("Built FFmpeg command")

	return cmd, nil
}

// buildProfileArgs constructs FFmpeg arguments for a streaming profile.
func (sm *StreamManager) buildProfileArgs(profile StreamProfile, index int) []string {
	args := []string{}
	args = append(args, "-c:v", "libx264")
	args = append(args, "-preset", profile.Preset)
	args = append(args, "-profile:v", profile.Profile)
	args = append(args, "-level", profile.Level)
	args = append(args, "-b:v", fmt.Sprintf("%dk", profile.VideoBitrate))
	args = append(args, "-maxrate", fmt.Sprintf("%dk", profile.VideoBitrate*12/10))
	args = append(args, "-bufsize", fmt.Sprintf("%dk", profile.VideoBitrate*2))
	args = append(args, "-g", strconv.Itoa(profile.GOPSize))
	args = append(args, "-keyint_min", strconv.Itoa(profile.GOPSize))
	args = append(args, "-sc_threshold", "0")

	if profile.Width > 0 && profile.Height > 0 {
		args = append(args, "-s", fmt.Sprintf("%dx%d", profile.Width, profile.Height))
	}

	if profile.FPS > 0 {
		args = append(args, "-r", strconv.Itoa(profile.FPS))
	}

	args = append(args, "-c:a", "aac")
	args = append(args, "-b:a", fmt.Sprintf("%dk", profile.AudioBitrate))
	args = append(args, "-ar", "44100")
	args = append(args, "-ac", "2")
	args = append(args, "-pix_fmt", "yuv420p")

	return args
}

// buildHLSArgs constructs HLS-specific FFmpeg arguments.
func (sm *StreamManager) buildHLSArgs(config StreamConfig) []string {
	args := []string{}
	args = append(args, "-f", "hls")
	args = append(args, "-hls_time", strconv.Itoa(config.HLSSegmentTime))
	args = append(args, "-hls_list_size", strconv.Itoa(config.HLSListSize))

	if len(config.HLSFlags) > 0 {
		args = append(args, "-hls_flags", strings.Join(config.HLSFlags, "+"))
	} else {
		args = append(args, "-hls_flags", "delete_segments+append_list")
	}

	args = append(args, "-hls_segment_filename", filepath.Join(filepath.Dir(config.Output), "segment_%03d.ts"))
	return args
}

// buildDASHArgs constructs DASH-specific FFmpeg arguments.
func (sm *StreamManager) buildDASHArgs(config StreamConfig) []string {
	args := []string{}
	args = append(args, "-f", "dash")
	args = append(args, "-seg_duration", "4")
	args = append(args, "-window_size", "5")
	args = append(args, "-extra_window_size", "10")
	return args
}

// buildRTSPArgs constructs RTSP-specific FFmpeg arguments.
func (sm *StreamManager) buildRTSPArgs(config StreamConfig) []string {
	args := []string{}
	args = append(args, "-f", "rtsp")
	args = append(args, "-rtsp_transport", "tcp")
	return args
}

// executeStreamCommand executes the FFmpeg streaming command.
func (sm *StreamManager) executeStreamCommand(session *StreamSession, cmd *exec.Cmd) error {
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	go sm.monitorStreamOutput(session, stderr)
	err = cmd.Wait()
	if err != nil && session.ctx.Err() == nil {
		return fmt.Errorf("ffmpeg process failed: %w", err)
	}

	return nil
}

// monitorStreamOutput monitors FFmpeg output for statistics and errors.
func (sm *StreamManager) monitorStreamOutput(session *StreamSession, stderr io.ReadCloser) {
	defer stderr.Close()
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		session.logger.Debug().Str("ffmpeg_output", line).Msg("FFmpeg output")
		if strings.Contains(line, "frame=") {
			progress := sm.parseProgressInfo(line)
			if progress != nil {
				select {
				case session.progressChan <- *progress:
				default:
				}
			}
		}
		if strings.Contains(line, "Error") || strings.Contains(line, "error") {
			session.logger.Warn().Str("error", line).Msg("FFmpeg error detected")
		}
	}
	if err := scanner.Err(); err != nil {
		session.logger.Error().Err(err).Msg("Error reading FFmpeg output")
	}
}

// parseProgressInfo parses FFmpeg progress output for streaming.
func (sm *StreamManager) parseProgressInfo(line string) *ProgressInfo {
	progress := &ProgressInfo{
		Timestamp: time.Now(),
	}
	if frameMatch := strings.Index(line, "frame="); frameMatch != -1 {
		frameStr := strings.Fields(line[frameMatch+6:])[0]
		if frame, err := strconv.ParseInt(frameStr, 10, 64); err == nil {
			progress.Frame = frame
		}
	}
	if fpsMatch := strings.Index(line, "fps="); fpsMatch != -1 {
		fpsStr := strings.Fields(line[fpsMatch+4:])[0]
		if fps, err := strconv.ParseFloat(fpsStr, 64); err == nil {
			progress.FPS = fps
		}
	}
	if bitrateMatch := strings.Index(line, "bitrate="); bitrateMatch != -1 {
		progress.Bitrate = strings.Fields(line[bitrateMatch+8:])[0]
	}
	if timeMatch := strings.Index(line, "time="); timeMatch != -1 {
		timeStr := strings.Fields(line[timeMatch+5:])[0]
		if duration, err := time.ParseDuration(strings.Replace(timeStr, ":", "h", 1)); err == nil {
			progress.Time = duration
		}
	}
	if speedMatch := strings.Index(line, "speed="); speedMatch != -1 {
		progress.Speed = strings.Fields(line[speedMatch+6:])[0]
	}
	return progress
}

// validateStreamConfig validates the streaming configuration.
func (sm *StreamManager) validateStreamConfig(config StreamConfig) error {
	if config.Input == "" {
		return fmt.Errorf("input is required")
	}
	if config.Output == "" {
		return fmt.Errorf("output is required")
	}
	if len(config.Profiles) == 0 {
		return fmt.Errorf("at least one profile is required")
	}
	if config.HLSSegmentTime <= 0 {
		config.HLSSegmentTime = 6
	}
	if config.HLSListSize <= 0 {
		config.HLSListSize = 5
	}
	return nil
}

// GetStats returns the current streaming statistics.
func (session *StreamSession) GetStats() *StreamStats {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	return session.Stats
}

// GetStatus returns the current session status.
func (session *StreamSession) GetStatus() ProcessStatus {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	return session.Status
}

// GetProgressChannel returns the channel for streaming progress updates.
func (session *StreamSession) GetProgressChannel() <-chan ProgressInfo {
	return session.progressChan
}

// GetErrorChannel returns the channel for streaming errors.
func (session *StreamSession) GetErrorChannel() <-chan error {
	return session.errorChan
}
