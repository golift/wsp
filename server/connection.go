package server

import (
	"io"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ConnectionStatus is an enumeration that represents the status of WebSocket connection.
type ConnectionStatus int

const (
	// Idle state means it is opened but not doing work now.
	// The default value for Connection is Idle.
	Idle   ConnectionStatus = iota
	Busy                    // Unavailble for use.
	Closed                  // Never use again.
)

// Connection manages a single websocket connection from the peer.
// Supports multiple connections from a single peer at the same time (a pool).
type Connection struct {
	pool      *Pool // the pool this connection belongs to.
	ws        *websocket.Conn
	status    ConnectionStatus
	idleSince time.Time
	lock      sync.Mutex
	// nextResponse is the channel to wait for an HTTP response.
	//
	// The `read` function waits to receive the HTTP response as a separate thread reader.
	// (See https://github.com/hgsgtk/wsp/blob/29cc73bbd67de18f1df295809166a7a5ef52e9fa/server/connection.go#L56 )
	//
	// When a "server" thread proxies, it sends the HTTP request to the peer over the WebSocket,
	// and sends the channel of the io.Reader interface (chan io.Reader) that can read the HTTP
	// response to the field `nextResponse`, then waits until the value is written in the channel
	// (chan io.Reader) by another thread "reader".
	//
	// After the thread "reader" detects that the HTTP response from the peer of the WebSocket connection has been written,
	// it sends the value to the channel (chan io.Reader),
	// and the "server" thread can proceed to process the rest of its procedures.
	nextResponse chan chan io.Reader
}

// NewConnection returns a new Connection.
// Each connection gets a go routine to read (wait for) messages.
func NewConnection(pool *Pool, ws *websocket.Conn) *Connection {
	// Initialize a new Connection
	conn := &Connection{
		pool:         pool,
		ws:           ws,
		nextResponse: make(chan chan io.Reader),
		status:       Idle,
	}
	// Mark that this connection is ready to use for relay.
	conn.Release()
	// Start to listen to incoming messages over the WebSocket connection.
	go conn.read()

	return conn
}

// read the incoming message from the connection.
// Every connection has a read() method in a go routine.
func (c *Connection) read() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Websocket crash recovered: %s\n%s", r, string(debug.Stack()))
		}

		c.Close()
	}()

	var (
		err    error
		reader io.Reader
		resp   chan io.Reader
	)

	for {
		if c.Status() == Closed {
			break
		}

		// https://godoc.org/github.com/gorilla/websocket#hdr-Control_Messages
		//
		// We need to ensure :
		//  - no concurrent calls to ws.NextReader() / ws.ReadMessage()
		//  - only one reader exists at a time
		//  - wait for reader to be consumed before requesting the next one
		//  - always be reading on the socket to be able to process control messages ( ping / pong / close )

		// We will block here until a message is received or the ws is closed
		if _, reader, err = c.ws.NextReader(); err != nil {
			break
		}

		if c.Status() != Busy {
			// We received a wild unexpected message, but we're going to silently ignore it.
			break
		}

		// When it gets here, it is expected that either a HttpResponse or a HttpResponseBody has been returned.
		//
		// Next, it waits to receive the value from the Connection.proxyRequest function.
		// that is invoked in the "server" thread.
		// https://github.com/hgsgtk/wsp/blob/29cc73bbd67de18f1df295809166a7a5ef52e9fa/server/connection.go#L157
		if resp = <-c.nextResponse; resp == nil {
			break // We have been unlocked by Close().
		}

		// Send the reader back to Connection.proxyRequest.
		resp <- reader

		// Wait for proxyRequest to close the channel.
		// This notifies that it is done with the reader.
		<-resp // Start the loop over, and take back control of the ws reader.
	}
}

func (c *Connection) Status() ConnectionStatus {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.status
}

// Take notifies that this connection is going to be used.
func (c *Connection) Take() bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	switch c.status {
	case Closed:
		return false
	case Busy:
		return false
	default:
		c.status = Busy
		return true
	}
}

// Release signals that this connection is ready to be used again.
func (c *Connection) Release() {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.status == Closed {
		return
	}

	c.idleSince = time.Now()
	c.status = Idle
	// stick this connection into the idle buffer pool.
	c.pool.idle <- c
}

// Close the connection.
func (c *Connection) Close() {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.close()
}

// Close the connection (without lock).
func (c *Connection) close() {
	if c.status == Closed {
		return
	}

	log.Printf("Closing connection from %s", c.pool.id)
	// Unlock a possible wild read() message.
	close(c.nextResponse)
	// Close the underlying TCP connection.
	c.ws.Close()
	// This must be executed *before* lock.Unlock().
	c.status = Closed
}
