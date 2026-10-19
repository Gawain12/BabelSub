package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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
			RatingKey string `json:"ratingKey"`
			Title     string `json:"title"`
			Year      int    `json:"year"`
			Media     []struct {
				Part []struct {
					File   string `json:"file"`
					Stream []struct {
						ID           int    `json:"id"`
						StreamType   int    `json:"streamType"`
						LanguageCode string `json:"languageCode"`
						DisplayTitle string `json:"displayTitle"`
						Codec        string `json:"codec"`
					} `json:"Stream"`
				} `json:"Part"`
			} `json:"Media"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

func (p *PlexAdapter) ListMovies(targetAudioLang string) ([]MediaItem, error) {
	reqURL := fmt.Sprintf("%s/library/sections", p.Host)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plex-Token", p.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
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
	langMatch := strings.ToLower(targetAudioLang)

	for _, sec := range secData.MediaContainer.Directory {
		if sec.Type != "movie" {
			continue
		}

		allURL := fmt.Sprintf("%s/library/sections/%s/all", p.Host, sec.Key)
		reqMeta, err := http.NewRequest("GET", allURL, nil)
		if err != nil {
			continue
		}
		reqMeta.Header.Set("X-Plex-Token", p.Token)
		reqMeta.Header.Set("Accept", "application/json")

		respMeta, err := p.client.Do(reqMeta)
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
			var streams []MediaStream
			filePath := ""
			hasAudioMatch := false
			hasBilingual := false

			for _, m := range item.Media {
				for _, part := range m.Part {
					if filePath == "" {
						filePath = part.File
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

						streams = append(streams, MediaStream{
							Index:        s.ID,
							Type:         sType,
							Language:     s.LanguageCode,
							DisplayTitle: s.DisplayTitle,
							Codec:        s.Codec,
						})

						if sType == "Audio" && langMatch != "" && strings.EqualFold(s.LanguageCode, langMatch) {
							hasAudioMatch = true
						}
						if sType == "Subtitle" && (strings.Contains(s.DisplayTitle, "zh-ja") || strings.Contains(s.DisplayTitle, "双语")) {
							hasBilingual = true
						}
					}
				}
			}

			if langMatch != "" && !hasAudioMatch {
				continue
			}

			results = append(results, MediaItem{
				ID:           item.RatingKey,
				Name:         item.Title,
				Year:         item.Year,
				Path:         filePath,
				ServerType:   "plex",
				Streams:      streams,
				HasBilingual: hasBilingual,
			})
		}
	}

	return results, nil
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
