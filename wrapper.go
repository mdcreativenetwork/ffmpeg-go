package ffmpeg

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Wrapper manages FFmpeg processes with lifecycle control and progress monitoring.
type Wrapper struct {
	FFmpegPath  string
	probePath   string
	logger      zerolog.Logger
	processes   map[string]*Process
	processesMu sync.RWMutex
}

// Process represents an active FFmpeg process.
type Process struct {
	ID         string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	stderr     io.ReadCloser
	ctx        context.Context
	cancel     context.CancelFunc
	startTime  time.Time
	status     ProcessStatus
	statusMu   sync.RWMutex
	progressCh chan Progress
	errorCh    chan error
	logger     zerolog.Logger
}

// ProcessStatus represents the status of an FFmpeg process.
type ProcessStatus int

const (
	StatusPending ProcessStatus = iota
	StatusRunning
	StatusCompleted
	StatusFailed
	StatusCancelled
)

// String returns the string representation of ProcessStatus.
func (s ProcessStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Progress represents FFmpeg processing progress information.
type Progress struct {
	Frame         int64         `json:"frame"`          // Current frame
	FPS           float64       `json:"fps"`            // Frames per second
	Quality       float64       `json:"quality"`        // Encoding quality
	Size          int64         `json:"size"`           // Output size in bytes
	Time          time.Duration `json:"time"`           // Current time
	Bitrate       string        `json:"bitrate"`        // Current bitrate
	Speed         float64       `json:"speed"`          // Encoding speed
	Progress      float64       `json:"progress"`       // Progress percentage
	ETA           time.Duration `json:"eta"`            // Estimated time remaining
	TotalDuration time.Duration `json:"total_duration"` // Total duration
}

// Config holds configuration for the FFmpeg wrapper.
type Config struct {
	BinPath        string        `yaml:"bin_path"`        // FFmpeg binary path
	ProbePath      string        `yaml:"probe_path"`      // FFprobe binary path
	MaxProcesses   int           `yaml:"max_processes"`   // Maximum concurrent processes
	ProcessTimeout time.Duration `yaml:"process_timeout"` // Process timeout
	LogLevel       string        `yaml:"log_level"`       // FFmpeg log level
	HardwareAccel  bool          `yaml:"hardware_accel"`  // Enable hardware acceleration
	EnableGPU      bool          `yaml:"enable_gpu"`      // Enable GPU support
}

// DefaultConfig returns a default FFmpeg configuration.
func DefaultConfig() *Config {
	return &Config{
		BinPath:        "ffmpeg",
		ProbePath:      "ffprobe",
		MaxProcesses:   50,
		ProcessTimeout: 1 * time.Hour,
		LogLevel:       "warning",
		HardwareAccel:  false,
		EnableGPU:      false,
	}
}

// NewWrapper creates a new FFmpeg wrapper instance.
func NewWrapper(config *Config, logger zerolog.Logger) (*Wrapper, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if err := validateBinary(config.BinPath); err != nil {
		return nil, fmt.Errorf("invalid ffmpeg binary: %w", err)
	}

	if err := validateBinary(config.ProbePath); err != nil {
		return nil, fmt.Errorf("invalid ffprobe binary: %w", err)
	}

	wrapper := &Wrapper{
		FFmpegPath: config.BinPath,
		probePath:  config.ProbePath,
		logger:     logger.With().Str("component", "ffmpeg").Logger(),
		processes:  make(map[string]*Process),
	}

	wrapper.logger.Info().
		Str("ffmpeg_path", config.BinPath).
		Str("ffprobe_path", config.ProbePath).
		Msg("FFmpeg wrapper initialized")

	return wrapper, nil
}

// GetVersion retrieves the FFmpeg version information.
func (w *Wrapper) GetVersion(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, w.FFmpegPath, "-version")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get ffmpeg version: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), nil
	}

	return "", fmt.Errorf("unable to parse ffmpeg version")
}

// GetMediaInfo retrieves media information using ffprobe.
func (w *Wrapper) GetMediaInfo(ctx context.Context, inputPath string) (*MediaInfo, error) {
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		inputPath,
	}

	cmd := exec.CommandContext(ctx, w.probePath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to probe media: %w", err)
	}

	info, err := parseMediaInfo(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse media info: %w", err)
	}

	return info, nil
}

