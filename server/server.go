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
	"time"

	"github.com/gorilla/websocket"
	"golift.io/mulery"
)

var (
	ErrInvalidKey    = fmt.Errorf("invalid secret key provided")
	ErrNoProxyTarget = fmt.Errorf("no proxy target found for request")
	ErrNoDestination = fmt.Errorf("x-proxy-destination header invalid")
	ErrInvalidData   = fmt.Errorf("invalid data received")
)

// Server is a Reverse HTTP Proxy over WebSocket.
// This is the Server part, Clients will offer websocket connections,
// those will be pooled to transfer HTTP Request and response.
type Server struct {
	Config   *Config
	upgrader websocket.Upgrader
	// In pools, keep connections with WebSocket peers.
	pools   map[clientID]*Pool
	newPool chan *newPool
	// Through dispatcher channel it communicates between "server" thread and "dispatcher" thread.
	// "server" thread sends the value to this channel when accepting requests in the endpoint /requests,
	// and "dispatcher" thread reads this channel.
	dispatcher chan *ConnectionRequest
	server     *http.Server
}

type newPool struct {
	sock     *websocket.Conn
	clientID clientID
	size     int
	max      int
	secret   string
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
		newPool:    make(chan *newPool, 100),
		dispatcher: make(chan *ConnectionRequest),
		pools:      make(map[clientID]*Pool),
	}
}

// Start Server HTTP server.
func (s *Server) Start() {
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
	go s.dispatchConnections() //gofunc:1

	s.server = &http.Server{
		Addr:        fmt.Sprintf("%s:%d", s.Config.Host, s.Config.Port),
		Handler:     smx,
		ReadTimeout: s.Config.Timeout,
	}

	go func() { //gofunc:2
		err := s.server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalln("Web server failed, exiting:", err)
		}
	}()
}

// clean removes empty Pools; those with no incoming client connections.
// It is invoked every 5 sesconds and at shutdown.
func (s *Server) clean() {
	if len(s.pools) == 0 {
		return
	}

	idle := 0
	busy := 0
	closed := 0
	conns := 0
	pools := map[clientID]*Pool{}

	for target, pool := range s.pools {
		if pool.IsEmpty() {
			log.Printf("Removing empty connection pool: %s", pool.id)
			pool.Shutdown()
			delete(s.pools, target)
			closed++
		} else {
			pools[target] = pool
			ps := pool.Size()
			conns += ps.Total
			idle += ps.Idle
			busy += ps.Busy
		}
	}

	s.pools = pools
	log.Printf("%d pools, %d connections, %d idle, %d busy, %d closed, %d newPool",
		len(s.pools), conns, idle, busy, closed, len(s.newPool))
}

