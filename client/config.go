package client

import (
	"fmt"
	"os"

	uuid "github.com/nu7hatch/gouuid"
	"gopkg.in/yaml.v2"
)

// Config is the required data to initialize a client proxy connection.
type Config struct {
	ID           string
	Targets      []string
	PoolIdleSize int
	PoolMaxSize  int
	SecretKey    string
}

// NewConfig creates a new ProxyConfig.
func NewConfig() *Config {
	return &Config{
		Targets:      []string{"ws://127.0.0.1:8080/register"},
		PoolIdleSize: 10,
		PoolMaxSize:  100,
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

	if config.ID == "" {
		id, err := uuid.NewV4()
		if err != nil {
			return nil, fmt.Errorf("unable to get unique id: %w", err)
		}

		config.ID = id.String()
	}

	return config, nil
}
