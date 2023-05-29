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
	"github.com/root-gg/wsp"
)

// Status of a Connection.
const (
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
	pool   *Pool
	ws     *websocket.Conn
	status int
}

// NewConnection create a Connection object.
func NewConnection(pool *Pool) *Connection {
	c := new(Connection)
	c.pool = pool
	c.status = CONNECTING

	return c
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
	greeting := fmt.Sprintf("%s_%d",
		// this is a random ID. For future purposes, it needs to be configurable.
		connection.pool.client.Config.ID,
		connection.pool.client.Config.PoolIdleSize,
	)

	if err := connection.ws.WriteMessage(websocket.TextMessage, []byte(greeting)); err != nil {
		connection.Close()
		return fmt.Errorf("greeting failure: %w", err)
	}

	// We are connected to the server, now start a go routine that waits for incoming server requests.
	go connection.serve(ctx)

	return nil
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

	// Keep connection alive. This go routine may leak.
	go func() {
		ticker := time.NewTicker(keepAliveInterval)
		defer ticker.Stop()

		for tick := range ticker.C {
			err := connection.ws.WriteControl(websocket.PingMessage, []byte{}, tick.Add(keepAliveTimeout))
			if err != nil {
				connection.Close()
			}
		}
	}()

	for {
		// Read request
		connection.status = IDLE

		_, jsonRequest, err := connection.ws.ReadMessage()
		if err != nil {
			log.Println("Unable to read request", err)
			break
		}

		connection.status = RUNNING
		httpRequest := new(wsp.HTTPRequest)

		// Deserialize request
		err = json.Unmarshal(jsonRequest, httpRequest)
		if err != nil {
			connection.error(fmt.Sprintf("Unable to deserialize json http request: %s\n", err))
			break
		}

		req, err := wsp.UnserializeHTTPRequest(httpRequest)
		if err != nil {
			connection.error(fmt.Sprintf("Unable to deserialize http request: %v\n", err))
			break
		}

		log.Printf("[%s] %s", req.Method, req.URL.String())

		// Pipe request body
		_, bodyReader, err := connection.ws.NextReader()
		if err != nil {
			log.Printf("Unable to get response body reader: %v", err)
			break
		}

		// Create a "fake" body.
		req.Body = io.NopCloser(bodyReader)

		// This is where a local client sends the server's request off to the Internet.
		resp, err := connection.pool.client.client.Do(req)
		if err != nil {
			if connection.error(fmt.Sprintf("Unable to execute request: %v\n", err)) {
				break
			}

			continue
		}

		// Turn the entire http response into a JSON response the server can parse.
		jsonResponse, err := json.Marshal(wsp.SerializeHTTPResponse(resp))
		if err != nil {
			if connection.error(fmt.Sprintf("Unable to serialize response: %v\n", err)) {
				break
			}

			continue
		}

		// This is where we send the Internet's (http request) response back to the server.
		err = connection.ws.WriteMessage(websocket.TextMessage, jsonResponse)
		if err != nil {
			log.Printf("Unable to write response: %v", err)
			break
		}

		// Pipe response body because an io.ReadCloser (http.Body) doesn't get serialized (above).
		bodyWriter, err := connection.ws.NextWriter(websocket.BinaryMessage)
		if err != nil {
			log.Printf("Unable to get response body writer: %v", err)
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

// All calls to this method are in the method above.
// Returns true if there's an error.
func (connection *Connection) error(msg string) bool {
	resp := wsp.NewHTTPResponse()
	resp.StatusCode = wsp.ClientErrorCode

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

// Close close the ws/tcp connection and remove it from the pool.
func (connection *Connection) Close() {
	connection.pool.remove(connection)
	connection.ws.Close()
}
