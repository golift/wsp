package server

import (
	"context"
	"crypto/sha256"
	"fmt"
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

	const cleanInterval = 5 * time.Second

	cleaner := time.NewTicker(cleanInterval)
	defer cleaner.Stop()

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
		case request, ok := <-s.dispatcher:
			if !ok {
				return
			}

			s.dispatchRequest(request)
		case <-cleaner.C:
			s.cleanPools()
		}
	}
}

// cleanPools removes empty Pools; those with no incoming client connections.
// It is invoked every 5 sesconds and at shutdown.
func (s *Server) cleanPools() {
	if len(s.pools) == 0 {
		return
	}

	totals := &PoolSize{}
	connsPerPool := make(map[int]int)
	pools := make(map[clientID]*Pool, len(s.pools))

	for target, pool := range s.pools {
		if pool.IsEmpty() {
			s.Config.Logger.Debugf("Removing empty connection pool: %s", pool.id)
			pool.Shutdown()

			continue
		}

		pools[target] = pool
		ps := pool.Size()
		totals.Total += ps.Total
		totals.Idle += ps.Idle
		totals.Busy += ps.Busy
		totals.Closed += ps.Closed
		connsPerPool[ps.Total]++
	}

	s.pools = pools
	s.Config.Logger.Debugf("%d pools, %d connections, %d idle, %d busy, %d closed",
		len(s.pools), totals.Total, totals.Idle, totals.Busy, totals.Closed)
	s.saveMetrics(totals, connsPerPool)
}

func (s *Server) saveMetrics(totals *PoolSize, connsPerPool map[int]int) {
	if s.metrics == nil {
		return
	}

	// we have to limit the label values to something, and this may even be too many.
	const max = 50

	for conns, pools := range connsPerPool {
		if conns > max {
			connsPerPool[max] += pools
		}
	}

	for conns, pools := range connsPerPool {
		if conns < max {
			s.metrics.PoolConns.WithLabelValues(fmt.Sprint(conns)).Set(float64(pools))
		} else if conns == max {
			s.metrics.PoolConns.WithLabelValues(fmt.Sprint(conns, "+")).Set(float64(pools))
		}
	}

	s.metrics.Conns.WithLabelValues("total").Set(float64(totals.Total))
	s.metrics.Conns.WithLabelValues("busy").Set(float64(totals.Busy))
	s.metrics.Conns.WithLabelValues("idle").Set(float64(totals.Idle))
	s.metrics.Conns.WithLabelValues("closed").Set(float64(totals.Closed))
	s.metrics.Pools.Set(float64(len(s.pools)))
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
func (s *Server) registerPool(newPool *PoolConfig) {
	if newPool.secret != "" {
		hash := sha256.New()
		hash.Write([]byte(newPool.secret + string(newPool.ID)))
		// As promised, if a custom key validator returns a secret(string),
		// hash that with the client id to create a new client id.
		// This is custom logic you probably don't want, so don't return a string from your key validator.
		newPool.ID = clientID(fmt.Sprintf("%x", hash.Sum(nil)))
	}

	if pool, ok := s.pools[newPool.ID]; !ok || pool == nil {
		s.pools[newPool.ID] = NewPool(s, newPool)
	}

	// Add the WebSocket connection to the pool
	s.pools[newPool.ID].Register(newPool.Sock)
}

// Shutdown stops the Server.
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
}
