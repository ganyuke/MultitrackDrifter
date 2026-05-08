package timeline

import "fmt"

func ClipLocalMS(wallclockMS, clipStartMS, softNudgeMS int64) int64 {
	return wallclockMS - clipStartMS + softNudgeMS
}

func WallclockMS(clipLocalMS, clipStartMS int64) int64 {
	return clipLocalMS + clipStartMS
}

func FormatMS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

func FramesFromMS(ms, fpsNum, fpsDen int64) int64 {
	if fpsNum <= 0 || fpsDen <= 0 {
		fpsNum, fpsDen = 30, 1
	}
	return (ms*fpsNum + 500*fpsDen) / (1000 * fpsDen)
}

func TimecodeFromMS(ms, fpsNum, fpsDen int64) string {
	if fpsNum <= 0 || fpsDen <= 0 {
		fpsNum, fpsDen = 30, 1
	}
	fps := fpsNum / fpsDen
	if fps <= 0 {
		fps = 30
	}
	frames := FramesFromMS(ms, fpsNum, fpsDen)
	h := frames / (fps * 3600)
	frames %= fps * 3600
	m := frames / (fps * 60)
	frames %= fps * 60
	s := frames / fps
	f := frames % fps
	return fmt.Sprintf("%02d:%02d:%02d:%02d", h, m, s, f)
}
