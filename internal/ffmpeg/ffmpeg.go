package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func (r Runner) Probe(ctx context.Context, input string) (Probe, error) {
	cmd := exec.CommandContext(ctx, r.FFprobe, "-v", "error", "-print_format", "json", "-show_streams", "-show_format", input)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Probe{}, fmt.Errorf("ffprobe: %w: %s", err, stderr.String())
	}
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

func (r Runner) TranscodeHLS(ctx context.Context, input string, outputDir string, streamIndex int, kind string) error {
	playlist := filepath.Join(outputDir, "index.m3u8")
	segment := filepath.Join(outputDir, "seg_%03d.ts")
	var args []string
	args = append(args, "-y", "-i", input, "-map", fmt.Sprintf("0:%d", streamIndex))
	if kind == "audio" {
		args = append(args, "-vn", "-c:a", "aac", "-b:a", "128k")
	} else {
		// Video and audio streams are represented as separate timeline tracks in the app.
		args = append(args, "-an", "-vf", "scale=-2:480", "-r", "30", "-c:v", "libx264", "-preset", "veryfast", "-crf", "28")
	}
	args = append(args, "-hls_time", "6", "-hls_playlist_type", "vod", "-hls_segment_filename", segment, playlist)
	cmd := exec.CommandContext(ctx, r.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, stderr.String())
	}
	return nil
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
