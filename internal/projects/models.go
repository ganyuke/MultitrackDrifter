package projects

// Package projects contains shared POC domain model names used by API and export
// layers. The SQL schema is the source of truth for persistence; these types are
// intentionally small DTOs for future service extraction.

type Project struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	OwnerUsername string `json:"ownerUsername"`
}

type Perspective struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"projectId"`
	Name      string `json:"name"`
	SortOrder int64  `json:"sortOrder"`
}

type Track struct {
	ID            int64  `json:"id"`
	ProjectID     int64  `json:"projectId"`
	PerspectiveID int64  `json:"perspectiveId"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	SortOrder     int64  `json:"sortOrder"`
}

type Clip struct {
	ID               int64  `json:"id"`
	ProjectID        int64  `json:"projectId"`
	PerspectiveID    int64  `json:"perspectiveId"`
	TrackID          int64  `json:"trackId"`
	SourceAssetID    int64  `json:"sourceAssetId"`
	SourceRevisionID int64  `json:"sourceRevisionId"`
	HLSAssetID       int64  `json:"hlsAssetId"`
	MediaKind        string `json:"mediaKind"`
	WallclockStartMS int64  `json:"wallclockStartMs"`
	DurationMS       int64  `json:"durationMs"`
	FPSNum           int64  `json:"fpsNum"`
	FPSDen           int64  `json:"fpsDen"`
	StreamIndex      int    `json:"streamIndex"`
	DisplayName      string `json:"displayName"`
	IngestStatus     string `json:"ingestStatus"`
}
