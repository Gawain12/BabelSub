package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Type  string `json:"type" yaml:"type"`   // emby, jellyfin, plex
	Host  string `json:"host" yaml:"host"`   // e.g. http://192.168.1.81:8096
	Token string `json:"token" yaml:"token"` // API Key or Plex Token
}

type StyleConfig struct {
	PlayResX          int    `json:"play_res_x" yaml:"play_res_x"`
	PlayResY          int    `json:"play_res_y" yaml:"play_res_y"`
	FontName          string `json:"font_name" yaml:"font_name"`
	PrimaryFontSize   int    `json:"primary_font_size" yaml:"primary_font_size"`
	SecondaryFontSize int    `json:"secondary_font_size" yaml:"secondary_font_size"`
	PrimaryColor      string `json:"primary_color" yaml:"primary_color"`
	SecondaryColor    string `json:"secondary_color" yaml:"secondary_color"`
	MarginV           int    `json:"margin_v" yaml:"margin_v"`
}

type ProviderConfig struct {
	EnableSubtitleCat bool   `json:"enable_subtitlecat" yaml:"enable_subtitlecat"`
	EnableASSRT       bool   `json:"enable_assrt" yaml:"enable_assrt"`
	EnableSubHD       bool   `json:"enable_subhd" yaml:"enable_subhd"`
	OpenSubtitlesKey  string `json:"opensubtitles_key" yaml:"opensubtitles_key"`
}

type Config struct {
	Server    ServerConfig   `json:"server" yaml:"server"`
	Styling   StyleConfig    `json:"styling" yaml:"styling"`
	Providers ProviderConfig `json:"providers" yaml:"providers"`
	Language  struct {
		TargetAudio string `json:"target_audio" yaml:"target_audio"` // Default: "ja"
		NativeSub   string `json:"native_sub" yaml:"native_sub"`     // Default: "zh"
	} `json:"language" yaml:"language"`
	FFsubsyncBin string `json:"ffsubsync_bin" yaml:"ffsubsync_bin"` // Custom binary path if needed
}

func DefaultConfig() *Config {
	cfg := &Config{}
	cfg.Server = ServerConfig{
		Type:  "emby",
		Host:  "",
		Token: "",
	}
	cfg.Styling = StyleConfig{
		PlayResX:          1920,
		PlayResY:          1080,
		FontName:          "Arial",
		PrimaryFontSize:   44,
		SecondaryFontSize: 30,
		PrimaryColor:      "&H00FFFFFF",
		SecondaryColor:    "&H00D0FFFF",
		MarginV:           35,
	}
	cfg.Providers = ProviderConfig{
		EnableSubtitleCat: true,
		EnableASSRT:       true,
		EnableSubHD:       true,
	}
	cfg.Language.TargetAudio = "ja"
	cfg.Language.NativeSub = "zh"
	cfg.FFsubsyncBin = "ffsubsync"
	return cfg
}

func ConfigFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "subsync-studio", "config.yaml")
}

func LoadConfig() (*Config, error) {
	p := ConfigFilePath()
	data, err := os.ReadFile(p)
	if err != nil {
		// If not exists, return default
		return DefaultConfig(), nil
	}
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		// Try fallback json
		if jsonErr := json.Unmarshal(data, cfg); jsonErr != nil {
			return nil, fmt.Errorf("failed to parse config: %v", err)
		}
	}
	return cfg, nil
}

func SaveConfig(cfg *Config) error {
	p := ConfigFilePath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}
