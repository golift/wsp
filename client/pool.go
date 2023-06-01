package client

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Pool manage a pool of connection to a remote Server.
type Pool struct {
	client      *Client
	target      string
	secretKey   string
	connections []*Connection
	done        chan struct{}
	getSize     chan struct{}
	repSize     chan *PoolSize
	delChan     chan *Connection
	repChan     chan struct{}
}

// PoolSize represent the number of open connections per status.
type PoolSize struct {
	Connecting int
	Idle       int
	Running    int
	Total      int
}

// StartPool creates and starts a pool in one command.
func StartPool(ctx context.Context, client *Client, target string, secretKey string) *Pool {
	pool := NewPool(client, target, secretKey)
	pool.Start(ctx)

	return pool
}

// NewPool creates a new Pool.
func NewPool(client *Client, target string, secretKey string) *Pool {
	return &Pool{
		client:      client,
		target:      target,
		secretKey:   secretKey,
		connections: []*Connection{},
		done:        make(chan struct{}),
		getSize:     make(chan struct{}),
		repSize:     make(chan *PoolSize),
		delChan:     make(chan *Connection),
		repChan:     make(chan struct{}),
	}
}

// Start connects to the remote server and runs a one-second loop to maintain the connection.
func (pool *Pool) Start(ctx context.Context) {
	pool.connector(ctx)

	go func() {
		ticker := time.NewTicker(time.Second)

		defer func() {
			ticker.Stop()
			close(pool.getSize)
			close(pool.repSize)
			close(pool.delChan)
			close(pool.repChan)
		}()

		for {
			select {
			case <-pool.done:
				for _, conn := range pool.connections {
					conn.Close()
				}

				return
			case <-ticker.C:
				pool.connector(ctx)
			case <-pool.getSize:
				pool.repSize <- pool.size()
			case conn := <-pool.delChan:
				pool.remove(conn)
				pool.repChan <- struct{}{}
			}
		}
	}()
}

// The garbage collector runs every second.
// If the size of the pool is not equivalent to the desired size,
// then N go functions are created that add additional pool connections.
// If the connection fails, the connection is removed form the pool.
//
// This method was also being called every time a server makes a request, that was removed.
func (pool *Pool) connector(ctx context.Context) {
	poolSize := pool.size()
	// Create enough connection to fill the pool
	toCreate := pool.client.Config.PoolIdleSize - poolSize.Idle

	// Create only one connection if the pool is empty
	if poolSize.Total == 0 {
		toCreate = 1
	}

	// Open at most PoolMaxSize connections.
	if poolSize.Total+toCreate > pool.client.Config.PoolMaxSize {
		toCreate = pool.client.Config.PoolMaxSize - poolSize.Total
	}

	// Try to reach ideal pool size.
	for i := 0; i < toCreate; i++ {
		// This is the only place a connection is added to the pool.
		conn := NewConnection(pool)
		if err := conn.Connect(ctx); err != nil {
			log.Printf("Unable to connect to %s: %s", pool.target, err)
		} else {
			pool.connections = append(pool.connections, conn)
		}
	}
}

// Remove a connection from the pool.
func (pool *Pool) Remove(conn *Connection) {
	pool.delChan <- conn
	<-pool.repChan
}

func (pool *Pool) remove(connection *Connection) {
	// This trick uses the fact that a slice shares the same backing array and capacity as the original,
	// so the storage is reused for the filtered slice. Of course, the original contents are modified.
	var filtered []*Connection // == nil

	for _, conn := range pool.connections {
		if connection != conn {
			filtered = append(filtered, conn)
		} else {
			conn.Close()
		}
	}

	pool.connections = filtered
}

// Shutdown and close all connections in the pool.
func (pool *Pool) Shutdown() {
	close(pool.done)
}

func (poolSize *PoolSize) String() string {
	return fmt.Sprintf("Connecting %d, idle %d, running %d, total %d",
		poolSize.Connecting, poolSize.Idle, poolSize.Running, poolSize.Total)
}

// Size returns the current telemetric state of the pool.
func (pool *Pool) Size() *PoolSize {
	pool.getSize <- struct{}{}
	return <-pool.repSize
}

func (pool *Pool) size() *PoolSize {
	poolSize := new(PoolSize)
	poolSize.Total = len(pool.connections)

	for _, connection := range pool.connections {
		switch connection.Status() {
		case CONNECTING:
			poolSize.Connecting++
		case IDLE:
			poolSize.Idle++
		case RUNNING:
			poolSize.Running++
		}
	}

	return poolSize
}
