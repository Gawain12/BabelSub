package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Gawain12/BabelSub/pkg/lang"
)

type EmbyAdapter struct {
	Host       string
	APIKey     string
	IsJellyfin bool
	prefix     string
	client     *http.Client
}

func NewEmbyAdapter(host, apiKey string, isJellyfin bool) *EmbyAdapter {
	prefix := "emby"
	if isJellyfin {
		prefix = "jellyfin"
	}
	return &EmbyAdapter{
		Host:       strings.TrimRight(host, "/"),
		APIKey:     apiKey,
		IsJellyfin: isJellyfin,
		prefix:     prefix,
		client:     &http.Client{Timeout: 15 * time.Second},
	}
}

func (e *EmbyAdapter) Ping() error {
	reqURL := fmt.Sprintf("%s/%s/System/Info?api_key=%s", e.Host, e.prefix, url.QueryEscape(e.APIKey))
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Emby-Token", e.APIKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return nil
}

type embyItemsResponse struct {
	Items            []embyRawItem `json:"Items"`
	TotalRecordCount int           `json:"TotalRecordCount"`
}

type embyRawItem struct {
	ID                  string          `json:"Id"`
	Name                string          `json:"Name"`
	OriginalTitle       string          `json:"OriginalTitle"`
	ProductionYear      int             `json:"ProductionYear"`
	Path                string          `json:"Path"`
	Container           string          `json:"Container"`
	DateCreated         string          `json:"DateCreated"`
	RunTimeTicks        int64           `json:"RunTimeTicks"`
	ProductionLocations []string        `json:"ProductionLocations"`
	MediaStreams        []embyRawStream `json:"MediaStreams"`
	ImageTags           map[string]any  `json:"ImageTags"`
	MediaSources        []struct {
		ID           string          `json:"Id"`
		Path         string          `json:"Path"`
		Protocol     string          `json:"Protocol"`
		Container    string          `json:"Container"`
		MediaStreams []embyRawStream `json:"MediaStreams"`
	} `json:"MediaSources"`
}

type embyRawStream struct {
	Index        int    `json:"Index"`
	Type         string `json:"Type"`
	Language     string `json:"Language"`
	DisplayTitle string `json:"DisplayTitle"`
	Codec        string `json:"Codec"`
	IsExternal   bool   `json:"IsExternal"`
	Path         string `json:"Path"`
}

func (e *EmbyAdapter) get(path string, q url.Values) (*http.Response, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("api_key", e.APIKey)
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/%s%s?%s", e.Host, e.prefix, path, q.Encode()), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Emby-Token", e.APIKey)
	return e.client.Do(req)
}

func (e *EmbyAdapter) ListMovies(targetAudioLang, nativeLang string) ([]MediaItem, error) {
	var results []MediaItem
	startIndex := 0
	limit := 300

	for {
		q := url.Values{}
		q.Set("IncludeItemTypes", "Movie")
		q.Set("Recursive", "true")
		q.Set("Fields", "MediaSources,MediaStreams,Path,OriginalTitle,ProductionYear,ProductionLocations,DateCreated")
		q.Set("StartIndex", fmt.Sprint(startIndex))
		q.Set("Limit", fmt.Sprint(limit))
		resp, err := e.get("/Items", q)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to fetch items, status: %d", resp.StatusCode)
		}

		var data embyItemsResponse
		err = json.NewDecoder(resp.Body).Decode(&data)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		if len(data.Items) == 0 {
			break
		}

		for _, item := range data.Items {
			mi := MediaItem{
				ID:            item.ID,
				Name:          item.Name,
				OriginalTitle: item.OriginalTitle,
				Year:          item.ProductionYear,
				Path:          item.Path,
				ServerType:    e.prefix,
				Countries:     item.ProductionLocations,
				DateCreated:   item.DateCreated,
				RuntimeSec:    int(item.RunTimeTicks / 10_000_000),
				Container:     item.Container,
				HasPoster:     item.ImageTags["Primary"] != nil,
			}
			rawStreams := item.MediaStreams
			if len(item.MediaSources) > 0 {
				src := item.MediaSources[0]
				mi.MediaSourceID = src.ID
				if src.Container != "" && src.Container != "strm" {
					mi.Container = src.Container
				}
				if strings.EqualFold(src.Protocol, "Http") || strings.HasPrefix(src.Path, "http") {
					mi.MediaURL = src.Path
				}
				if len(rawStreams) == 0 {
					rawStreams = src.MediaStreams
				}
			}

			hasAudioMatch := false
			for _, s := range rawStreams {
				mi.Streams = append(mi.Streams, MediaStream{
					Index:        s.Index,
					Type:         s.Type,
					Language:     s.Language,
					DisplayTitle: s.DisplayTitle,
					Codec:        s.Codec,
					IsExternal:   s.IsExternal,
					Path:         s.Path,
				})
				if s.Type == "Audio" && lang.Matches(s.Language, targetAudioLang) && !hasAudioMatch {
					hasAudioMatch = true
					mi.TargetAudio = s.Language
				}
				if s.Type == "Subtitle" && (lang.IsBilingualName(s.DisplayTitle, nativeLang, targetAudioLang) ||
					lang.IsBilingualName(filepathBase(s.Path), nativeLang, targetAudioLang)) {
					mi.HasBilingual = true
				}
			}

			if targetAudioLang != "" {
				switch {
				case hasAudioMatch:
					mi.MatchedBy = "audio"
				// Unprobed items (e.g. .strm) carry no streams: fall back to production country.
				case lang.MatchesCountry(targetAudioLang, item.ProductionLocations):
					mi.MatchedBy = "country"
					mi.TargetAudio = lang.Normalize(targetAudioLang)
				default:
					continue
				}
			}

			results = append(results, mi)
		}

		startIndex += limit
		if startIndex >= data.TotalRecordCount {
			break
		}
	}

	return results, nil
}

func filepathBase(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (e *EmbyAdapter) FetchSubtitle(item MediaItem, stream MediaStream) ([]byte, error) {
	msid := item.MediaSourceID
	if msid == "" {
		msid = item.ID
	}
	resp, err := e.get(fmt.Sprintf("/Videos/%s/%s/Subtitles/%d/Stream.srt", url.PathEscape(item.ID), url.PathEscape(msid), stream.Index), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subtitle download returned status %d", resp.StatusCode)
	}
	return readAllLimited(resp.Body)
}

func (e *EmbyAdapter) FetchPoster(itemID string, maxHeight int) (*http.Response, error) {
	q := url.Values{}
	q.Set("maxHeight", fmt.Sprint(maxHeight))
	q.Set("quality", "85")
	return e.get(fmt.Sprintf("/Items/%s/Images/Primary", url.PathEscape(itemID)), q)
}

func (e *EmbyAdapter) RefreshItem(itemID string) error {
	reqURL := fmt.Sprintf("%s/%s/Items/%s/Refresh?Recursive=true&api_key=%s",
		e.Host, e.prefix, itemID, url.QueryEscape(e.APIKey))

	req, err := http.NewRequest("POST", reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Emby-Token", e.APIKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("refresh returned status %d", resp.StatusCode)
	}
	return nil
}