// Dispatch connection from available pools to client requests.
func (s *Server) dispatchConnections() {
	defer s.shutdown()

	const waitTime = 5 * time.Second

	ticker := time.NewTicker(waitTime)
	defer ticker.Stop()

	for {
		// Runs in an infinite loop:
		// - Receives the value from the `server.dispatcher` channel.
		// - Checks for done channel closing.
		// - Runs cleaner every 5 seconds.
		select {
		case newPool, ok := <-s.newPool:
			if !ok {
				return
			}

			s.registerPool(newPool)
		case <-ticker.C:
			s.clean()
		case request, ok := <-s.dispatcher:
			if !ok {
				return
			}

			s.dispatchRequest(request)
		}
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

		if len(s.pools) == 0 {
			// No connection pool available
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
		mulery.ProxyError(w, fmt.Errorf("%w: not provided", ErrNoDestination))
		return
	}

	URL, err := url.Parse(dstURL)
	if err != nil {
		mulery.ProxyError(w, fmt.Errorf("parsing X-PROXY-DESTINATION header: %w", err))
		return
	}

	r.URL = URL // used in proxyRequest().
	log.Printf("[%s] %s", r.Method, r.URL.String())

	if len(s.pools) == 0 {
		mulery.ProxyError(w, fmt.Errorf("%w: no pools registered", ErrNoProxyTarget))
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
		// Dispatcher is `nil` which means the target has no pool.
		mulery.ProxyError(w, fmt.Errorf("%w: %s", ErrNoProxyTarget, request.target))
		return
	}

	// [3]: Send the request to the peer through the WebSocket connection.
	if err := connection.proxyRequest(w, r); err != nil {
		// An error occurred throw the connection away.
		connection.Close()
		// Try to return an error to the client.
		// This might fail if response headers have already been sent.
		mulery.ProxyError(w, fmt.Errorf("tunneling failure, connection closed: %w", err))
	}
}

// handleRegister receives http requests for /register paths.
// Receives the WebSocket upgrade handshake request from wsp_client.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	// 0. Validate the provided secret key.
	secret, err := s.validateKey(r.Header)
	if err != nil {
		mulery.ProxyError(w, err)
		return
	}

	// 1. Upgrade a received HTTP request to a WebSocket connection.
	sock, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		mulery.ProxyError(w, fmt.Errorf("http upgrade failed: %w", err))
		return
	}

	// 2. Wait a greeting message from the peer and parse it.
	// The first message should contain the remote Proxy name and pool size.
	clientID, size, max, err := parseGreeting(sock)
	if err != nil {
		mulery.ProxyError(w, err)
		sock.Close()

		return
	}

	// 3. Register the connection into server pools.
	s.newPool <- &newPool{sock, clientID, size, max, secret}
}

// 0. Validate the provided secret key.
func (s *Server) validateKey(header http.Header) (string, error) {
	// If a custom key validator is provided, run that.
	if s.Config.KeyValidator != nil {
		secret, err := s.Config.KeyValidator(header)
		if err != nil {
			return "", fmt.Errorf("custom key validation failed: %v", err)
		}

		return secret, nil
	}

	// Otherwise run the default validator.
	secretKey := header.Get("X-SECRET-KEY")
	if secretKey != s.Config.SecretKey {
		return "", ErrInvalidKey
	}

	// Do not return the "configured" secret key.
	return "", nil
}

// 2. Wait a greeting message from the peer and parse it.
func parseGreeting(sock *websocket.Conn) (clientID, int, int, error) {
	_, greeting, err := sock.ReadMessage()
	if err != nil {
		return "", 0, 0, fmt.Errorf("unable to read greeting message: %w", err)
	}

	// Parse the greeting message
	split := strings.Split(string(greeting), "_")
	if len(split) != 3 {
		return "", 0, 0, fmt.Errorf("%w: greeting separator count is wrong", ErrInvalidData)
	}

	clientID := clientID(split[0])

	size, err := strconv.Atoi(split[1])
	if err != nil {
		return "", 0, 0, fmt.Errorf("unable to parse greeting message: %w", err)
	}

	max, err := strconv.Atoi(split[2])
	if err != nil {
		return "", 0, 0, fmt.Errorf("unable to parse greeting message: %w", err)
	}

	return clientID, size, max, nil
}

// 3. Register the connection into server pools.
func (s *Server) registerPool(newPool *newPool) {
	if pool, ok := s.pools[newPool.clientID]; !ok || pool == nil {
		s.pools[newPool.clientID] = NewPool(s, newPool.clientID, newPool.max, newPool.secret)
	}

	// update pool size
	s.pools[newPool.clientID].length = newPool.size

	// Add the WebSocket connection to the pool
	s.pools[newPool.clientID].Register(newPool.sock)
}

func (s *Server) handleStatus(resp http.ResponseWriter, _ *http.Request) {
	http.Error(resp, "ok", http.StatusOK)
}

// Shutdown stop the Server.
func (s *Server) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), s.server.ReadTimeout)
	defer cancel()

	_ = s.server.Shutdown(ctx)
	close(s.newPool)
}

func (s *Server) shutdown() {
	close(s.dispatcher)

	for target, pool := range s.pools {
		pool.Shutdown()
		delete(s.pools, target)
	}

	s.clean()
}
