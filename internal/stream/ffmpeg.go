package stream

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/zerohalo/weatherrupert/internal/plog"
)

// FFmpeg manages a running FFmpeg subprocess.
type FFmpeg struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	warnings atomic.Int64 // stderr warning lines from FFmpeg
}

// Start launches FFmpeg, returning an FFmpeg handle with accessible stdin/stdout pipes.
// The caller should write raw RGBA frames to Stdin() and read MPEG-TS from Stdout().
// label is a short identifier (e.g. ZIP code) included in log messages.
func Start(width, height, frameRate int, music *MusicSource, label string, videoMaxRate string) (*FFmpeg, error) {
	flog := plog.New("ffmpeg", label)
	args := buildArgs(width, height, frameRate, music, videoMaxRate)
	cmd := exec.Command("ffmpeg", args...)

	flog.Printf("args: %v", args)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: stdout pipe: %w", err)
	}

	// Capture FFmpeg stderr and pipe each line through our logger so
	// warnings about buffer underruns, codec issues, etc. are visible.
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: stderr pipe: %w", err)
	}

	// If audio comes from a MusicRelay pipe, pass it as ExtraFiles[0] → fd 3.
	if music.RelayPipe != nil {
		cmd.ExtraFiles = []*os.File{music.RelayPipe}
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg: start: %w", err)
	}

	f := &FFmpeg{cmd: cmd, stdin: stdin, stdout: stdout}

	// Log FFmpeg stderr lines in a background goroutine.
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			f.warnings.Add(1)
			flog.Printf("%s", scanner.Text())
		}
	}()

	// Verify FFmpeg didn't exit immediately (e.g. bad args, missing codec).
	// Wait briefly, then check if the process is still alive via signal 0.
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		// Process already exited. Call Wait to collect exit status and clean up.
		waitErr := cmd.Wait()
		return nil, fmt.Errorf("ffmpeg: exited immediately: %w", waitErr)
	}

	return f, nil
}

// Stdin returns the writer for raw RGBA frame data.
func (f *FFmpeg) Stdin() io.WriteCloser { return f.stdin }

// Stdout returns the reader for MPEG-TS encoded output.
func (f *FFmpeg) Stdout() io.ReadCloser { return f.stdout }

// Wait waits for FFmpeg to exit and returns any error.
func (f *FFmpeg) Wait() error { return f.cmd.Wait() }

// Warnings returns the number of stderr warning lines emitted by FFmpeg.
func (f *FFmpeg) Warnings() int64 { return f.warnings.Load() }

// Kill terminates the FFmpeg process and waits for it to exit.
func (f *FFmpeg) Kill() error {
	if f.cmd.Process == nil {
		return nil
	}
	_ = f.cmd.Process.Kill()
	return f.cmd.Wait()
}

func buildArgs(width, height, frameRate int, music *MusicSource, videoMaxRate string) []string {
	videoSize := fmt.Sprintf("%dx%d", width, height)
	fps := strconv.Itoa(frameRate)

	args := []string{
		"-loglevel", "warning",
		// Video input: raw RGBA from stdin.
		// Raise thread_queue_size so the video demuxer doesn't block
		// the audio input thread during encoding spikes.
		"-thread_queue_size", "512",
		"-f", "rawvideo",
		"-pixel_format", "rgba",
		"-video_size", videoSize,
		"-framerate", fps,
		"-i", "pipe:0",
	}

	// Audio input (silence or music playlist)
	args = append(args, music.FFmpegArgs()...)

	// Output encoding
	// Baseline profile + zerolatency eliminates B-frames and CABAC entropy
	// coding — the two most CPU-intensive parts of x264 — so multiple
	// concurrent pipelines can run without starving each other's audio.
	args = append(args,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-profile:v", "baseline",
		"-crf", "28",
	)

	// VBV rate limiting smooths out bitrate so IPTV clients don't underrun
	// during static slides. Set VIDEO_MAXRATE="" to disable.
	if videoMaxRate != "" {
		args = append(args, "-maxrate", videoMaxRate, "-bufsize", videoMaxRate)
	}

	args = append(args,
		"-threads", "1",
		"-g", strconv.Itoa(frameRate), // keyframe every 1 second (enables fast first segment)
		"-pix_fmt", "yuv420p",
	)

	// Audio encoding: pass through the source codec when possible (relay or
	// direct stream) to avoid the decode→encode CPU cost entirely.  Fall back
	// to AAC for local files and generated silence where copy isn't possible.
	if music.RelayPipe != nil || music.StreamURL != "" {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "128k")
	}

	args = append(args, "-f", "mpegts", "pipe:1")

	return args
}
