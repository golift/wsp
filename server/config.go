package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"golift.io/mulery/mulch"
)

// Config configures a Server.
type Config struct {
	Dispatchers uint          `json:"dispatchers" toml:"dispatchers" yaml:"dispatchers" xml:"dispatchers"`
	Timeout     time.Duration `json:"timeout" toml:"timeout" yaml:"timeout" xml:"timeout"`
	IdleTimeout time.Duration `json:"idleTimeout" toml:"idle_timeout" yaml:"idleTimeout" xml:"idle_timeout"`
	SecretKey   string        `json:"secretKey" toml:"secret_key" yaml:"secretKey" xml:"secret_key"`
	// IDHeader sets the upstream header to parse for a remote client.
	// Default behavior is to send requests to clients randomly.
	// If this value is set, requests can only be directed to clients by providing the client ID in this header.
	IDHeader string `json:"idHeader" toml:"id_header" yaml:"idHeader" xml:"id_header"`
	// If a KeyValidator method is provided, then Secretkey is ignored.
	// If the validator returns a string then all pool IDs become a
	// sha256 of that string and the client's generated or provided id.
	// See the NewPool function to see that in action.
	// This allows you to let clients provide their own ID, but a secure
	// access-ID is created with your provided seed to prevent hash collisions.
	KeyValidator func(context.Context, http.Header) (string, error) `json:"-" toml:"-" yaml:"-" xml:"-"`
	// Logger allows routing logs from this package to somewhere special.
	// If left nil logs are written to stdout.
	Logger mulch.Logger `json:"-" toml:"-" yaml:"-" xml:"-"`
}

// Server is a Reverse HTTP Proxy over WebSocket.
// This is the Server part, Clients offer websocket connections,
// and those are pooled to transfer HTTP Requests and responses.
type Server struct {
	Config   *Config
	upgrader websocket.Upgrader
	// In pools, keep connections with WebSocket peers.
	pools   map[clientID]*Pool
	newPool chan *PoolConfig
	// Through dispatcher channel it communicates between "http server" thread and "dispatcher" thread.
	// "server" thread sends the value to this channel when accepting requests in the endpoint /requests,
	// and "dispatcher" thread reads this channel.
	dispatcher chan *dispatchRequest
	metrics    *Metrics
	closed     int
	getPool    chan clientID
	repPool    chan *Pool
}

// PoolConfig is a struct for transitting a new pool's data through a channel.
type PoolConfig struct {
	MinConns int
	MaxConns int
	ID       clientID
	secret   string
	Sock     *websocket.Conn
}

// dispatchRequest is used to request a proxy connection from the dispatcher.
// By sending it through a channel.
type dispatchRequest struct {
	connection chan *Connection
	client     clientID
}

// NewConfig creates a new ProxyConfig.
func NewConfig() *Config {
	return &Config{
		Dispatchers: 1,
		Timeout:     time.Second,
		IdleTimeout: time.Minute + time.Second,
		Logger:      &mulch.DefaultLogger{},
	}
}

// NewServer return a new Server instance.
func NewServer(config *Config) *Server {
	const defaultPoolBuffer = 100

	if config.Logger == nil {
		config.Logger = &mulch.DefaultLogger{}
	}

	if config.Dispatchers == 0 {
		config.Dispatchers = 1
	}

	return &Server{
		Config:     config,
		upgrader:   websocket.Upgrader{},
		newPool:    make(chan *PoolConfig, defaultPoolBuffer),
		dispatcher: make(chan *dispatchRequest),
		pools:      make(map[clientID]*Pool),
		metrics:    getMetrics(),
		getPool:    make(chan clientID),
		repPool:    make(chan *Pool),
	}
}
