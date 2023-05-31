package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	keepAliveInterval = 30 * time.Second
	keepAliveTimeout  = 2 * time.Second
)

// Connection handle a single websocket (HTTP/TCP) connection to an Server.
type Connection struct {
	pool      *Pool
	ws        *websocket.Conn
	status    int
	setStatus chan int
	getStatus chan int
}

// NewConnection create a Connection object.
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
	log.Printf("Connecting to %s", connection.pool.target)

	var err error
	// Create a new TCP(/TLS) connection (no use of net.http)
	//nolint:bodyclose // Gets closed in the Close() method.
	connection.ws, _, err = connection.pool.client.dialer.DialContext(
		ctx,
		connection.pool.target,
		http.Header{"X-SECRET-KEY": {connection.pool.secretKey}},
	)
	if err != nil {
		return fmt.Errorf("tcp dialer failure: %w", err)
	}

	log.Printf("Connected to %s", connection.pool.target)

	// Send the greeting message with proxy id and wanted pool size.
	greeting := fmt.Sprintf("%s_%d_%d",
		connection.pool.client.Config.ID,
		connection.pool.client.Config.PoolIdleSize,
		connection.pool.client.Config.PoolMaxSize,
	)

	if err := connection.ws.WriteMessage(websocket.TextMessage, []byte(greeting)); err != nil {
		connection.remove()
		return fmt.Errorf("greeting failure: %w", err)
	}

	// We are connected to the server, now start a go routine that waits for incoming server requests.
	go connection.serve(ctx)
	go connection.keepAlive(ctx)

	return nil
}

// Keep connection alive.
func (connection *Connection) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case tick := <-ticker.C:
			err := connection.ws.WriteControl(websocket.PingMessage, []byte{}, tick.Add(keepAliveTimeout))
			if err != nil {
				log.Printf("Keep-alive failure: %v", err)
				connection.remove()

				return
			}
		case <-ctx.Done():
			return
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
func (connection *Connection) serve(ctx context.Context) {
	defer connection.Close()

	for {
		// Read request
		connection.setStatus <- IDLE

		_, jsonRequest, err := connection.ws.ReadMessage()
		if err != nil {
			connection.setStatus <- RUNNING
			log.Println("Unable to read request:", err)

			break
		}

		connection.setStatus <- RUNNING
		httpRequest := new(mulery.HTTPRequest)

		// Deserialize request
		err = json.Unmarshal(jsonRequest, httpRequest)
		if err != nil {
			connection.error(fmt.Sprintf("Unable to deserialize json http request: %s", err))
			break
		}

		req, err := mulery.UnserializeHTTPRequest(httpRequest)
		if err != nil {
			connection.error(fmt.Sprintf("Unable to deserialize http request: %v", err))
			break
		}

		log.Printf("[%s] %s %s", connection.pool.client.Config.ID, req.Method, req.URL.String())

		// Pipe request body
		_, bodyReader, err := connection.ws.NextReader()
		if err != nil {
			log.Printf("Unable to get response body reader: %v", err)
			break
		}

		// Create a "fake" body.
		req.Body = io.NopCloser(bodyReader)

		if do := connection.pool.client.Config.Handler; do != nil {
			if ok := connection.runMiddleware(req); !ok {
				break
			}

			continue
		}
		// This is where a local client sends the server's request off to the Internet.
		resp, err := connection.pool.client.client.Do(req)
		if err != nil {
			if connection.error(fmt.Sprintf("Unable to execute request: %v", err)) {
				break
			}

			continue
		}

		bodyWriter := connection.writeResponseHeaders(resp)
		if bodyWriter == nil {
			break
		}

		_, err = io.Copy(bodyWriter, resp.Body)
		if err != nil {
			log.Printf("Unable to get pipe response body: %v", err)
			break
		}

		resp.Body.Close()
		bodyWriter.Close()
	}
}

func (connection *Connection) writeResponseHeaders(resp *http.Response) io.WriteCloser {
	// Turn the entire http response into a JSON response the server can parse.
	jsonResponse, _ := json.Marshal(mulery.SerializeHTTPResponse(resp))

	// This is where we send the Internet's (http request) response back to the server.
	err := connection.ws.WriteMessage(websocket.TextMessage, jsonResponse)
	if err != nil {
		log.Printf("Unable to write response: %v", err)
		return nil
	}

	// Pipe response body because an io.ReadCloser (http.Body) doesn't get serialized (above).
	bodyWriter, err := connection.ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		log.Printf("Unable to get response body writer: %v", err)
		return nil
	}

	return bodyWriter
}

// All calls to this method are in the two methods above.
// Returns true if there's an error.
func (connection *Connection) error(msg string) bool {
	resp := mulery.NewHTTPResponse()
	resp.StatusCode = mulery.ClientErrorCode

	log.Println(msg)

	resp.ContentLength = int64(len(msg))

	// Serialize response
	jsonResponse, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Unable to serialize response: %v", err)
		return true
	}

	// Write response
	err = connection.ws.WriteMessage(websocket.TextMessage, jsonResponse)
	if err != nil {
		log.Printf("Unable to write response: %v", err)
		return true
	}

	// Write response body
	err = connection.ws.WriteMessage(websocket.BinaryMessage, []byte(msg))
	if err != nil {
		log.Printf("Unable to write response body: %v", err)
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

// remove and close the ws/tcp connection from the pool.
func (connection *Connection) remove() {
	connection.Close()
	connection.pool.Remove(connection)
}
