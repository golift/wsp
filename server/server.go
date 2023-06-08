package server

import (
	"crypto/sha256"
	"fmt"
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
	defer s.shutdown() // close(s.dispatcher)

	const cleanInterval = 5 * time.Second

	cleaner := time.NewTicker(cleanInterval)
	defer cleaner.Stop()

	for i := s.Config.Dispatchers; i > 0; i-- {
		go func() {
			for r := range s.dispatcher {
				s.dispatchRequest(r)
			}

			// notify shutdown() that dispatcher is closed.
			s.getPool <- ""
		}()
	}

	for {
		// Runs in an infinite loop:
		// - Checks for done channel closing.
		// - Runs cleaner every 5 seconds.
		select {
		case newPool, ok := <-s.newPool:
			if !ok {
				return
			}

			s.registerPool(newPool)
		case name := <-s.getPool:
			s.repPool <- s.pools[name]
		case <-cleaner.C:
			s.cleanPools()
		case clientID := <-s.getStats:
			s.repStats <- s.poolStats(clientID)
		}
	}
}

func (s *Server) poolStats(cID clientID) map[clientID]*PoolSize {
	if cID != "" {
		if s.pools[cID] == nil {
			return map[clientID]*PoolSize{"id not found": nil}
		}

		return map[clientID]*PoolSize{cID: s.pools[cID].size()}
	}

	pools := make(map[clientID]*PoolSize, len(s.pools))
	for target, pool := range s.pools {
		pools[target] = pool.size()
	}

	return pools
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
			s.closed += pool.closed

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

	totals.Closed += s.closed
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
// So the problem here is that this function is blocking, and it will block
// every single request if there is no available idle connection for the
// current request it's processing. If the clients are slow and the requests
// long this could be problematic. Start more dispatchers if you need to.
func (s *Server) dispatchRequest(request *dispatchRequest) {
	defer close(request.connection)

	for {
		// Ask the main thread for this pool by ID.
		s.getPool <- request.client
		// Get the pool reply from the main thread.
		pool := <-s.repPool
		if pool == nil {
			s.Config.Logger.Debugf("Got an empty pool for %s", request.client)
			return // no client pool with that name.
		}

		// This blocks until an idle connection is available.
		// Verify that we can use this connection and take it.
		if connection := (<-pool.idle).Take(); connection != nil {
			request.connection <- connection
			return
		}
	}
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

	for i := s.Config.Dispatchers; i > 0; i-- {
		<-s.getPool // wait for dispatchers to finish.
	}

	close(s.getPool)
	close(s.repPool)
	close(s.getStats)
	close(s.repStats)

	for target, pool := range s.pools {
		pool.Shutdown()
		delete(s.pools, target)
	}
}
