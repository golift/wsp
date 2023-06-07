package client

import (
	"context"
	"fmt"
	"time"
)

// Pool of connections to a remote Server.
type Pool struct {
	client      *Client
	target      string
	secretKey   string
	connections []*Connection
	done        chan struct{}
	getSize     chan struct{}
	repSize     chan *PoolSize
	conChan     chan *Connection
	repChan     chan struct{}
	shutdown    bool
	lastTry     time.Time
	backOff     time.Duration
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
		conChan:     make(chan *Connection),
		repChan:     make(chan struct{}),
		backOff:     time.Second,
	}
}

// Start connects to the remote server and runs a one-second loop to maintain the connection.
func (p *Pool) Start(ctx context.Context) {
	p.connector(ctx, time.Now())

	go func() {
		ticker := time.NewTicker(time.Second)

		defer func() {
			ticker.Stop()
			close(p.getSize)
			close(p.repSize)
			close(p.conChan)
			close(p.repChan)
		}()

		for {
			select {
			case <-p.done:
				for _, conn := range p.connections {
					conn.Close()
				}

				return
			case now := <-ticker.C:
				p.connector(ctx, now)
			case <-p.getSize:
				p.repSize <- p.size()
			case conn := <-p.conChan:
				if conn == nil {
					p.connector(ctx, time.Now())
				} else {
					p.remove(conn)
				}

				p.repChan <- struct{}{}
			}
		}
	}()
}

// The garbage collector runs every second.
// If the size of the pool is not equivalent to the desired size,
// then N go functions are created that add additional pool connections.
// If the connection fails, the connection is removed from the pool.
func (p *Pool) connector(ctx context.Context, now time.Time) {
	//nolint:gomnd
	if p.backOff > 20*time.Second {
		p.backOff = 10 * time.Second // keep bringing it back down.
	}

	if now.Sub(p.lastTry) < p.backOff {
		return
	}

	p.lastTry = now
	poolSize := p.size()
	// Create enough connection to fill the pool.
	toCreate := p.client.Config.PoolIdleSize - poolSize.Idle

	// Create only one connection if the pool is empty.
	if poolSize.Total == 0 && toCreate < 1 {
		toCreate = 1
	}

	// Open at most PoolMaxSize connections.
	if poolSize.Total+toCreate > p.client.Config.PoolMaxSize {
		toCreate = p.client.Config.PoolMaxSize - poolSize.Total
	}

	// Try to reach ideal pool size.
	for ; toCreate > 0; toCreate-- {
		// This is the only place a connection is added to the pool.
		conn := NewConnection(p)
		if err := conn.Connect(ctx); err != nil {
			p.client.Errorf("Connecting to tunnel @ %s: %s", p.target, err)
			p.backOff += time.Second

			break // don't try any more this round.
		}

		p.connections = append(p.connections, conn)
		p.backOff = time.Second
	}
}

// Remove a connection from the pool.
func (p *Pool) Remove(conn *Connection) {
	if !p.shutdown {
		p.conChan <- conn
		<-p.repChan
	}
}

func (p *Pool) remove(connection *Connection) {
	// This trick uses the fact that a slice shares the same backing array and capacity as the original,
	// so the storage is reused for the filtered slice. Of course, the original contents are modified.
	var filtered []*Connection // == nil

	for _, conn := range p.connections {
		if connection != conn {
			filtered = append(filtered, conn)
		} else {
			conn.Close()
		}
	}

	p.connections = filtered
}

// Shutdown and close all connections in the pool.
func (p *Pool) Shutdown() {
	p.shutdown = true
	close(p.done)
}

func (ps *PoolSize) String() string {
	return fmt.Sprintf("Connecting %d, idle %d, running %d, total %d",
		ps.Connecting, ps.Idle, ps.Running, ps.Total)
}

// Size returns the current telemetric state of the pool.
func (p *Pool) Size() *PoolSize {
	p.getSize <- struct{}{}
	return <-p.repSize
}

func (p *Pool) size() *PoolSize {
	poolSize := new(PoolSize)
	poolSize.Total = len(p.connections)

	for _, connection := range p.connections {
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
