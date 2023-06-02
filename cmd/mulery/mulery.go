package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"golift.io/cnfgfile"
	"golift.io/mulery"
	"golift.io/mulery/server"
)

type Config struct {
	AuthURL    string `json:"authUrl" toml:"auth_url" yaml:"authUrl" xml:"auth_url"`
	AuthHeader string `json:"authHeader" toml:"auth_header" yaml:"authHeader" xml:"auth_header"`
	*server.Config
	server *server.Server
	client *http.Client `json:"-" toml:"-" yaml:"-" xml:"-"`
	sigCh  chan os.Signal
}

var ErrInvalidKey = fmt.Errorf("provided key is not authorized")

const keyLen = 36

func main() {
	configFile := flag.String("config", "mulery.conf", "config file path")
	flag.Parse()

	// Load configuration file.
	config, err := LoadConfigFile(*configFile)
	if err != nil {
		log.Fatalf("Unable to load configuration: %s", err)
	}

	config.server = server.NewServer(config.Config)
	config.server.Start()

	signal.Notify(config.sigCh, os.Interrupt, syscall.SIGTERM)
	// Wait here for a signal to shut down.
	<-config.sigCh
	config.server.Shutdown()
}

// LoadConfigFile does what its name implies.
func LoadConfigFile(path string) (*Config, error) {
	config := &Config{
		Config: server.NewConfig(),
		client: &http.Client{},
		sigCh:  make(chan os.Signal, 1),
	}
	config.Config.KeyValidator = config.KeyValidator

	if err := cnfgfile.Unmarshal(config, path); err != nil {
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}

	return config, nil
}

func (c *Config) KeyValidator(ctx context.Context, header http.Header) (string, error) {
	key := header.Get(mulery.SecretKeyHeader)
	if key == "" || len(key) != keyLen {
		return "", fmt.Errorf("%w: keyLen: %d!=%d", ErrInvalidKey, len(key), keyLen)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.AuthURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating auth proxy request: %w", err)
	}

	req.Header.Add(c.AuthHeader, key)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connecting to auth proxy: %w", err)
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body) // avoid memory leak

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: status: %s", ErrInvalidKey, resp.Status)
	}

	return key, nil
}
