package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	ID             string            `json:"Id"`
	Name           string            `json:"Name"`
	OriginalTitle  string            `json:"OriginalTitle"`
	ProductionYear int               `json:"ProductionYear"`
	Path           string            `json:"Path"`
	MediaStreams   []embyRawStream   `json:"MediaStreams"`
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

func (e *EmbyAdapter) ListMovies(targetAudioLang string) ([]MediaItem, error) {
	var results []MediaItem
	startIndex := 0
	limit := 300

	langMatch := strings.ToLower(targetAudioLang)
	var langAliases []string
	switch langMatch {
	case "ja", "jpn", "japanese":
		langAliases = []string{"ja", "jpn", "japanese"}
	case "zh", "chi", "chinese":
		langAliases = []string{"zh", "chi", "chinese", "zho"}
	case "en", "eng", "english":
		langAliases = []string{"en", "eng", "english"}
	default:
		if langMatch != "" {
			langAliases = []string{langMatch}
		}
	}

	for {
		reqURL := fmt.Sprintf("%s/%s/Items?IncludeItemTypes=Movie&Recursive=true&Fields=MediaSources,MediaStreams,Path,OriginalTitle,ProductionYear&StartIndex=%d&Limit=%d&api_key=%s",
			e.Host, e.prefix, startIndex, limit, url.QueryEscape(e.APIKey))

		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Emby-Token", e.APIKey)

		resp, err := e.client.Do(req)
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
			var streams []MediaStream
			hasAudioMatch := false
			hasBilingual := false
			matchedAudioName := ""

			for _, s := range item.MediaStreams {
				ms := MediaStream{
					Index:        s.Index,
					Type:         s.Type,
					Language:     s.Language,
					DisplayTitle: s.DisplayTitle,
					Codec:        s.Codec,
					IsExternal:   s.IsExternal,
					Path:         s.Path,
				}
				streams = append(streams, ms)

				if s.Type == "Audio" && len(langAliases) > 0 {
					for _, alias := range langAliases {
						if strings.EqualFold(s.Language, alias) {
							hasAudioMatch = true
							matchedAudioName = s.Language
							break
						}
					}
				}

				if s.Type == "Subtitle" {
					titleLower := strings.ToLower(s.DisplayTitle)
					pathLower := strings.ToLower(s.Path)
					if strings.Contains(titleLower, "zh-ja") || strings.Contains(titleLower, "双语") ||
						strings.Contains(pathLower, "zh-ja") || strings.Contains(pathLower, "双语") {
						hasBilingual = true
					}
				}
			}

			if len(langAliases) > 0 && !hasAudioMatch {
				continue
			}

			results = append(results, MediaItem{
				ID:            item.ID,
				Name:          item.Name,
				OriginalTitle: item.OriginalTitle,
				Year:          item.ProductionYear,
				Path:          item.Path,
				ServerType:    e.prefix,
				Streams:       streams,
				HasBilingual:  hasBilingual,
				TargetAudio:   matchedAudioName,
			})
		}

		startIndex += limit
		if startIndex >= data.TotalRecordCount {
			break
		}
	}

	return results, nil
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
