package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Probe struct {
	Streams []Stream `json:"streams"`
	Format  Format   `json:"format"`
}

type Stream struct {
	Index         int               `json:"index"`
	CodecType     string            `json:"codec_type"`
	CodecName     string            `json:"codec_name"`
	Duration      string            `json:"duration"`
	AvgFrameRate  string            `json:"avg_frame_rate"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	Channels      int               `json:"channels"`
	ChannelLayout string            `json:"channel_layout"`
	Tags          map[string]string `json:"tags"`
}

type Format struct {
	Duration string `json:"duration"`
	Size     string `json:"size"`
}

type Metadata struct {
	DurationMS int64
	FPSNum     int64
	FPSDen     int64
	Kind       string
}

type StreamSummary struct {
	Index         int    `json:"index"`
	Kind          string `json:"kind"`
	Codec         string `json:"codec"`
	Label         string `json:"label"`
	Language      string `json:"language,omitempty"`
	DurationMS    int64  `json:"durationMs"`
	FPSNum        int64  `json:"fpsNum,omitempty"`
	FPSDen        int64  `json:"fpsDen,omitempty"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	Channels      int    `json:"channels,omitempty"`
	ChannelLayout string `json:"channelLayout,omitempty"`
}

type Runner struct {
	FFmpeg  string
	FFprobe string
}

type TranscodeOptions struct {
	Preset         string
	SegmentSeconds int
	Progress       func(Progress)
}

type Progress struct {
	TimeMS  int64
	Frame   int64
	FPS     float64
	Bitrate string
	Speed   string
	Status  string
}

func (r Runner) Probe(ctx context.Context, input string) (Probe, error) {
	cmd := exec.CommandContext(ctx, r.FFprobe, "-v", "error", "-print_format", "json", "-show_streams", "-show_format", input)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Probe{}, fmt.Errorf("ffprobe: %w: %s", err, stderr.String())
	}
	logFFmpegOutput(ctx, "ffprobe", stderr.String())
	var p Probe
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		return Probe{}, err
	}
	return p, nil
}

func FirstPlayable(p Probe) (streamIndex int, kind string, meta Metadata) {
	for _, s := range p.Streams {
		if s.CodecType == "video" || s.CodecType == "audio" {
			kind, meta = StreamMeta(p, s.Index)
			return s.Index, kind, meta
		}
	}
	return 0, "video", Metadata{}
}

func StreamMeta(p Probe, index int) (kind string, meta Metadata) {
	for _, s := range p.Streams {
		if s.Index != index {
			continue
		}
		kind = s.CodecType
		if kind != "video" && kind != "audio" {
			kind = "video"
		}
		dur := parseSecondsMS(firstNonEmpty(s.Duration, p.Format.Duration))
		fpsNum, fpsDen := parseRate(s.AvgFrameRate)
		if kind == "audio" {
			fpsNum, fpsDen = 0, 0
		}
		return kind, Metadata{DurationMS: dur, FPSNum: fpsNum, FPSDen: fpsDen, Kind: kind}
	}
	return "video", Metadata{}
}

func StreamSummaries(p Probe) []StreamSummary {
	out := make([]StreamSummary, 0, len(p.Streams))
	for _, s := range p.Streams {
		if s.CodecType != "video" && s.CodecType != "audio" {
			continue
		}
		kind, meta := StreamMeta(p, s.Index)
		out = append(out, StreamSummary{
			Index: s.Index, Kind: kind, Codec: s.CodecName, Label: StreamLabel(s),
			Language: strings.TrimSpace(s.Tags["language"]), DurationMS: meta.DurationMS,
			FPSNum: meta.FPSNum, FPSDen: meta.FPSDen, Width: s.Width, Height: s.Height,
			Channels: s.Channels, ChannelLayout: s.ChannelLayout,
		})
	}
	return out
}

func StreamLabel(s Stream) string {
	if title := usefulTag(s.Tags["title"]); title != "" {
		return title
	}
	if handler := usefulTag(s.Tags["handler_name"]); handler != "" {
		return handler
	}
	if s.CodecType == "video" {
		if s.Width > 0 && s.Height > 0 {
			return fmt.Sprintf("Video %d (%dx%d)", s.Index, s.Width, s.Height)
		}
		return fmt.Sprintf("Video %d", s.Index)
	}
	if s.Channels > 0 {
		return fmt.Sprintf("Audio %d (%d ch)", s.Index, s.Channels)
	}
	return fmt.Sprintf("Audio %d", s.Index)
}

