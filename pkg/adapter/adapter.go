package adapter

import (
	"io"
	"net/http"
)

type MediaStream struct {
	Index        int    `json:"index"`
	Type         string `json:"type"` // "Audio", "Subtitle", "Video"
	Language     string `json:"language"`
	DisplayTitle string `json:"display_title"`
	Codec        string `json:"codec"`
	IsExternal   bool   `json:"is_external"`
	Path         string `json:"path,omitempty"`
	Key          string `json:"key,omitempty"` // server-specific stream key (Plex)
}

type MediaItem struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	OriginalTitle string        `json:"original_title,omitempty"`
	Year          int           `json:"year,omitempty"`
	Path          string        `json:"path"`
	ServerType    string        `json:"server_type"` // emby, jellyfin, plex
	Streams       []MediaStream `json:"streams"`
	HasBilingual  bool          `json:"has_bilingual"`
	TargetAudio   string        `json:"target_audio,omitempty"`
	MatchedBy     string        `json:"matched_by,omitempty"` // "audio" | "country"
	Countries     []string      `json:"countries,omitempty"`
	DateCreated   string        `json:"date_created,omitempty"`
	RuntimeSec    int           `json:"runtime_sec,omitempty"`
	Container     string        `json:"container,omitempty"`
	MediaSourceID string        `json:"media_source_id,omitempty"`
	// MediaURL is the direct playable location of the underlying file when the
	// server exposes one (e.g. the target URL of a .strm file). It lets ffprobe
	// read container headers without going through the media server.
	MediaURL  string `json:"media_url,omitempty"`
	HasPoster bool   `json:"has_poster"`
}

type MediaServerAdapter interface {
	Ping() error
	// ListMovies returns all movies; when targetAudioLang is non-empty only movies
	// whose audio track or production country matches are returned.
	ListMovies(targetAudioLang, nativeLang string) ([]MediaItem, error)
	RefreshItem(itemID string) error
	// FetchSubtitle downloads a subtitle stream (embedded or external) converted
	// to SRT by the media server itself, so no direct file access is needed.
	FetchSubtitle(item MediaItem, stream MediaStream) ([]byte, error)
	// FetchPoster returns the primary image of an item.
	FetchPoster(itemID string, maxHeight int) (*http.Response, error)
}

func readAllLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 32<<20))
}