// StartProcess initiates a new FFmpeg process with the given arguments.
func (w *Wrapper) StartProcess(ctx context.Context, processID string, args []string) (*Process, error) {
	w.processesMu.Lock()
	defer w.processesMu.Unlock()

	if _, exists := w.processes[processID]; exists {
		return nil, fmt.Errorf("process %s already exists", processID)
	}

	processCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(processCtx, w.FFmpegPath, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		cancel()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		cancel()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	process := &Process{
		ID:         processID,
		cmd:        cmd,
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		ctx:        processCtx,
		cancel:     cancel,
		startTime:  time.Now(),
		status:     StatusPending,
		progressCh: make(chan Progress, 100),
		errorCh:    make(chan error, 10),
		logger:     w.logger.With().Str("process_id", processID).Logger(),
	}

	w.processes[processID] = process

	if err := cmd.Start(); err != nil {
		process.cleanup()
		delete(w.processes, processID)
		return nil, fmt.Errorf("failed to start ffmpeg process: %w", err)
	}

	process.setStatus(StatusRunning)
	go process.monitorStderr()
	go process.monitorProcess()

	process.logger.Info().
		Strs("args", args).
		Msg("FFmpeg process started")

	return process, nil
}

// GetProcess retrieves an FFmpeg process by ID.
func (w *Wrapper) GetProcess(processID string) (*Process, bool) {
	w.processesMu.RLock()
	defer w.processesMu.RUnlock()

	process, exists := w.processes[processID]
	return process, exists
}

// StopProcess terminates an FFmpeg process by ID.
func (w *Wrapper) StopProcess(processID string) error {
	w.processesMu.Lock()
	defer w.processesMu.Unlock()

	process, exists := w.processes[processID]
	if !exists {
		return fmt.Errorf("process %s not found", processID)
	}

	process.Stop()
	delete(w.processes, processID)
	return nil
}

// GetActiveProcesses returns all active FFmpeg processes.
func (w *Wrapper) GetActiveProcesses() map[string]*Process {
	w.processesMu.RLock()
	defer w.processesMu.RUnlock()

	processes := make(map[string]*Process)
	for id, process := range w.processes {
		if process.IsRunning() {
			processes[id] = process
		}
	}
	return processes
}

// CleanupFinishedProcesses removes completed processes from memory.
func (w *Wrapper) CleanupFinishedProcesses() {
	w.processesMu.Lock()
	defer w.processesMu.Unlock()

	for id, process := range w.processes {
		if !process.IsRunning() && time.Since(process.startTime) > 5*time.Minute {
			process.cleanup()
			delete(w.processes, id)
		}
	}
}

// Shutdown gracefully terminates all FFmpeg processes.
func (w *Wrapper) Shutdown(ctx context.Context) error {
	w.processesMu.Lock()
	defer w.processesMu.Unlock()

	w.logger.Info().Msg("Shutting down FFmpeg wrapper")

	var wg sync.WaitGroup
	for _, process := range w.processes {
		wg.Add(1)
		go func(p *Process) {
			defer wg.Done()
			p.Stop()
		}(process)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		w.logger.Info().Msg("All FFmpeg processes stopped")
	case <-ctx.Done():
		w.logger.Warn().Msg("Timeout waiting for FFmpeg processes to stop")
	}

	return nil
}

// IsRunning checks if the process is currently running.
func (p *Process) IsRunning() bool {
	p.statusMu.RLock()
	defer p.statusMu.RUnlock()
	return p.status == StatusRunning
}

// GetStatus returns the current process status.
func (p *Process) GetStatus() ProcessStatus {
	p.statusMu.RLock()
	defer p.statusMu.RUnlock()
	return p.status
}

// setStatus sets the process status.
func (p *Process) setStatus(status ProcessStatus) {
	p.statusMu.Lock()
	defer p.statusMu.Unlock()
	p.status = status
}

// GetProgressChannel returns the channel for progress updates.
func (p *Process) GetProgressChannel() <-chan Progress {
	return p.progressCh
}

// GetErrorChannel returns the channel for error notifications.
func (p *Process) GetErrorChannel() <-chan error {
	return p.errorCh
}

// Stop gracefully terminates the FFmpeg process.
func (p *Process) Stop() {
	p.logger.Info().Msg("Stopping FFmpeg process")
	if p.stdin != nil {
		p.stdin.Write([]byte("q\n"))
		p.stdin.Close()
	}
	time.Sleep(2 * time.Second)
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.cancel()
	p.setStatus(StatusCancelled)
}

// Wait waits for the FFmpeg process to complete.
func (p *Process) Wait() error {
	if p.cmd == nil {
		return fmt.Errorf("no command to wait for")
	}
	return p.cmd.Wait()
}

// monitorStderr monitors FFmpeg stderr for progress and errors.
func (p *Process) monitorStderr() {
	defer p.cleanup()
	scanner := bufio.NewScanner(p.stderr)
	progressRegex := regexp.MustCompile(`frame=\s*(\d+)\s+fps=\s*([\d.]+)\s+q=([\d.-]+)\s+size=\s*(\d+)kB\s+time=(\d{2}:\d{2}:\d{2}\.\d{2})\s+bitrate=\s*([\d.]+kbits/s)\s+speed=\s*([\d.]+)x`)

	for scanner.Scan() {
		line := scanner.Text()
		p.logger.Debug().Str("stderr", line).Msg("FFmpeg stderr output")
		if matches := progressRegex.FindStringSubmatch(line); matches != nil {
			progress := parseProgress(matches)
			select {
			case p.progressCh <- progress:
			default:
			}
		}
		if strings.Contains(strings.ToLower(line), "error") {
			select {
			case p.errorCh <- fmt.Errorf("ffmpeg error: %s", line):
			default:
			}
		}
	}
	if err := scanner.Err(); err != nil {
		p.logger.Error().Err(err).Msg("Error reading stderr")
	}
}

// monitorProcess monitors the FFmpeg process lifecycle.
func (p *Process) monitorProcess() {
	defer func() {
		close(p.progressCh)
		close(p.errorCh)
	}()
	err := p.Wait()
	if err != nil {
		p.logger.Error().Err(err).Msg("FFmpeg process finished with error")
		p.setStatus(StatusFailed)
		select {
		case p.errorCh <- err:
		default:
		}
	} else {
		p.logger.Info().Msg("FFmpeg process completed successfully")
		p.setStatus(StatusCompleted)
	}
}

// cleanup releases process resources.
func (p *Process) cleanup() {
	if p.stdin != nil {
		p.stdin.Close()
	}
	if p.stdout != nil {
		p.stdout.Close()
	}
	if p.stderr != nil {
		p.stderr.Close()
	}
	if p.cancel != nil {
		p.cancel()
	}
}

// validateBinary checks if the binary is valid and executable.
func validateBinary(path string) error {
	if path == "" {
		return fmt.Errorf("binary path is empty")
	}
	if !filepath.IsAbs(path) {
		if _, err := exec.LookPath(path); err != nil {
			return fmt.Errorf("binary not found in PATH: %w", err)
		}
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("binary not found: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a binary")
	}
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("binary is not executable")
	}
	return nil
}

