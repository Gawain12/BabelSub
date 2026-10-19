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

type PlexAdapter struct {
	Host   string
	Token  string
	client *http.Client
}

func NewPlexAdapter(host, token string) *PlexAdapter {
	return &PlexAdapter{
		Host:   strings.TrimRight(host, "/"),
		Token:  token,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *PlexAdapter) Ping() error {
	reqURL := fmt.Sprintf("%s/identity", p.Host)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Plex-Token", p.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plex ping failed with status %d", resp.StatusCode)
	}
	return nil
}

type plexSectionsResp struct {
	MediaContainer struct {
		Directory []struct {
			Key  string `json:"key"`
			Type string `json:"type"`
		} `json:"Directory"`
	} `json:"MediaContainer"`
}

type plexMetadataResp struct {
	MediaContainer struct {
		Metadata []struct {
			RatingKey     string `json:"ratingKey"`
			Title         string `json:"title"`
			OriginalTitle string `json:"originalTitle"`
			Year          int    `json:"year"`
			AddedAt       int64  `json:"addedAt"`
			Duration      int64  `json:"duration"`
			Thumb         string `json:"thumb"`
			Country       []struct {
				Tag string `json:"tag"`
			} `json:"Country"`
			Media []struct {
				Container string `json:"container"`
				Part      []struct {
					File   string `json:"file"`
					Stream []struct {
						ID           int    `json:"id"`
						StreamType   int    `json:"streamType"`
						LanguageCode string `json:"languageCode"`
						DisplayTitle string `json:"displayTitle"`
						Codec        string `json:"codec"`
						Key          string `json:"key"`
					} `json:"Stream"`
				} `json:"Part"`
			} `json:"Media"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

func (p *PlexAdapter) get(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", p.Host+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plex-Token", p.Token)
	req.Header.Set("Accept", "application/json")
	return p.client.Do(req)
}

func (p *PlexAdapter) ListMovies(targetAudioLang, nativeLang string) ([]MediaItem, error) {
	resp, err := p.get("/library/sections")
	if err != nil {
		return nil, err
	}

	var secData plexSectionsResp
	err = json.NewDecoder(resp.Body).Decode(&secData)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}

	var results []MediaItem

	for _, sec := range secData.MediaContainer.Directory {
		if sec.Type != "movie" {
			continue
		}

		respMeta, err := p.get(fmt.Sprintf("/library/sections/%s/all", sec.Key))
		if err != nil {
			continue
		}

		var metaData plexMetadataResp
		err = json.NewDecoder(respMeta.Body).Decode(&metaData)
		respMeta.Body.Close()
		if err != nil {
			continue
		}

		for _, item := range metaData.MediaContainer.Metadata {
			mi := MediaItem{
				ID:            item.RatingKey,
				Name:          item.Title,
				OriginalTitle: item.OriginalTitle,
				Year:          item.Year,
				ServerType:    "plex",
				RuntimeSec:    int(item.Duration / 1000),
				HasPoster:     item.Thumb != "",
			}
			if item.AddedAt > 0 {
				mi.DateCreated = time.Unix(item.AddedAt, 0).UTC().Format(time.RFC3339)
			}
			for _, c := range item.Country {
				mi.Countries = append(mi.Countries, c.Tag)
			}
			hasAudioMatch := false

			for _, m := range item.Media {
				if mi.Container == "" {
					mi.Container = m.Container
				}
				for _, part := range m.Part {
					if mi.Path == "" {
						mi.Path = part.File
					}
					for _, s := range part.Stream {
						sType := "Other"
						switch s.StreamType {
						case 1:
							sType = "Video"
						case 2:
							sType = "Audio"
						case 3:
							sType = "Subtitle"
						}

						mi.Streams = append(mi.Streams, MediaStream{
							Index:        s.ID,
							Type:         sType,
							Language:     s.LanguageCode,
							DisplayTitle: s.DisplayTitle,
							Codec:        s.Codec,
							IsExternal:   s.Key != "",
							Key:          s.Key,
						})

						if sType == "Audio" && lang.Matches(s.LanguageCode, targetAudioLang) && !hasAudioMatch {
							hasAudioMatch = true
							mi.TargetAudio = s.LanguageCode
						}
						if sType == "Subtitle" && lang.IsBilingualName(s.DisplayTitle, nativeLang, targetAudioLang) {
							mi.HasBilingual = true
						}
					}
				}
			}

			if targetAudioLang != "" {
				switch {
				case hasAudioMatch:
					mi.MatchedBy = "audio"
				case lang.MatchesCountry(targetAudioLang, mi.Countries):
					mi.MatchedBy = "country"
					mi.TargetAudio = lang.Normalize(targetAudioLang)
				default:
					continue
				}
			}

			results = append(results, mi)
		}
	}

	return results, nil
}

// FetchSubtitle only works for sidecar subtitles: Plex exposes them under the
// stream key, while embedded tracks have no download endpoint.
func (p *PlexAdapter) FetchSubtitle(item MediaItem, stream MediaStream) ([]byte, error) {
	if stream.Key == "" {
		return nil, fmt.Errorf("plex 不提供内嵌字幕轨下载，请改用本地文件或 ffprobe 探测轨道")
	}
	resp, err := p.get(stream.Key)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subtitle download returned status %d", resp.StatusCode)
	}
	return readAllLimited(resp.Body)
}

func (p *PlexAdapter) FetchPoster(itemID string, maxHeight int) (*http.Response, error) {
	return p.get(fmt.Sprintf("/photo/:/transcode?width=%d&height=%d&minSize=1&url=%s",
		maxHeight*2/3, maxHeight, url.QueryEscape("/library/metadata/"+itemID+"/thumb")))
}

func (p *PlexAdapter) RefreshItem(itemID string) error {
	reqURL := fmt.Sprintf("%s/library/metadata/%s/refresh", p.Host, itemID)
	req, err := http.NewRequest("PUT", reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Plex-Token", p.Token)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plex refresh returned status: %d", resp.StatusCode)
	}
	return nil
}