func (r Runner) TranscodeHLS(ctx context.Context, input string, outputDir string, streamIndex int, kind string, opts TranscodeOptions) error {
	preset := strings.TrimSpace(opts.Preset)
	if preset == "" {
		preset = "ultrafast"
	}
	segmentSeconds := opts.SegmentSeconds
	if segmentSeconds <= 0 {
		segmentSeconds = 2
	}

	playlist := filepath.Join(outputDir, "index.m3u8")
	segment := filepath.Join(outputDir, "seg_%03d.ts")
	var args []string
	args = append(args, "-y", "-hide_banner", "-v", "warning", "-progress", "pipe:2", "-nostats", "-i", input, "-map", fmt.Sprintf("0:%d", streamIndex))
	if kind == "audio" {
		args = append(args, "-vn", "-c:a", "aac", "-b:a", "128k")
	} else {
		// Video and audio streams are represented as separate timeline tracks in the app.
		// Force a 2-second GOP by default so HLS seeks land near segment boundaries without split_by_time's non-keyframe cuts.
		gop := strconv.Itoa(segmentSeconds * 30)
		args = append(args, "-an", "-vf", "scale=-2:480", "-r", "30", "-g", gop, "-keyint_min", gop, "-sc_threshold", "0", "-c:v", "libx264", "-preset", preset, "-crf", "28")
	}
	args = append(args, "-hls_time", strconv.Itoa(segmentSeconds), "-hls_playlist_type", "vod", "-hls_segment_filename", segment, playlist)

	cmd := exec.CommandContext(ctx, r.FFmpeg, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stderr: %w", err)
	}
	var captured bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- scanFFmpegProgress(ctx, stderr, &captured, opts.Progress)
	}()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}
	waitErr := cmd.Wait()
	scanErr := <-done
	stderrText := captured.String()
	if waitErr != nil {
		return fmt.Errorf("ffmpeg: %w: %s", waitErr, stderrText)
	}
	if scanErr != nil {
		return fmt.Errorf("ffmpeg progress: %w", scanErr)
	}
	logFFmpegOutput(ctx, "ffmpeg", stderrText)
	return nil
}

func scanFFmpegProgress(ctx context.Context, r io.Reader, captured *bytes.Buffer, cb func(Progress)) error {
	s := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	s.Buffer(buf, 1024*1024)
	state := map[string]string{}
	for s.Scan() {
		line := s.Text()
		if captured.Len() < 256*1024 {
			_, _ = captured.WriteString(line)
			_ = captured.WriteByte('\n')
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		state[key] = value
		if cb != nil && (key == "progress" || isProgressTimeKey(key)) {
			if p, ok := progressFromState(state); ok {
				cb(p)
			}
		}
	}
	if err := s.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func isProgressTimeKey(key string) bool {
	switch key {
	case "out_time_us", "out_time_ms", "out_time":
		return true
	default:
		return false
	}
}

func progressFromState(state map[string]string) (Progress, bool) {
	var p Progress
	if us, err := strconv.ParseInt(firstNonEmpty(state["out_time_us"], state["out_time_ms"]), 10, 64); err == nil && us >= 0 {
		// FFmpeg historically names out_time_ms as milliseconds, but it is microseconds.
		p.TimeMS = us / 1000
	} else if ms := parseClockMS(state["out_time"]); ms >= 0 {
		p.TimeMS = ms
	} else {
		return Progress{}, false
	}
	if frame, err := strconv.ParseInt(state["frame"], 10, 64); err == nil && frame >= 0 {
		p.Frame = frame
	}
	if fps, err := strconv.ParseFloat(state["fps"], 64); err == nil && fps >= 0 {
		p.FPS = fps
	}
	p.Bitrate = strings.TrimSpace(state["bitrate"])
	p.Speed = strings.TrimSpace(state["speed"])
	p.Status = strings.TrimSpace(state["progress"])
	return p, true
}

func progressTimeMS(line string) (int64, bool) {
	key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return 0, false
	}
	switch key {
	case "out_time_us":
		us, err := strconv.ParseInt(value, 10, 64)
		return us / 1000, err == nil && us >= 0
	case "out_time_ms":
		// FFmpeg historically names this value "ms" even though it is microseconds.
		us, err := strconv.ParseInt(value, 10, 64)
		return us / 1000, err == nil && us >= 0
	case "out_time":
		ms := parseClockMS(value)
		return ms, ms >= 0
	default:
		return 0, false
	}
}

func logFFmpegOutput(ctx context.Context, component, output string) {
	output = strings.TrimSpace(output)
	if output == "" {
		return
	}
	level := slog.LevelDebug
	if containsWarning(output) {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, component+": process output", "output", output)
}

func containsWarning(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "warning") || strings.Contains(lower, "error")
}

func usefulTag(v string) string {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "", "videohandler", "soundhandler", "audiohandler", "core media video", "core media audio":
		return ""
	default:
		return v
	}
}

func parseSecondsMS(v string) int64 {
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return int64(f*1000 + 0.5)
}

func parseClockMS(v string) int64 {
	parts := strings.Split(v, ":")
	if len(parts) != 3 {
		return -1
	}
	h, err1 := strconv.ParseInt(parts[0], 10, 64)
	m, err2 := strconv.ParseInt(parts[1], 10, 64)
	s, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || m < 0 || s < 0 {
		return -1
	}
	return h*3600000 + m*60000 + int64(s*1000+0.5)
}

func parseRate(v string) (int64, int64) {
	if v == "" || v == "0/0" {
		return 0, 0
	}
	parts := strings.Split(v, "/")
	if len(parts) != 2 {
		return 0, 0
	}
	n, _ := strconv.ParseInt(parts[0], 10, 64)
	d, _ := strconv.ParseInt(parts[1], 10, 64)
	if d == 0 {
		return 0, 0
	}
	return n, d
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
