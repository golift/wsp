package server

import (
	"fmt"
	"os"
	"strconv"
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
}

// GetAddr returns the address to specify a HTTP server address.
func (c Config) GetAddr() string {
	return c.Host + ":" + strconv.Itoa(c.Port)
}

// NewConfig creates a new ProxyConfig.
func NewConfig() *Config {
	config := new(Config)
	config.Host = "127.0.0.1"
	config.Port = 8080
	config.Timeout = 1000 // millisecond
	config.IdleTimeout = 60000

	return config
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
