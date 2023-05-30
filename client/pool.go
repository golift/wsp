package client

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Pool manage a pool of connection to a remote Server.
type Pool struct {
	client      *Client
	target      string
	secretKey   string
	connections []*Connection
	lock        sync.RWMutex
	done        chan struct{}
}

// PoolSize represent the number of open connections per status.
type PoolSize struct {
	connecting int
	idle       int
	running    int
	total      int
}

// NewPool creates a new Pool.
func NewPool(client *Client, target string, secretKey string) *Pool {
	return &Pool{
		client:      client,
		target:      target,
		secretKey:   secretKey,
		connections: []*Connection{},
		done:        make(chan struct{}),
	}
}

// Start connects to the remote server and runs a one-second loop to maintain the connection.
func (pool *Pool) Start(ctx context.Context) {
	pool.connector(ctx)

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-pool.done:
				return
			case <-ticker.C:
				pool.connector(ctx)
			}
		}
	}()
}

// The garbage collector runs every second. It locks the mutex and then checks the pool size.
// If the size of the pool is not equivalent to the desired size,
// then N go functions are created that add additional pool connections.
// If the connection fails, the mutex is locked again and the connection is removed form the pool.
//
// The go function is because of the mutex lock required in the called method.
// This method was also being called every time a server makes a request, that was removed.
func (pool *Pool) connector(ctx context.Context) {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	poolSize := pool.size()
	// Create enough connection to fill the pool
	toCreate := pool.client.Config.PoolIdleSize - poolSize.idle

	// Create only one connection if the pool is empty
	if poolSize.total == 0 {
		toCreate = 1
	}

	// Ensure to open at most PoolMaxSize connections
	if poolSize.total+toCreate > pool.client.Config.PoolMaxSize {
		toCreate = pool.client.Config.PoolMaxSize - poolSize.total
	}

	// Try to reach ideal pool size
	for i := 0; i < toCreate; i++ {
		// This is the only place a connection is added to the pool.
		conn := NewConnection(pool)
		pool.connections = append(pool.connections, conn)

		go func() {
			err := conn.Connect(ctx)
			if err != nil {
				log.Printf("Unable to connect to %s: %s", pool.target, err)
				pool.remove(conn)
			}
		}()
	}
}

// Remove a connection from the pool.
func (pool *Pool) remove(conn *Connection) {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	// This trick uses the fact that a slice shares the same backing array and capacity as the original,
	// so the storage is reused for the filtered slice. Of course, the original contents are modified.

	var filtered []*Connection // == nil

	for _, c := range pool.connections {
		if conn != c {
			filtered = append(filtered, c)
		}
	}

	pool.connections = filtered
}

// Shutdown close all connection in the pool.
func (pool *Pool) Shutdown() {
	close(pool.done)

	for _, conn := range pool.connections {
		conn.Close()
	}
}

func (poolSize *PoolSize) String() string {
	return fmt.Sprintf("Connecting %d, idle %d, running %d, total %d",
		poolSize.connecting, poolSize.idle, poolSize.running, poolSize.total)
}

// size returns the current telemetric state of the pool.
// This is currently not thread safe.
func (pool *Pool) size() *PoolSize {
	poolSize := new(PoolSize)
	poolSize.total = len(pool.connections)

	for _, connection := range pool.connections {
		switch connection.status {
		case CONNECTING:
			poolSize.connecting++
		case IDLE:
			poolSize.idle++
		case RUNNING:
			poolSize.running++
		}
	}

	return poolSize
}
