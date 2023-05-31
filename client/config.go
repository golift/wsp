package client

import (
	"fmt"
	"net/http"

	uuid "github.com/nu7hatch/gouuid"
	"golift.io/cnfgfile"
)

// Config is the required data to initialize a client proxy connection.
type Config struct {
	ID           string
	Targets      []string
	PoolIdleSize int
	PoolMaxSize  int
	SecretKey    string
	// Handler is an optional custom handler for all proxied requests.
	// Leaving this nil makes all requests use an empty http.Client.
	Handler func(http.ResponseWriter, *http.Request)
}

// NewConfig creates a new ProxyConfig.
func NewConfig() *Config {
	return &Config{
		Targets:      []string{"ws://127.0.0.1:8080/register"},
		PoolIdleSize: 10,
		PoolMaxSize:  100,
	}
}

func LoadConfiguration(path string) (*Config, error) {
	config := NewConfig()

	err := cnfgfile.Unmarshal(config, path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}

	if config.ID == "" {
		id, err := uuid.NewV4()
		if err != nil {
			return nil, fmt.Errorf("unable to get unique id: %w", err)
		}

		config.ID = id.String()
	}

	return config, nil
}
