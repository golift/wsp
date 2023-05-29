package server

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

// Config configures a Server.
type Config struct {
	Host        string
	Port        int
	Timeout     time.Duration
	IdleTimeout time.Duration
	SecretKey   string
	// If a KeyValidator method is provided, then Secretkey is ignored.
	KeyValidator func(http.Header) error
}

// NewConfig creates a new ProxyConfig.
func NewConfig() *Config {
	return &Config{
		Host:        "127.0.0.1",
		Port:        8080,
		Timeout:     time.Second,
		IdleTimeout: time.Minute,
	}
}

// LoadConfiguration loads configuration from a YAML file.
func LoadConfiguration(path string) (*Config, error) {
	config := NewConfig()

	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration: %w", err)
	}

	err = yaml.Unmarshal(bytes, config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}

	return config, nil
}
