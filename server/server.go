package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"reflect"
	"time"
)

var (
	ErrInvalidKey    = fmt.Errorf("invalid secret key provided")
	ErrNoClientID    = fmt.Errorf("required client id header is missing")
	ErrNoProxyTarget = fmt.Errorf("no proxy target found for request")
	ErrInvalidData   = fmt.Errorf("invalid data received")
)

// StartDispatcher dispatches connection from available pools to client requests.
// You need to start this in a go routine.
func (s *Server) StartDispatcher() {
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
			s.cleanPools()
		case request, ok := <-s.dispatcher:
			if !ok {
				return
			}

			s.dispatchRequest(request)
		}
	}
}

// cleanPools removes empty Pools; those with no incoming client connections.
// It is invoked every 5 sesconds and at shutdown.
func (s *Server) cleanPools() {
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
	log.Printf("%d pools, %d connections, %d idle, %d busy, %d closed",
		len(s.pools), conns, idle, busy, closed)
}

// dispatchRequest runs every time an http request comes into the server.
// This finds a pool for the request, and sends the request to it.
func (s *Server) dispatchRequest(request *dispatchRequest) {
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
		connection, ok := s.findSocketConnection(request)
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

// findSocketConnection searches the pools for one that matches the request.
// Or if no client ID is provided, then it returns a random connection.
func (s *Server) findSocketConnection(request *dispatchRequest) (*Connection, bool) {
	if request.client != "" {
		if s.pools[request.client] != nil {
			return <-s.pools[request.client].idle, true
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

// Register the connection into server pools.
// This is called through a channel from the register handler.
func (s *Server) registerPool(newPool *poolConfig) {
	if newPool.secret != "" {
		hash := sha256.New()
		hash.Write([]byte(newPool.secret + string(newPool.clientID)))
		// As promised, if a custom key validator returns a secret(string),
		// hash that with the client id to create a new client id.
		// This is custom logic you probably don't want, so don't return a string from your key validator.
		newPool.clientID = clientID(fmt.Sprintf("%x", hash.Sum(nil)))
	}

	if pool, ok := s.pools[newPool.clientID]; !ok || pool == nil {
		s.pools[newPool.clientID] = NewPool(s, newPool.clientID, newPool.max)
	}

	// update pool size
	s.pools[newPool.clientID].minSize = newPool.size

	// Add the WebSocket connection to the pool
	s.pools[newPool.clientID].Register(newPool.sock)
}

// Shutdown stop the Server.
func (s *Server) Shutdown() {
	// closing this channel makes shutdown() run.
	close(s.newPool)
}

func (s *Server) shutdown() {
	close(s.dispatcher)

	for target, pool := range s.pools {
		pool.Shutdown()
		delete(s.pools, target)
	}

	s.cleanPools()
}
