package stream

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AudioThreadQueueSize is the FFmpeg -thread_queue_size for relay pipe audio.
// Each MP3 packet is ~26ms, so 64 packets ≈ 1.7s of audio.  This limits how
// much stale audio accumulates in FFmpeg's internal buffer during a
// SIGSTOP/SIGCONT cycle (we can drain the OS pipe but not FFmpeg's queue).
// The Hub's flushWindow is derived from this value.
const AudioThreadQueueSize = 64

var audioExtensions = map[string]bool{
	".mp3":  true,
	".flac": true,
	".ogg":  true,
	".wav":  true,
	".m4a":  true,
	".aac":  true,
}

// MusicSource represents available background audio.
// Exactly one of PlaylistPath, StreamURL, or RelayPipe should be set; if all
// are empty, FFmpegArgs returns a silent audio source.
type MusicSource struct {
	HasMusic     bool
	PlaylistPath string   // path to FFmpeg concat demuxer playlist file (local files)
	StreamURL    string   // URL of an HTTP/Icecast audio stream (direct connection)
	RelayPipe    *os.File // read end of a MusicRelay pipe (shared stream via fd 3)
}

// NewStreamSource returns a MusicSource that streams from the given URL.
func NewStreamSource(url string) *MusicSource {
	return &MusicSource{HasMusic: true, StreamURL: url}
}

// FFmpegArgs returns the FFmpeg audio input arguments for this source.
func (m *MusicSource) FFmpegArgs() []string {
	switch {
	case m.RelayPipe != nil:
		// Audio arrives via an OS pipe from a shared MusicRelay.
		// The pipe is passed as ExtraFiles[0] which becomes fd 3.
		// Keep thread_queue_size modest: a large queue (e.g. 512) means
		// ~13s of stale audio after a SIGSTOP/SIGCONT cycle because we
		// can drain the OS pipe but not FFmpeg's internal thread queue.
		// 64 packets ≈ 1.7s of MP3, enough headroom for encode spikes.
		return []string{"-thread_queue_size", strconv.Itoa(AudioThreadQueueSize), "-i", "pipe:3"}
	case m.PlaylistPath != "":
		return []string{
			"-thread_queue_size", "512",
			"-stream_loop", "-1",
			"-f", "concat",
			"-safe", "0",
			"-i", m.PlaylistPath,
		}
	case m.StreamURL != "":
		// -reconnect flags keep FFmpeg running through brief stream interruptions.
		return []string{
			"-thread_queue_size", "512",
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_delay_max", "5",
			"-i", m.StreamURL,
		}
	default:
		// Generate digital silence
		return []string{"-f", "lavfi", "-i", "aevalsrc=0:c=stereo:s=44100"}
	}
}

// NewRelaySource returns a MusicSource backed by a relay pipe.
func NewRelaySource(pipe *os.File) *MusicSource {
	return &MusicSource{HasMusic: true, RelayPipe: pipe}
}

// ScanMusicDir scans dir for audio files and returns a MusicSource.
// If dir is empty or missing, returns a silence source.
func ScanMusicDir(dir string) (*MusicSource, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("music: directory %q not found, using silence", dir)
			return &MusicSource{HasMusic: false}, nil
		}
		return nil, fmt.Errorf("music: reading %q: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if audioExtensions[ext] {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}

	if len(files) == 0 {
		log.Printf("music: no audio files found in %q, using silence", dir)
		return &MusicSource{HasMusic: false}, nil
	}

	log.Printf("music: found %d audio file(s) in %q", len(files), dir)

	playlistPath, err := buildConcatPlaylist(files)
	if err != nil {
		return nil, fmt.Errorf("music: building playlist: %w", err)
	}

	return &MusicSource{HasMusic: true, PlaylistPath: playlistPath}, nil
}

// buildConcatPlaylist writes an FFmpeg concat demuxer playlist to /tmp.
func buildConcatPlaylist(files []string) (string, error) {
	path := "/tmp/weatherrupert_music.txt"

	var sb strings.Builder
	sb.WriteString("ffconcat version 1.0\n")
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("file '%s'\n", f))
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		return "", err
	}
	return path, nil
}
