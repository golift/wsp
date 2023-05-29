package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/root-gg/wsp"
)

var (
	ErrInvalidKey    = fmt.Errorf("invalid secret key provided")
	ErrNoProxyTarget = fmt.Errorf("no proxy target found for request")
	ErrNoDestination = fmt.Errorf("x-proxy-destination header invalid")
)

// Server is a Reverse HTTP Proxy over WebSocket.
// This is the Server part, Clients will offer websocket connections,
// those will be pooled to transfer HTTP Request and response.
type Server struct {
	Config   *Config
	upgrader websocket.Upgrader
	// In pools, keep connections with WebSocket peers.
	pools map[clientID]*Pool
	// A RWMutex is a reader/writer mutual exclusion lock,
	// and it is for exclusive control with pools operation.
	//
	// This is locked when reading and writing pools, the timing is when:
	// 1. (rw) registering websocket clients in /register endpoint
	// 2. (rw) remove empty pools which has no connections
	// 3. (r) dispatching connection from available pools to clients requests
	//
	// And then it is released after each process is completed.
	lock sync.RWMutex
	done chan struct{}
	// Through dispatcher channel it communicates between "server" thread and "dispatcher" thread.
	// "server" thread sends the value to this channel when accepting requests in the endpoint /requests,
	// and "dispatcher" thread reads this channel.
	dispatcher chan *ConnectionRequest
	server     *http.Server
}

// ConnectionRequest is used to request a proxy connection from the dispatcher.
type ConnectionRequest struct {
	connection chan *Connection
	target     clientID
}

// NewServer return a new Server instance.
func NewServer(config *Config) *Server {
	rand.Seed(time.Now().Unix()) // hmm

	return &Server{
		Config:     config,
		upgrader:   websocket.Upgrader{},
		done:       make(chan struct{}),
		dispatcher: make(chan *ConnectionRequest),
		pools:      make(map[clientID]*Pool),
	}
}

// Start Server HTTP server.
func (s *Server) Start() {
	go func() {
		const waitTime = 5 * time.Second
		ticker := time.NewTicker(waitTime)
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				s.clean()
			}
		}
	}()

	smx := http.NewServeMux()
	// XXX: I want to detach the handler function from the Server struct,
	// but it is tightly coupled to the internal state of the Server.
	// Lessons learned:
	// - The handlers need to live in the main app because they interface with everything in the app.
	// - As you attempt to decouple the handlers, you wind up moving most of the code. Trust me.
	// - Handlers that do things in other packages can be in other packages.
	smx.HandleFunc("/register", s.handleRegister)
	smx.HandleFunc("/request", s.handleRequest)
	smx.HandleFunc("/status", s.handleStatus)

	// Dispatch connection from available pools to client requests
	// in a separate thread from the server thread.
	go s.dispatchConnections()

	s.server = &http.Server{
		Addr:        fmt.Sprintf("%s:%d", s.Config.Host, s.Config.Port),
		Handler:     smx,
		ReadTimeout: s.Config.Timeout,
	}

	go func() {
		err := s.server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalln("Web server failed, exiting:", err)
		}
	}()
}

// clean removes empty Pools; those with no incoming client connections.
// It is invoked every 5 sesconds and at shutdown.
func (s *Server) clean() {
	s.lock.Lock()
	defer s.lock.Unlock()

	if len(s.pools) == 0 {
		return
	}

	idle := 0
	busy := 0
	pools := map[clientID]*Pool{}

	for target, pool := range s.pools {
		if pool.IsEmpty() {
			log.Printf("Removing empty connection pool: %s", pool.id)
			pool.Shutdown()
			delete(s.pools, target)
		} else {
			pools[target] = pool
		}

		ps := pool.size()
		idle += ps.Idle
		busy += ps.Busy
	}

	s.pools = pools
	log.Printf("%d pools, %d idle, %d busy", len(s.pools), idle, busy)
}

// Dispatch connection from available pools to client requests.
func (s *Server) dispatchConnections() {
	for request := range s.dispatcher {
		// Runs in an infinite loop and keeps receiving the value from the `server.dispatcher` channel.
		s.dispatchRequest(request)
	}
}

// dispatchRequest runs every time an http request comes into the server.
// This finds a pool for the request, and sends the request to it.
func (s *Server) dispatchRequest(request *ConnectionRequest) {
	defer close(request.connection)

	// A timeout is set for each dispatch request.
	ctx, cancel := context.WithTimeout(context.Background(), s.Config.Timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done(): // The timeout elapses
			return
		default: // Go through
		}

		s.lock.RLock()

		if len(s.pools) == 0 {
			// No connection pool available
			s.lock.RUnlock()
			return
		}

		// [1]: Select a pool which has an idle connection, or one that matches the requested target.
		connection, ok := s.getRequestConnection(request)
		if !ok {
			continue // a pool has been removed, try again.
		} else if connection == nil {
			return // the requested target has no pool
		}

		// [2]: Verify that we can use this connection and take it.
		if connection.Take() {
			request.connection <- connection
			return
		}
	}
}