// parseProgress parses FFmpeg progress output.
func parseProgress(matches []string) Progress {
	progress := Progress{}
	if len(matches) >= 8 {
		if frame, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			progress.Frame = frame
		}
		if fps, err := strconv.ParseFloat(matches[2], 64); err == nil {
			progress.FPS = fps
		}
		if quality, err := strconv.ParseFloat(matches[3], 64); err == nil {
			progress.Quality = quality
		}
		if size, err := strconv.ParseInt(matches[4], 10, 64); err == nil {
			progress.Size = size * 1024
		}
		if duration, err := parseDuration(matches[5]); err == nil {
			progress.Time = duration
		}
		progress.Bitrate = matches[6]
		if speed, err := strconv.ParseFloat(matches[7], 64); err == nil {
			progress.Speed = speed
		}
	}
	return progress
}

// parseDuration parses FFmpeg time format (HH:MM:SS.FF).
func parseDuration(timeStr string) (time.Duration, error) {
	parts := strings.Split(timeStr, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid time format")
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, err
	}
	duration := time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second))
	return duration, nil
}

// parseMediaInfo parses ffprobe output into MediaInfo.
func parseMediaInfo(output []byte) (*MediaInfo, error) {
	var probeResult struct {
		Format struct {
			Duration   string `json:"duration"`
			Size       string `json:"size"`
			BitRate    string `json:"bit_rate"`
			FormatName string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			RFrameRate string `json:"r_frame_rate"`
			BitRate    string `json:"bit_rate"`
			PixFmt     string `json:"pix_fmt"`
			SampleRate string `json:"sample_rate"`
			Channels   int    `json:"channels"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(output, &probeResult); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	mediaInfo := &MediaInfo{
		Format: probeResult.Format.FormatName,
	}

	if duration, err := strconv.ParseFloat(probeResult.Format.Duration, 64); err == nil {
		mediaInfo.Duration = time.Duration(duration * float64(time.Second))
	}

	if size, err := strconv.ParseInt(probeResult.Format.Size, 10, 64); err == nil {
		mediaInfo.Size = size
	}

	if bitrate, err := strconv.ParseInt(probeResult.Format.BitRate, 10, 64); err == nil {
		mediaInfo.Bitrate = bitrate
	}

	for _, stream := range probeResult.Streams {
		switch stream.CodecType {
		case "video":
			mediaInfo.Video = &VideoInfo{
				Codec:       stream.CodecName,
				Width:       stream.Width,
				Height:      stream.Height,
				PixelFormat: stream.PixFmt,
			}
			if parts := strings.Split(stream.RFrameRate, "/"); len(parts) == 2 {
				if num, err1 := strconv.ParseFloat(parts[0], 64); err1 == nil {
					if den, err2 := strconv.ParseFloat(parts[1], 64); err2 == nil && den != 0 {
						mediaInfo.Video.Framerate = num / den
					}
				}
			}
			if bitrate, err := strconv.ParseInt(stream.BitRate, 10, 64); err == nil {
				mediaInfo.Video.Bitrate = bitrate
			}
		case "audio":
			mediaInfo.Audio = &AudioInfo{
				Codec:    stream.CodecName,
				Channels: stream.Channels,
			}
			if sampleRate, err := strconv.Atoi(stream.SampleRate); err == nil {
				mediaInfo.Audio.SampleRate = sampleRate
			}
			if bitrate, err := strconv.ParseInt(stream.BitRate, 10, 64); err == nil {
				mediaInfo.Audio.Bitrate = bitrate
			}
		}
	}

	return mediaInfo, nil
}
