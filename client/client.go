package client

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"
)

// Client connects to one or more Server using HTTP websockets.
// The Server can then send HTTP requests to execute.
type Client struct {
	Config *Config
	client *http.Client
	dialer *websocket.Dialer
	pools  map[string]*Pool
}

// NewClient creates a new Client.
func NewClient(config *Config) *Client {
	client := new(Client)
	client.Config = config
	client.client = &http.Client{}
	client.dialer = &websocket.Dialer{}
	client.pools = make(map[string]*Pool)

	return client
}

// Start the Proxy.
func (c *Client) Start(ctx context.Context) {
	for _, target := range c.Config.Targets {
		c.pools[target] = NewPool(c, target, c.Config.SecretKey)
		go c.pools[target].Start(ctx)
	}
}

// Shutdown the Proxy.
func (c *Client) Shutdown() {
	for _, pool := range c.pools {
		pool.Shutdown()
	}
}
