package client

import (
	"fmt"
	"os"

	uuid "github.com/nu7hatch/gouuid"
	"gopkg.in/yaml.v2"
)

// Config configures an Proxy.
type Config struct {
	ID           string
	Targets      []string
	PoolIdleSize int
	PoolMaxSize  int
	SecretKey    string
}

// NewConfig creates a new ProxyConfig.
func NewConfig() (*Config, error) {
	config := new(Config)

	id, err := uuid.NewV4()
	if err != nil {
		return nil, fmt.Errorf("unable to get unique id: %w", err)
	}

	config.ID = id.String()
	config.Targets = []string{"ws://127.0.0.1:8080/register"}
	config.PoolIdleSize = 10
	config.PoolMaxSize = 100

	return config, nil
}

// LoadConfiguration loads configuration from a YAML file.
func LoadConfiguration(path string) (*Config, error) {
	config, err := NewConfig()
	if err != nil {
		return nil, err
	}

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
