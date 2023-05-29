package server

import (
	"context"
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

// Server is a Reverse HTTP Proxy over WebSocket.
// This is the Server part, Clients will offer websocket connections,
// those will be pooled to transfer HTTP Request and response.
type Server struct {
	Config   *Config
	upgrader websocket.Upgrader
	// In pools, keep connections with WebSocket peers.
	pools []*Pool
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
}

// NewConnectionRequest creates a new connection request.
func NewConnectionRequest() *ConnectionRequest {
	return &ConnectionRequest{
		connection: make(chan *Connection),
	}
}

// NewServer return a new Server instance.
func NewServer(config *Config) *Server {
	rand.Seed(time.Now().Unix()) // hmm

	server := new(Server)
	server.Config = config
	server.upgrader = websocket.Upgrader{}
	server.done = make(chan struct{})
	server.dispatcher = make(chan *ConnectionRequest)

	return server
}

// Start Server HTTP server.
func (s *Server) Start() {
	go func() {
		const waitTime = 5 * time.Second

		for {
			select {
			case <-s.done:
				return
			case <-time.After(waitTime):
				s.clean()
			}
		}
	}()

	req := http.NewServeMux()
	// XXX: I want to detach the handler function from the Server struct,
	// but it is tightly coupled to the internal state of the Server.
	// Lessons learned:
	// - The handlers need to live in the main app because they interface with everything in the app.
	// - As you attempt to decouple the handlers, you wind up moving most of the code. Trust me.
	// - Handlers that do things in other packages can be in other packages.
	req.HandleFunc("/register", s.Register)
	req.HandleFunc("/request", s.Request)
	req.HandleFunc("/status", s.status)

	// Dispatch connection from available pools to clients requests
	// in a separate thread from the server thread.
	go s.dispatchConnections()

	s.server = &http.Server{
		Addr:        s.Config.GetAddr(),
		Handler:     req,
		ReadTimeout: s.Config.Timeout,
	}

	go func() { log.Fatal(s.server.ListenAndServe()) }()
}

// clean removes empty Pools which has no connection.
// It is invoked every 5 sesconds and at shutdown.
func (s *Server) clean() {
	s.lock.Lock()
	defer s.lock.Unlock()

	if len(s.pools) == 0 {
		return
	}

	idle := 0
	busy := 0
	pools := []*Pool{}

	for _, pool := range s.pools {
		if pool.IsEmpty() {
			log.Printf("Removing empty connection pool : %s", pool.id)
			pool.Shutdown()
		} else {
			pools = append(pools, pool)
		}

		ps := pool.Size()
		idle += ps.Idle
		busy += ps.Busy
	}

	s.pools = pools
	log.Printf("%d pools, %d idle, %d busy", len(s.pools), idle, busy)
}

// Dispatch connection from available pools to clients requests.
func (s *Server) dispatchConnections() {
	for {
		// Runs in an infinite loop and keeps receiving the value from the `server.dispatcher` channel
		// The operator <- is "receive operator", which expression blocks until a value is available.
		request, ok := <-s.dispatcher
		if !ok {
			// The value of `ok` is false if it is a zero value generated because the channel is closed an empty.
			// In this case, that means server shutdowns.
			break
		}

		// A timeout is set for each dispatch request.
		ctx, cancel := context.WithTimeout(context.Background(), s.Config.Timeout)
		defer cancel()

		for {
			select {
			case <-ctx.Done(): // The timeout elapses
				return
			default: // Go through
			}

			/* How often does this run? hmm, find out. */

			s.lock.RLock()
			if len(s.pools) == 0 {
				// No connection pool available
				s.lock.RUnlock()
				break
			}

			// [1]: Select a pool which has an idle connection
			// Build a select statement dynamically to handle an arbitrary number of pools.
			cases := make([]reflect.SelectCase, len(s.pools)+1)

			for i, ch := range s.pools {
				cases[i] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch.idle)}
			}

			cases[len(cases)-1] = reflect.SelectCase{Dir: reflect.SelectDefault}
			s.lock.RUnlock()

			_, value, ok := reflect.Select(cases)
			if !ok {
				continue // a pool has been removed, try again
			}

			connection, _ := value.Interface().(*Connection)

			// [2]: Verify that we can use this connection and take it.
			if connection.Take() {
				request.connection <- connection
				break
			}
		}

		close(request.connection)
	}
}

func (s *Server) Request(w http.ResponseWriter, r *http.Request) {
	// [1]: Receive requests to be proxied
	// Parse destination URL
	dstURL := r.Header.Get("X-PROXY-DESTINATION")
	if dstURL == "" {
		wsp.ProxyErrorf(w, "Missing X-PROXY-DESTINATION header")
		return
	}

	URL, err := url.Parse(dstURL)
	if err != nil {
		wsp.ProxyErrorf(w, "Unable to parse X-PROXY-DESTINATION header")
		return
	}

	r.URL = URL

	log.Printf("[%s] %s", r.Method, r.URL.String())

	if len(s.pools) == 0 {
		wsp.ProxyErrorf(w, "No proxy available")
		return
	}

	// [2]: Take an WebSocket connection available from pools for relaying received requests.
	request := NewConnectionRequest()
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
		// It means that dispatcher has set `nil` which is a system error case that is
		// not expected in the normal flow.
		wsp.ProxyErrorf(w, "Unable to get a proxy connection")
		return
	}

	// [3]: Send the request to the peer through the WebSocket connection.
	if err := connection.proxyRequest(w, r); err != nil {
		// An error occurred throw the connection away
		log.Println(err)
		connection.Close()

		// Try to return an error to the client
		// This might fail if response headers have already been sent
		wsp.ProxyError(w, err)
	}
}

// Request receives the WebSocket upgrade handshake request from wsp_client.
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	// 1. Upgrade a received HTTP request to a WebSocket connection
	secretKey := r.Header.Get("X-SECRET-KEY")
	if secretKey != s.Config.SecretKey {
		wsp.ProxyErrorf(w, "Invalid X-SECRET-KEY")
		return
	}

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		wsp.ProxyErrorf(w, "HTTP upgrade error: %v", err)
		return
	}

	// 2. Wait a greeting message from the peer and parse it
	// The first message should contains the remote Proxy name and size
	_, greeting, err := ws.ReadMessage()
	if err != nil {
		wsp.ProxyErrorf(w, "Unable to read greeting message: %s", err)
		ws.Close()

		return
	}

	// Parse the greeting message
	split := strings.Split(string(greeting), "_")
	id := PoolID(split[0])

	size, err := strconv.Atoi(split[1])
	if err != nil {
		wsp.ProxyErrorf(w, "Unable to parse greeting message: %s", err)
		ws.Close()

		return
	}

	// 3. Register the connection into server pools.
	// s.lock is for exclusive control of pools operation.
	s.lock.Lock()
	defer s.lock.Unlock()

	var pool *Pool
	// There is no need to create a new pool,
	// if it is already registered in current pools.
	for _, p := range s.pools {
		if p.id == id {
			pool = p
			break
		}
	}

	if pool == nil {
		pool = NewPool(s, id)
		s.pools = append(s.pools, pool)
	}

	// update pool size
	pool.size = size

	// Add the WebSocket connection to the pool
	pool.Register(ws)
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

// Shutdown stop the Server.
func (s *Server) Shutdown() {
	close(s.done)
	close(s.dispatcher)

	for _, pool := range s.pools {
		pool.Shutdown()
	}

	s.clean()
}
