// BlurCut config: persisted in <data-dir>/config.json.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Port         int             `json:"port"`
	DataDir      string          `json:"dataDir"`
	ScanInterval float64         `json:"scanInterval"`
	BlurStrength int             `json:"blurStrength"`
	Keywords     []string        `json:"keywords"`
	Patterns     map[string]bool `json:"patterns"`
}

const defaultPort = 8080

func defaultDataDir() string {
	if d := os.Getenv("BLURCUT_DATA"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".blurcut"
	}
	return filepath.Join(home, ".blurcut")
}

func defaultConfig(dataDir string) Config {
	port := defaultPort
	if p := os.Getenv("BLURCUT_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 1 && n <= 65535 {
			port = n
		}
	}
	return Config{
		Port:         port,
		DataDir:      dataDir,
		ScanInterval: 1.0,
		BlurStrength: 20,
		Keywords:     []string{},
		Patterns:     map[string]bool{"url": true, "email": true, "password": true, "apikey": true},
	}
}

func configPath(dataDir string) string { return filepath.Join(dataDir, "config.json") }

func loadConfig(dataDir string) (Config, error) {
	cfg := defaultConfig(dataDir)
	b, err := os.ReadFile(configPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	if cfg.DataDir == "" {
		cfg.DataDir = dataDir
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		cfg.Port = defaultPort
	}
	if cfg.ScanInterval < 0.5 || cfg.ScanInterval > 2 {
		cfg.ScanInterval = 1.0
	}
	if cfg.BlurStrength < 10 || cfg.BlurStrength > 30 {
		cfg.BlurStrength = 20
	}
	if cfg.Patterns == nil {
		cfg.Patterns = map[string]bool{"url": true, "email": true, "password": true, "apikey": true}
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(cfg.DataDir), b, 0o644)
