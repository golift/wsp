package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"golift.io/mulery"
)

// Status of a Connection.
const (
	UNKNOWN    = -1
	CONNECTING = iota
	IDLE
	RUNNING
)

const (
	keepAliveInterval = 60 * time.Second
	keepAliveTimeout  = 5 * time.Second
)

// Connection handle a single websocket (HTTP/TCP) connection to an Server.
type Connection struct {
	pool      *Pool
	ws        *websocket.Conn
	status    int
	setStatus chan int
	getStatus chan int
}

// NewConnection creates a Connection object.
func NewConnection(pool *Pool) *Connection {
	return &Connection{
		pool:      pool,
		status:    CONNECTING,
		setStatus: make(chan int),
		getStatus: make(chan int),
	}
}

// Connect to the remote server using a HTTP websocket.
func (connection *Connection) Connect(ctx context.Context) error {
	connection.pool.client.Debugf("Connecting to tunnel @ %s", connection.pool.target)

	var err error
	// Create a new TCP(/TLS) connection (no use of net.http)
	//nolint:bodyclose // Gets closed in the Close() method.
	connection.ws, _, err = connection.pool.client.dialer.DialContext(
		ctx,
		connection.pool.target,
		http.Header{"X-SECRET-KEY": {connection.pool.secretKey}},
	)
	if err != nil {
		return fmt.Errorf("tcp tunnel dialer failure: %w", err)
	}

	// Send the greeting message with proxy id and wanted pool size.
	greeting := fmt.Sprintf("%s_%d_%d",
		connection.pool.client.Config.ID,
		connection.pool.client.Config.PoolIdleSize,
		connection.pool.client.Config.PoolMaxSize,
	)

	if err := connection.ws.WriteMessage(websocket.TextMessage, []byte(greeting)); err != nil {
		connection.pool.Remove(connection)
		return fmt.Errorf("tunnel greeting failure: %w", err)
	}

	// We are connected to the server, now start a go routine that waits for incoming server requests.
	go connection.serve()
	go connection.keepAlive()

	return nil
}

// Keep connection alive.
func (connection *Connection) keepAlive() {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case tick := <-ticker.C:
			err := connection.ws.WriteControl(websocket.PingMessage, []byte{}, tick.Add(keepAliveTimeout))
			if err != nil {
				connection.pool.client.Errorf("Tunnel keep-alive failure: %v", err)

				return
			}
		case status, ok := <-connection.setStatus:
			if !ok {
				return
			}

			if status == UNKNOWN { // signal to return status.
				connection.getStatus <- connection.status
				continue
			}

			connection.status = status
		}
	}
}

func (connection *Connection) Status() int {
	connection.setStatus <- UNKNOWN
	return <-connection.getStatus
}

// serve is the main loop, it:
//   - Waits to receive HTTP requests from the Server.
//   - Executes HTTP requests.
//   - Sends HTTP responses back to the Server.
//
// As in the server code there is no buffering of HTTP request/response body.
// As in the server if any error occurs the connection is closed/thrown.
func (connection *Connection) serve() {
	defer connection.pool.Remove(connection)

	for {
		if !connection.serveHandler() {
			return
		}
	}
}

// serveHandler is the main loop that handles incoming http requests from the server we're connected to.
func (connection *Connection) serveHandler() bool {
	// Read request
	connection.setStatus <- IDLE

	_, jsonRequest, err := connection.ws.ReadMessage()
	if err != nil {
		if !connection.pool.shutdown {
			connection.pool.client.Errorf("While waiting for a tunnel request: %v", err)
		}

		return false
	}

	connection.setStatus <- RUNNING

	httpRequest := new(mulery.HTTPRequest) // Deserialize request.
	if err := json.Unmarshal(jsonRequest, httpRequest); err != nil {
		connection.error(fmt.Sprintf("Deserializing json tunnel request: %s", err))
		return false
	}

	req := mulery.UnserializeHTTPRequest(httpRequest)
	handler := connection.customHandler

	if connection.pool.client.Config.Handler == nil {
		handler = connection.defaultHandler
		connection.pool.client.Printf("[%s] %s %s", connection.pool.client.Config.ID, req.Method, req.URL.String())
	}

	// Pipe request body.
	_, bodyReader, err := connection.ws.NextReader()
	if err != nil {
		connection.pool.client.Errorf("Getting tunnel response body reader: %v", err)
		return false
	}

	// Create a "fake" body.
	req.Body = io.NopCloser(bodyReader)

	return handler(req)
}

func (connection *Connection) defaultHandler(req *http.Request) bool {
	// This is where a local client sends the server's request off to the Internet.
	resp, err := connection.pool.client.client.Do(req)
	if err != nil {
		return !connection.error(fmt.Sprintf("Executing tunneled request: %v", err))
	}

	bodyWriter := connection.writeResponseHeaders(resp)
	if bodyWriter == nil {
		return false
	}

	if _, err := io.Copy(bodyWriter, resp.Body); err != nil {
		connection.pool.client.Errorf("Getting tunnel pipe response body: %v", err)
		return false
	}

	resp.Body.Close()
	bodyWriter.Close()

	return true
}

func (connection *Connection) writeResponseHeaders(resp *http.Response) io.WriteCloser {
	// Turn the entire http response into a JSON response the server can parse.
	jsonResponse, _ := json.Marshal(mulery.SerializeHTTPResponse(resp)) //nolint:errchkjson // it wont error.

	// This is where we send the Internet's (http request) response back to the server.
	err := connection.ws.WriteMessage(websocket.TextMessage, jsonResponse)
	if err != nil {
		connection.pool.client.Errorf("Writing tunnel response: %v", err)
		return nil
	}

	// Pipe response body because an io.ReadCloser (http.Body) doesn't get serialized (above).
	bodyWriter, err := connection.ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		connection.pool.client.Errorf("Getting tunnel response body writer: %v", err)
		return nil
	}

	return bodyWriter
}

// All calls to this method are in the methods above.
// Returns true if there's an error.
func (connection *Connection) error(msg string) bool {
	resp := mulery.NewHTTPResponse()
	resp.StatusCode = mulery.ClientErrorCode

	connection.pool.client.Errorf(msg)

	resp.ContentLength = int64(len(msg))

	// Serialize response
	jsonResponse, err := json.Marshal(resp)
	if err != nil {
		connection.pool.client.Errorf("Serializing tunnel response: %v", err)
		return true
	}

	// Write response
	err = connection.ws.WriteMessage(websocket.TextMessage, jsonResponse)
	if err != nil {
		connection.pool.client.Errorf("Writing tunnel response: %v", err)
		return true
	}

	// Write response body
	err = connection.ws.WriteMessage(websocket.BinaryMessage, []byte(msg))
	if err != nil {
		connection.pool.client.Errorf("Writing tunnel response body: %v", err)
		return true
	}

	return false
}

// Close the ws/tcp connection.
func (connection *Connection) Close() {
	connection.ws.Close()
	close(connection.setStatus)
	close(connection.getStatus)
}
