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
	// Name is an optional client identifier. Only used in logs.
	Name string
	// ID is a required client identifier. All connections are pooled using the ID,
	// so make this unique if you don't want this client pooled with another.
	ID string
	// ClientIDs is simply saved with the connection data and available in stats.
	// It may be empty and is not directly used by this library.
	// It's for you to identify your clients with your own ID(s).
	ClientIDs []interface{}
	// Websocket URLs this client shall connect to.
	Targets []string
	// Minimum count of idle connections to maintain at all times.
	PoolIdleSize int
	// Maximum websocket connections to keep per target.
	PoolMaxSize int
	// SecretKey is passed as a header to the server to "authenticate".
	// The target servers must accept this value.
	SecretKey string
	// How often to reap dead connections from the target pools.
	// This also controls how often to re-try connections to the targets.
	CleanInterval time.Duration
	// How many seconds to backoff on every connection attempt.
	Backoff time.Duration
	// Maximum backoff length.
	MaxBackoff time.Duration
	// What to reset the backoff to when max is hit.
	// Set this to max to stay at max.
	BackoffReset time.Duration
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
		dialer: &websocket.Dialer{
			EnableCompression: true,
			HandshakeTimeout:  mulch.HandshakeTimeout,
		},
		pools: make(map[string]*Pool),
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

// GetID returns the client ID hash.
func (c *Client) GetID() string {
	return mulch.HashKeyID(c.SecretKey, c.ID)
}
