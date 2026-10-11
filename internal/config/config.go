package config

import (
	"encoding/json"
	"log"
	"os"
)

type Voip struct {
	AutoRejectIncoming bool `json:"auto_reject_incoming"`
	SampleRate         int  `json:"sample_rate"`
	Channels           int  `json:"channels"`
	BitrateKbps        int  `json:"bitrate_kbps"`
}

type Config struct {
	Owner         string `json:"owner"`
	Prefix        string `json:"prefix"`
	EasterEggFile string `json:"easter_egg_file"`
	WebPort       int    `json:"web_port"`
	Voip          Voip   `json:"voip"`
}

func Default() *Config {
	return &Config{
		Prefix:  "!",
		WebPort: 3000,
		Voip: Voip{
			AutoRejectIncoming: true,
			SampleRate:         48000,
			Channels:           1,
			BitrateKbps:        32,
		},
	}
}

func Load(path string) *Config {
	cfg := Default()
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[config] не удалось прочитать %s: %v", path, err)
		return cfg
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		log.Printf("[config] битый JSON в %s: %v", path, err)
		return Default()
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "!"
	}
	if cfg.WebPort == 0 {
		cfg.WebPort = 3000
	}
	if cfg.Voip.SampleRate == 0 {
		cfg.Voip.SampleRate = 48000
	}
	if cfg.Voip.Channels == 0 {
		cfg.Voip.Channels = 1
	}
	if cfg.Voip.BitrateKbps == 0 {
		cfg.Voip.BitrateKbps = 32
	}
	return cfg
}
