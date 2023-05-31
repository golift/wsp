package server

import (
	"fmt"
	"net/http"
	"time"

	"golift.io/cnfgfile"
)

// Config configures a Server.
type Config struct {
	Host        string
	Port        int
	Timeout     time.Duration
	IdleTimeout time.Duration
	SecretKey   string
	// If a KeyValidator method is provided, then Secretkey is ignored.
	// If the validator returns a string then then all pool IDs become
	// a sha256 of that string and the client's generated or provided id.
	// See the NewPool function to see that in action.
	// This allows you to let clients provide their own ID, but a secure
	// access-ID is created with your provided seed to prevent hash collisions.
	KeyValidator func(http.Header) (string, error)
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

	err := cnfgfile.Unmarshal(config, path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}

	return config, nil
}
