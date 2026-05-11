package hlsassets

import (
	"fmt"
	"strings"
)

const (
	RootDir             = "previews"
	PlaylistDir         = RootDir + "/playlists"
	SegmentDir          = RootDir + "/hls"
	PlaylistFilename    = "index.m3u8"
	SegmentExtension    = ".ts"
	PlaylistContentType = "application/vnd.apple.mpegurl"
	SegmentContentType  = "video/MP2T"
	HashPrefixChars     = 2
	HashFanoutChars     = 4
)

func PlaylistPath(assetID int64) string {
	return fmt.Sprintf("%s/%d.m3u8", PlaylistDir, assetID)
}

func SegmentPath(hexDigest string) string {
	return fmt.Sprintf("%s/%s/%s/%s.ts", SegmentDir, hexDigest[:HashPrefixChars], hexDigest[HashPrefixChars:HashFanoutChars], hexDigest)
}

func SegmentURI(segmentPath string) string {
	return "../" + strings.TrimPrefix(segmentPath, RootDir+"/")
}
