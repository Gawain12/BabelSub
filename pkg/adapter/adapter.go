package adapter

type MediaStream struct {
	Index        int    `json:"index"`
	Type         string `json:"type"` // "Audio", "Subtitle", "Video"
	Language     string `json:"language"`
	DisplayTitle string `json:"display_title"`
	Codec        string `json:"codec"`
	IsExternal   bool   `json:"is_external"`
	Path         string `json:"path,omitempty"`
}

type MediaItem struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	OriginalTitle  string        `json:"original_title,omitempty"`
	Year           int           `json:"year,omitempty"`
	Path           string        `json:"path"`
	ServerType     string        `json:"server_type"` // emby, jellyfin, plex
	Streams        []MediaStream `json:"streams"`
	HasBilingual   bool          `json:"has_bilingual"`
	TargetAudio    string        `json:"target_audio,omitempty"`
}

type MediaServerAdapter interface {
	Ping() error
	ListMovies(targetAudioLang string) ([]MediaItem, error)
	RefreshItem(itemID string) error
}