func (s *Server) getRequestConnection(request *ConnectionRequest) (*Connection, bool) {
	if request.target != "" {
		if s.pools[request.target] != nil {
			return <-s.pools[request.target].idle, true
		}

		return nil, true
	}

	// Build a select statement dynamically to handle an arbitrary number of pools.
	cases := make([]reflect.SelectCase, len(s.pools)+1)
	idx := 0

	for _, ch := range s.pools {
		cases[idx] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch.idle)}
		idx++
	}

	cases[len(cases)-1] = reflect.SelectCase{Dir: reflect.SelectDefault}
	s.lock.RUnlock()

	_, value, ok := reflect.Select(cases)
	if !ok {
		return nil, false // a pool has been removed, try again.
	}

	connection, _ := value.Interface().(*Connection)

	return connection, true
}

// handleRequest receives http requests for /request paths.
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// [1]: Receive requests to be proxied; parse destination URL.
	dstURL := r.Header.Get("X-PROXY-DESTINATION")
	if dstURL == "" {
		wsp.ProxyError(w, fmt.Errorf("%w: not provided", ErrNoDestination))
		return
	}

	URL, err := url.Parse(dstURL)
	if err != nil {
		wsp.ProxyError(w, fmt.Errorf("parsing X-PROXY-DESTINATION header: %w", err))
		return
	}

	r.URL = URL // used in proxyRequest().
	log.Printf("[%s] %s", r.Method, r.URL.String())

	if len(s.pools) == 0 {
		wsp.ProxyError(w, fmt.Errorf("%w: no pools registered", ErrNoProxyTarget))
		return
	}

	// [2]: Take an WebSocket connection available from pools for relaying received requests.
	request := &ConnectionRequest{
		connection: make(chan *Connection),
		target:     clientID(r.Header.Get("X-PROXY-TARGET")),
	}
	// "Dispatcher" is running in a separate thread from the server by `go s.dispatchConnections()`.
	// It waits to receive requests to dispatch connection from available pools to clients requests.
	// https://github.com/hgsgtk/wsp/blob/ea4902a8e11f820268e52a6245092728efeffd7f/server/server.go#L93
	//
	// Notify request from handler to dispatcher through Server.dispatcher channel.
	s.dispatcher <- request
	// Dispatcher tries to find an available connection pool,
	// and it returns the connection through Server.connection channel.
	// https://github.com/hgsgtk/wsp/blob/ea4902a8e11f820268e52a6245092728efeffd7f/server/server.go#L189
	//
	// Here waiting for a result from dispatcher.
	connection := <-request.connection
	if connection == nil {
		// Dispatcher has set `nil` which means the target has no pool.
		wsp.ProxyError(w, fmt.Errorf("%w: %s", ErrNoProxyTarget, request.target))
		return
	}

	// [3]: Send the request to the peer through the WebSocket connection.
	if err := connection.proxyRequest(w, r); err != nil {
		// An error occurred throw the connection away.
		connection.Close()
		// Try to return an error to the client.
		// This might fail if response headers have already been sent.
		wsp.ProxyError(w, err)
	}
}

func (s *Server) validateKey(header http.Header) error {
	// If a custom key validator is provided, run that.
	if s.Config.KeyValidator != nil {
		if err := s.Config.KeyValidator(header); err != nil {
			return fmt.Errorf("custom key validation failed: %v", err)
		}

		return nil
	}

	// Otherwise run the default validator.
	secretKey := header.Get("X-SECRET-KEY")
	if secretKey != s.Config.SecretKey {
		return ErrInvalidKey
	}

	return nil
}

// handleRegister receives http requests for /register paths.
// Receives the WebSocket upgrade handshake request from wsp_client.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	// 1. Upgrade a received HTTP request to a WebSocket connection.
	if err := s.validateKey(r.Header); err != nil {
		wsp.ProxyError(w, err)
		return
	}

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		wsp.ProxyError(w, fmt.Errorf("http upgrade failed: %w", err))
		return
	}

	// 2. Wait a greeting message from the peer and parse it.
	// The first message should contain the remote Proxy name and pool size.
	clientID, size, err := parseGreeting(ws)
	if err != nil {
		wsp.ProxyError(w, err)
		ws.Close()

		return
	}

	// 3. Register the connection into server pools.
	// s.lock is for exclusive control of pools operation.
	s.lock.Lock()
	defer s.lock.Unlock()

	if pool, ok := s.pools[clientID]; !ok || pool == nil {
		s.pools[clientID] = NewPool(s, clientID)
	}

	// update pool size
	s.pools[clientID].length = size

	// Add the WebSocket connection to the pool
	s.pools[clientID].Register(ws)
}

func parseGreeting(sock *websocket.Conn) (clientID, int, error) {
	_, greeting, err := sock.ReadMessage()
	if err != nil {
		return "", 0, fmt.Errorf("unable to read greeting message: %w", err)
	}

	// Parse the greeting message
	split := strings.Split(string(greeting), "_")
	clientID := clientID(split[0])

	size, err := strconv.Atoi(split[1])
	if err != nil {
		return "", 0, fmt.Errorf("unable to parse greeting message: %w", err)
	}

	return clientID, size, nil
}

func (s *Server) handleStatus(resp http.ResponseWriter, _ *http.Request) {
	http.Error(resp, "ok", http.StatusOK)
}

// Shutdown stop the Server.
func (s *Server) Shutdown() {
	close(s.done)
	close(s.dispatcher)

	for target, pool := range s.pools {
		pool.Shutdown()
		delete(s.pools, target)
	}

	s.clean()
}
