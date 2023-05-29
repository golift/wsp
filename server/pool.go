package server

import (
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Pool handles all connections from the peer.
type Pool struct {
	server      *Server
	id          PoolID
	size        int
	connections []*Connection
	idle        chan *Connection
	done        bool
	lock        sync.RWMutex
}

// PoolID represents the identifier of the connected WebSocket client.
type PoolID string

// NewPool creates a new Pool.
func NewPool(server *Server, id PoolID) *Pool {
	pool := new(Pool)
	pool.server = server
	pool.id = id
	pool.idle = make(chan *Connection)

	return pool
}

// Register creates a new Connection and adds it to the pool.
func (pool *Pool) Register(ws *websocket.Conn) {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	// Ensure we never add a connection to a pool we have garbage collected.
	if pool.done {
		return
	}

	log.Printf("Registering new connection from %s", pool.id)

	connection := NewConnection(pool, ws)
	pool.connections = append(pool.connections, connection)
}

// Offer offers an idle connection to the server.
func (pool *Pool) Offer(connection *Connection) {
	// The original code of root-gg/wsp was invoking goroutine,
	// but the callder was also invoking goroutine,
	// so it was deemed unnecessary and removed.
	pool.idle <- connection
}

// Clean removes dead connection from the pool.
// Look for dead connection in the pool.
// This MUST be surrounded by pool.lock.Lock().
func (pool *Pool) Clean() {
	idle := 0
	connections := []*Connection{}

	for _, connection := range pool.connections {
		// We need to be sur we'll never close a BUSY or soon to be BUSY connection
		connection.lock.Lock()

		if connection.status == Idle {
			if idle++; idle > pool.size {
				// We have enough idle connections in the pool.
				// Terminate the connection if it is idle since more that IdleTimeout
				if time.Since(connection.idleSince) > pool.server.Config.IdleTimeout {
					connection.close()
				}
			}
		}

		connection.lock.Unlock()

		if connection.status == Closed {
			continue
		}

		connections = append(connections, connection)
	}

	pool.connections = connections
}

// IsEmpty clean the pool and return true if the pool is empty.
func (pool *Pool) IsEmpty() bool {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	pool.Clean()

	return len(pool.connections) == 0
}

// Shutdown closes every connections in the pool and cleans it.
func (pool *Pool) Shutdown() {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	pool.done = true

	for _, connection := range pool.connections {
		connection.Close()
	}

	pool.Clean()
}

// PoolSize is the number of connection in each state in the pool.
type PoolSize struct {
	Idle   int
	Busy   int
	Closed int
}

// Size return the number of connection in each state in the pool.
func (pool *Pool) Size() *PoolSize {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	size := new(PoolSize)

	for _, connection := range pool.connections {
		switch connection.status {
		case Idle:
			size.Idle++
		case Busy:
			size.Busy++
		case Closed:
			size.Closed++
		}
	}

	return size
}
