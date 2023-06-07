package client

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"golift.io/mulery/mulch"
)

const (
	DefaultMaxBackoff   = 30 * time.Second
	DefaultBackoffReset = 10 * time.Second
	DefaultPoolIdleSize = 10
	DefaultPoolMaxSize  = 100
)

// Config is the required data to initialize a client proxy connection.
type Config struct {
	ID            string
	Targets       []string
	PoolIdleSize  int
	PoolMaxSize   int
	SecretKey     string
	CleanInterval time.Duration
	Backoff       time.Duration
	MaxBackoff    time.Duration
	BackoffReset  time.Duration
	// Handler is an optional custom handler for all proxied requests.
	// Leaving this nil makes all requests use an empty http.Client.
	Handler func(http.ResponseWriter, *http.Request)
	// Logger allows routing logs from this package however you'd like.
	// If left nil, you will get no logs. Use DefaultLogger to print logs to stdout.
	mulch.Logger
}

// Client connects to one or more Server using HTTP websockets.
// The Server can then send HTTP requests to execute.
type Client struct {
	*Config
	client *http.Client
	dialer *websocket.Dialer
	pools  map[string]*Pool
}

// NewConfig creates a new ProxyConfig.
func NewConfig() *Config {
	return &Config{
		Targets:       []string{"ws://127.0.0.1:8080/register"},
		PoolIdleSize:  DefaultPoolIdleSize,
		PoolMaxSize:   DefaultPoolMaxSize,
		Logger:        &mulch.DefaultLogger{Silent: false},
		CleanInterval: time.Second,
		MaxBackoff:    DefaultMaxBackoff,
		BackoffReset:  DefaultBackoffReset,
	}
}

// NewClient creates a new Client.
func NewClient(config *Config) *Client {
	if config.Logger == nil {
		config.Logger = &mulch.DefaultLogger{Silent: true}
	}

	if config.CleanInterval < time.Second {
		config.CleanInterval = time.Second
	}

	if config.MaxBackoff == 0 {
		config.MaxBackoff = DefaultMaxBackoff
	}

	if config.BackoffReset == 0 {
		config.BackoffReset = DefaultBackoffReset
	}

	return &Client{
		Config: config,
		client: &http.Client{},
		dialer: &websocket.Dialer{},
		pools:  make(map[string]*Pool),
	}
}

// Start the Proxy.
func (c *Client) Start(ctx context.Context) {
	for _, target := range c.Config.Targets {
		c.pools[target] = StartPool(ctx, c, target, c.Config.SecretKey)
	}
}

// Shutdown the Proxy.
func (c *Client) Shutdown() {
	for _, pool := range c.pools {
		pool.Shutdown()
	}
}
