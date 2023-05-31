package server

import (
	"crypto/sha256"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

// Pool handles all connections from the peer.
type Pool struct {
	done        bool
	length      int
	idleTimeout time.Duration
	id          clientID
	connections []*Connection
	idle        chan *Connection
	newConn     chan *Connection
	askClean    chan struct{}
	askSize     chan struct{}
	getSize     chan *PoolSize
}

// clientID represents the identifier of the connected WebSocket client.
type clientID string

// NewPool creates a new Pool, and starts one go routine per pool to keep it clean and running.
// Each pool represents 1 client, and each client may have many connections.
// hashSeed is optional and changes how client IDs are generated.
func NewPool(server *Server, id clientID, max int, hashSeed string) *Pool {
	if hashSeed != "" {
		hash := sha256.New()
		hash.Write([]byte(hashSeed + string(id)))
		id = clientID(fmt.Sprintf("%x", hash.Sum(nil)))
	}

	pool := &Pool{
		id:          id,
		idle:        make(chan *Connection, max),
		idleTimeout: server.Config.IdleTimeout,
		newConn:     make(chan *Connection),
		askClean:    make(chan struct{}),
		askSize:     make(chan struct{}),
		getSize:     make(chan *PoolSize),
	}

	go pool.keepRunning() //gofunc:3 (N)

	return pool
}

func (pool *Pool) shutdown() {
	pool.done = true

	for _, connection := range pool.connections {
		connection.Close()
	}

	close(pool.askClean)
	close(pool.askSize)
	close(pool.getSize)
	pool.clean()
}

func (pool *Pool) keepRunning() {
	defer pool.shutdown()

	for {
		select {
		case <-pool.askClean:
			pool.clean()
			pool.getSize <- &PoolSize{Total: len(pool.connections)}
		case <-pool.askSize:
			pool.getSize <- pool.size()
		case conn, ok := <-pool.newConn:
			if !ok {
				return
			}

			if !pool.done {
				pool.connections = append(pool.connections, conn)
			}
		}
	}
}

// Register creates a new Connection and adds it to the pool.
func (pool *Pool) Register(ws *websocket.Conn) {
	log.Printf("Registering new connection from %s", pool.id)
	pool.newConn <- NewConnection(pool, ws)
}

// clean removes dead and idle connections from the pool.
func (pool *Pool) clean() {
	idle := 0
	connections := []*Connection{}

	for _, connection := range pool.connections {
		// We need to be sure we'll never close a BUSY or soon to be BUSY connection.
		connection.lock.Lock()

		if connection.status == Idle {
			if idle++; idle > pool.length {
				// We have enough idle connections in the pool.
				// Terminate the connection if it is idle since more that IdleTimeout
				if time.Since(connection.idleSince) > pool.idleTimeout {
					log.Println("[DEBUG] Closing idle connection: ", connection.pool.id)
					connection.close()
				}
			}
		}

		if connection.status == Closed {
			connection.lock.Unlock()
			continue
		}

		connection.lock.Unlock()

		connections = append(connections, connection)
	}

	pool.connections = connections
}

// IsEmpty cleans the pool and return true if the pool is empty.
func (pool *Pool) IsEmpty() bool {
	pool.askClean <- struct{}{}
	return (<-pool.getSize).Total == 0
}

// Shutdown closes every connection in the pool and cleans it.
func (pool *Pool) Shutdown() {
	close(pool.newConn)
}

// PoolSize is the number of connection in each state in the pool.
type PoolSize struct {
	Total  int
	Idle   int
	Busy   int
	Closed int
}

// Size return the number of connection in each state in the pool.
func (pool *Pool) Size() *PoolSize {
	pool.askSize <- struct{}{}
	return <-pool.getSize
}

// size return the number of connection in each state in the pool. not thread safe.
func (pool *Pool) size() *PoolSize {
	size := &PoolSize{
		Total: len(pool.connections),
	}

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
