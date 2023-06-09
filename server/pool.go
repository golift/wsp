package server

import (
	"time"

	"github.com/gorilla/websocket"
	"golift.io/mulery/mulch"
)

// Pool handles all connections from the peer.
// Each pool is unique by it's clientID.
type Pool struct {
	connected   time.Time
	handshake   *mulch.Handshake
	done        bool
	minSize     int
	idleTimeout time.Duration
	id          string
	connections []*Connection
	closed      int
	idle        chan *Connection
	newConn     chan *Connection
	askClean    chan struct{}
	askSize     chan struct{}
	getSize     chan *PoolSize
	mulch.Logger
	metrics *Metrics
}

// clientID represents the identifier of the connected WebSocket client.
type clientID string

// NewPool creates a new Pool, and starts one go routine per pool to keep it clean and running.
// Each pool represents 1 client, and each client may have many connections.
func NewPool(server *Server, client *PoolConfig, altID string) *Pool {
	// We increase the idle pool buffer size in case a client restarts.
	// This allows the restarted client to reconnect before the previous connections get used up from the buffer.
	// If the buffer fills, new connections are rejected.
	const idlePoolMultiplier = 3

	if altID == "" {
		altID = client.ID
	}

	// update pool size; we add 1 so the pool may have 1 thread more than it's minimum idle.
	pool := &Pool{
		connected:   time.Now(),
		handshake:   client.Handshake,
		id:          altID,
		minSize:     client.Size + 1, // This 1 allows slightly less thread teardown/bringup.
		idle:        make(chan *Connection, client.MaxSize*idlePoolMultiplier),
		idleTimeout: server.Config.IdleTimeout,
		newConn:     make(chan *Connection),
		askClean:    make(chan struct{}),
		askSize:     make(chan struct{}),
		getSize:     make(chan *PoolSize),
		Logger:      server.Config.Logger,
		metrics:     server.metrics,
	}

	go pool.keepRunning() // gofunc:3 (N)

	return pool
}

func (pool *Pool) shutdown() {
	if pool.done {
		return
	}

	pool.done = true

	for _, connection := range pool.connections {
		connection.Close("shutdown")
	}

	close(pool.askClean)
	close(pool.askSize)
	close(pool.getSize)
}

func (pool *Pool) keepRunning() {
	defer pool.shutdown()

	for {
		select {
		case <-pool.askClean:
			pool.clean()
			pool.getSize <- &PoolSize{Total: len(pool.connections)} // shoehorn.
		case <-pool.askSize:
			pool.getSize <- pool.size()
		case conn, ok := <-pool.newConn:
			if !ok {
				return
			}

			if !pool.done {
				pool.clean()
				pool.connections = append(pool.connections, conn)
				pool.Printf("Registering new connection from %s [%s], tunnels: %d, max: %d",
					pool.id, conn.sock.RemoteAddr(), len(pool.connections), cap(pool.idle))
			}
		}
	}
}

// Register creates a new Connection and adds it to the pool.
func (pool *Pool) Register(ws *websocket.Conn) {
	pool.newConn <- NewConnection(pool, ws)
}

// clean removes dead and idle connections from the pool.
// Calling pool.IsEmpty is the only way to trigger this.
func (pool *Pool) clean() {
	var (
		idle = 0
		save = []*Connection{}
		keep bool
	)

	for _, connection := range pool.connections {
		if idle, keep = pool.cleanConnection(connection, idle); keep {
			save = append(save, connection)
		} else {
			pool.closed++
		}
	}

	pool.connections = save
}

func (pool *Pool) cleanConnection(connection *Connection, idle int) (int, bool) {
	// Ensure a busy connection is never closed.
	connection.lock.Lock()
	defer connection.lock.Unlock()

	if connection.status == Idle {
		idle++
		// Terminate the connection if it is idle since more that IdleTimeout.
		if age := time.Since(connection.idleSince); idle > pool.minSize && age > pool.idleTimeout {
			// We have enough idle connections in the pool, and this one is old.
			pool.Printf("Closing idle connection: %s [%s], tunnels: %d , max: %d",
				pool.id, connection.sock.RemoteAddr(), len(pool.connections), cap(pool.idle))
			connection.close("idle " + age.String())
		}
	}

	return idle, connection.status != Closed
}

// IsEmpty cleans the pool and return true if the pool is empty.
func (pool *Pool) IsEmpty() bool {
	pool.askClean <- struct{}{}
	return (<-pool.getSize).Total == 0
}

// Shutdown closes every connection in the pool and closes all channels.
func (pool *Pool) Shutdown() {
	close(pool.newConn)
}

// PoolSize is the number of connection in each state in the pool.
type PoolSize struct {
	Total  int          `json:"total"`
	Idle   int          `json:"idle"`
	Busy   int          `json:"busy"`
	Closed int          `json:"closed"`
	Conns  []*ConnStats `json:"conns"`
}

type ConnStats struct {
	Remote    string    `json:"remote"`
	Requests  int       `json:"requests"`
	Connected time.Time `json:"conneteed"`
}

// Size return the number of connection in each state in the pool.
func (pool *Pool) Size() *PoolSize {
	pool.askSize <- struct{}{}
	return <-pool.getSize
}

// size return the number of connection in each state in the pool. not thread safe.
func (pool *Pool) size() *PoolSize {
	size := PoolSize{
		Total:  len(pool.connections),
		Closed: pool.closed,
		Conns:  make([]*ConnStats, len(pool.connections)),
	}

	for idx, connection := range pool.connections {
		size.Conns[idx] = &ConnStats{
			Remote:    connection.sock.RemoteAddr().String(),
			Connected: connection.connected,
			Requests:  connection.requests,
		}

		switch connection.status {
		case Idle:
			size.Idle++
		case Busy:
			size.Busy++
		}
	}

	return &size
}
