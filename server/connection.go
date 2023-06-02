package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golift.io/mulery"
)

// ConnectionStatus is an enumeration type which represents the status of WebSocket connection.
type ConnectionStatus int

const (
	// Idle state means it is opened but not doing work now.
	// The default value for Connection is Idle.
	Idle ConnectionStatus = iota
	Busy
	Closed
)

// Connection manages a single websocket connection from the peer.
// wsp supports multiple connections from a single peer at the same time.
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
	go conn.read() // gofunc:4 (N)

	return conn
}

// read the incoming message from the connection.
// Every connection has a read() method in a go routine.
func (connection *Connection) read() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Websocket crash recovered: %s\n%s", r, string(debug.Stack()))
		}

		connection.Close()
	}()

	for {
		if connection.Status() == Closed {
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
		_, reader, err := connection.ws.NextReader()
		if err != nil {
			break
		}

		if connection.Status() != Busy {
			// We received a wild unexpected message, but we're going to silently ignore it.
			break
		}

		// When it gets here, it is expected to be either a HttpResponse or a HttpResponseBody has been returned.
		//
		// Next, it waits to receive the value from the Connection.proxyRequest function.
		// that is invoked in the "server" thread.
		// https://github.com/hgsgtk/wsp/blob/29cc73bbd67de18f1df295809166a7a5ef52e9fa/server/connection.go#L157
		resp := <-connection.nextResponse
		if resp == nil {
			// We have been unlocked by Close().
			break
		}

		// Send the reader back to Connection.proxyRequest.
		resp <- reader

		// Wait for proxyRequest to close the channel.
		// This notifies that it is done with the reader.
		<-resp
	}
}

func (connection *Connection) Status() ConnectionStatus {
	connection.lock.Lock()
	defer connection.lock.Unlock()

	return connection.status
}

// Proxy a HTTP request through the Proxy over the websocket connection.
func (connection *Connection) proxyRequest(w http.ResponseWriter, r *http.Request) error {
	defer func() {
		if r := recover(); r != nil {
			// https://github.com/golang/go/blob/b100e127ca0e398fbb58d04d04e2443b50b3063e/src/runtime/chan.go#LL206C15-L206C15
			if err, _ := r.(error); err != nil && err.Error() != "send on closed channel" { // ignore this specific panic.
				log.Printf("panic error: %v\n%s", err, string(debug.Stack()))
			} else if err == nil {
				log.Printf("panic: %v\n%s", r, string(debug.Stack()))
			}
		}
	}()

	// [1]: Serialize HTTP request
	jsonReq, err := json.Marshal(mulery.SerializeHTTPRequest(r))
	if err != nil {
		return fmt.Errorf("unable to serialize request: %w", err)
	}

	// [2]: Send the HTTP request to the peer
	// Send the serialized HTTP request to the peer
	if err := connection.ws.WriteMessage(websocket.TextMessage, jsonReq); err != nil {
		return fmt.Errorf("unable to write request : %w", err)
	}

	// Pipe the HTTP request body to the peer
	bodyWriter, err := connection.ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return fmt.Errorf("unable to get request body writer: %w", err)
	}

	if _, err := io.Copy(bodyWriter, r.Body); err != nil {
		return fmt.Errorf("unable to pipe request body: %w", err)
	}

	if err := bodyWriter.Close(); err != nil {
		return fmt.Errorf("unable to close request body pipe: %w", err)
	}

	// [3]: Wait the HTTP response is ready
	responseChannel := make(chan (io.Reader))
	if err := connection.getNextResponse(r.Context(), responseChannel); err != nil {
		return err
	}

	responseReader, ok := <-responseChannel
	if responseReader == nil {
		if ok {
			// The value of ok is false, the channel is closed and empty.
			// See the Receiver operator in https://go.dev/ref/spec for more information.
			close(responseChannel)
		}

		return fmt.Errorf("unable to get http response reader: %w", err)
	}

	// [4]: Read the HTTP response from the peer
	// Get the serialized HTTP Response from the peer
	jsonResponse, err := io.ReadAll(responseReader)
	if err != nil {
		close(responseChannel)
		return fmt.Errorf("unable to read http response: %w", err)
	}

	// Notify the read() goroutine that we are done reading the response
	close(responseChannel)

	// Deserialize the HTTP Response
	httpResponse := new(mulery.HTTPResponse)
	if err := json.Unmarshal(jsonResponse, httpResponse); err != nil {
		return fmt.Errorf("unable to unserialize http response: %w", err)
	}

	// Write response headers back to the client
	for header, values := range httpResponse.Header {
		for _, value := range values {
			w.Header().Add(header, value)
		}
	}

	w.WriteHeader(httpResponse.StatusCode)

	// [5]: Wait for the HTTP response body to be ready.
	// Get the HTTP Response body from the peer.
	// To do so send a new channel to the read() goroutine to get the next message reader.
	responseBodyChannel := make(chan (io.Reader))
	if err := connection.getNextResponse(r.Context(), responseBodyChannel); err != nil {
		return err
	}

	responseBodyReader, ok := <-responseBodyChannel
	if responseBodyReader == nil {
		if ok {
			// If ok is false the channel is already closed
			close(responseChannel)
		}

		return fmt.Errorf("unable to get http response body reader: %w", err)
	}

	// [6]: Read the HTTP response body from the peer
	// Pipe the HTTP response body right from the remote Proxy to the client
	if _, err := io.Copy(w, responseBodyReader); err != nil {
		close(responseBodyChannel)
		return fmt.Errorf("unable to pipe response body: %w", err)
	}

	// Notify read() that we are done reading the response body
	close(responseBodyChannel)

	connection.Release()

	return nil
}

// getNextResponse waits for another upstream response, or for the client to give up.
func (connection *Connection) getNextResponse(ctx context.Context, ioCh chan io.Reader) error {
	for {
		select {
		case connection.nextResponse <- ioCh:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("http client gave up waiting for remote: %w", ctx.Err())
		}
	}
}

// Take notifies that this connection is going to be used.
func (connection *Connection) Take() bool {
	connection.lock.Lock()
	defer connection.lock.Unlock()

	switch connection.status {
	case Closed:
		return false
	case Busy:
		return false
	default:
		connection.status = Busy
		return true
	}
}

// Release signals that this connection is ready to be used again.
func (connection *Connection) Release() {
	connection.lock.Lock()
	defer connection.lock.Unlock()
	connection.release()
}

// release the connection without lock.
func (connection *Connection) release() {
	if connection.status == Closed {
		return
	}

	connection.idleSince = time.Now()
	connection.status = Idle
	// stick this connection into the idle buffer pool.
	connection.pool.idle <- connection
}

// Close the connection.
func (connection *Connection) Close() {
	connection.lock.Lock()
	defer connection.lock.Unlock()
	connection.close()
}

// Close the connection (without lock).
func (connection *Connection) close() {
	if connection.status == Closed {
		return
	}

	log.Printf("Closing connection from %s", connection.pool.id)
	// Unlock a possible read() wild message
	close(connection.nextResponse)
	// Close the underlying TCP connection
	connection.ws.Close()
	// This must be executed *before* lock.Unlock()
	connection.status = Closed
}
