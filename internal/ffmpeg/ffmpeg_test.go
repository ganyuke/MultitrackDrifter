package ffmpeg

import "testing"

func TestStreamSummariesIncludeMultipleAudioStreams(t *testing.T) {
	probe := Probe{
		Format: Format{Duration: "12.000000"},
		Streams: []Stream{
			{Index: 0, CodecType: "video", CodecName: "h264", AvgFrameRate: "30000/1001", Width: 1280, Height: 720, Tags: map[string]string{"title": "Gameplay"}},
			{Index: 1, CodecType: "audio", CodecName: "aac", Channels: 2, ChannelLayout: "stereo", Tags: map[string]string{"title": "Game Audio"}},
			{Index: 2, CodecType: "audio", CodecName: "aac", Channels: 1, ChannelLayout: "mono", Tags: map[string]string{"title": "Mic"}},
			{Index: 3, CodecType: "audio", CodecName: "aac", Channels: 2, ChannelLayout: "stereo", Tags: map[string]string{"title": "Discord"}},
		},
	}

	got := StreamSummaries(probe)
	if len(got) != 4 {
		t.Fatalf("got %d streams, want 4", len(got))
	}
	want := []struct {
		index int
		kind  string
		label string
	}{
		{0, "video", "Gameplay"},
		{1, "audio", "Game Audio"},
		{2, "audio", "Mic"},
		{3, "audio", "Discord"},
	}
	for i := range want {
		if got[i].Index != want[i].index || got[i].Kind != want[i].kind || got[i].Label != want[i].label {
			t.Fatalf("stream %d = %#v, want index=%d kind=%s label=%s", i, got[i], want[i].index, want[i].kind, want[i].label)
		}
	}
	if got[0].FPSNum != 30000 || got[0].FPSDen != 1001 {
		t.Fatalf("video FPS = %d/%d, want 30000/1001", got[0].FPSNum, got[0].FPSDen)
	}
	if got[1].FPSNum != 0 || got[1].FPSDen != 0 {
		t.Fatalf("audio FPS metadata should be zero, got %d/%d", got[1].FPSNum, got[1].FPSDen)
	}
}
