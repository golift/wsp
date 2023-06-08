package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"golift.io/mulery/mulch"
)

// ProxyError log error and return a HTTP 526 error with the message.
func (s *Server) ProxyError(resp http.ResponseWriter, err error, regFail string) {
	if regFail != "" && s.metrics != nil {
		s.metrics.Regs.WithLabelValues(regFail).Add(1)
	}

	s.Config.Logger.Errorf("%v", err)
	http.Error(resp, err.Error(), mulch.ProxyErrorCode)
}

func (s *Server) HandleStats(resp http.ResponseWriter, req *http.Request) {
	s.getStats <- clientID(req.Header.Get(s.Config.IDHeader)) // ask for stats.
	if err := json.NewEncoder(resp).Encode(<-s.repStats); err != nil {
		http.Error(resp, err.Error(), http.StatusInternalServerError)
	}
}

// HandleRequest receives http requests for /request paths.
func (s *Server) HandleRequest(name string) http.Handler {
	if name == "" {
		name = "request"
	}

	return s.metrics.Wrap(func(resp http.ResponseWriter, req *http.Request) {
		// Receive requests to be proxied; parse destination URL if it exists (otherwise use the incoming url).
		if dstURL := req.Header.Get("X-PROXY-DESTINATION"); dstURL != "" {
			var err error
			// r.URL is used in proxyRequest().
			if req.URL, err = url.Parse(dstURL); err != nil {
				s.ProxyError(resp, fmt.Errorf("parsing X-PROXY-DESTINATION header: %w", err), "")
				return
			}
		}

		if len(s.pools) == 0 {
			s.ProxyError(resp, fmt.Errorf("%w: no pools registered", ErrNoProxyTarget), "")
			return
		}

		clientID, err := s.getClientID(req)
		if err != nil {
			s.ProxyError(resp, err, "")
			return
		}

		request := &dispatchRequest{
			connection: make(chan *Connection), // do not close this here.
			client:     clientID,
		}

		// "Dispatcher" is running in a separate thread from the server by `go s.DispatchConnections()`.
		// It waits to receive requests to dispatch connections from available pools to http-clients' requests.
		// https://github.com/hgsgtk/wsp/blob/ea4902a8e11f820268e52a6245092728efeffd7f/server/server.go#L93
		s.dispatcher <- request
		// Dispatcher tries to find an available connection pool,
		// and it returns the connection through Server.connection channel.
		// https://github.com/hgsgtk/wsp/blob/ea4902a8e11f820268e52a6245092728efeffd7f/server/server.go#L189
		// Wait briefly for the dispatcher to return a websocket connection.
		connection := <-request.connection
		if connection == nil {
			// Dispatcher is `nil` which means the target has no pool.
			s.ProxyError(resp, fmt.Errorf("%w: %s", ErrNoProxyTarget, request.client), "")
			return
		}

		// Send the incoming http request to the peer through the WebSocket connection.
		if err := connection.proxyRequest(resp, req); err != nil {
			// An error occurred throw the connection away.
			connection.Close("proxy error")
			// Try to return an error to the client.
			// This might fail if response headers have already been sent.
			s.ProxyError(resp, fmt.Errorf("tunneling failure, connection closed: %w", err), "")
		}
	}, name)
}

// HandleRegister receives http requests for /register paths.
// Receives the WebSocket upgrade handshake request from clients.
func (s *Server) HandleRegister() http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		// 0. Validate the provided secret key.
		secret, err := s.validateKey(req.Context(), req.Header)
		if err != nil {
			s.ProxyError(resp, err, "keyFailed")
			return
		}

		// 1. Upgrade a received HTTP request to a WebSocket connection.
		sock, err := s.upgrader.Upgrade(resp, req, nil)
		if err != nil {
			s.ProxyError(resp, fmt.Errorf("http upgrade failed: %w", err), "upgradeFailed")
			return
		}

		// 2. Wait for a greeting message from the peer and parse it.
		// The first message should contain the remote Proxy name and pool size.
		poolConfig, err := parseGreeting(sock)
		if err != nil {
			s.ProxyError(resp, err, "greetingFailed")
			sock.Close()
			return
		}

		// 3. Register the connection into server pools.
		poolConfig.secret = secret
		s.newPool <- poolConfig

		if s.metrics != nil {
			s.metrics.Regs.WithLabelValues("success").Add(1)
		}
	})
}

func (s *Server) getClientID(req *http.Request) (clientID, error) {
	target := clientID("")

	if s.Config.IDHeader != "" {
		target = clientID(req.Header.Get(s.Config.IDHeader))
		if target == "" {
			return "", fmt.Errorf("%w: %s", ErrNoClientID, s.Config.IDHeader)
		}
	}

	return target, nil // target may be empty.
}

// 0. Validate the provided secret key.
func (s *Server) validateKey(ctx context.Context, header http.Header) (string, error) {
	// If a custom key validator is provided, run that.
	if s.Config.KeyValidator != nil {
		secret, err := s.Config.KeyValidator(ctx, header)
		if err != nil {
			return "", fmt.Errorf("custom key validation failed: %w", err)
		}

		return secret, nil
	}

	// Otherwise run the default validator.
	secretKey := header.Get(mulch.SecretKeyHeader)
	if secretKey != s.Config.SecretKey {
		return "", ErrInvalidKey
	}

	// Do not return the "configured" secret key.
	return "", nil
}

// 2. Wait for a greeting message from the peer and parse it.
func parseGreeting(sock *websocket.Conn) (*PoolConfig, error) {
	_, greeting, err := sock.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("unable to read greeting message: %w", err)
	}

	// Parse the greeting message.
	split := strings.Split(string(greeting), "_")
	if len(split) != 3 { //nolint:gomnd
		return nil, fmt.Errorf("%w: greeting separator count is wrong", ErrInvalidData)
	}

	size, err := strconv.Atoi(split[1])
	if err != nil {
		return nil, fmt.Errorf("unable to parse greeting message: %w", err)
	}

	max, err := strconv.Atoi(split[2])
	if err != nil {
		return nil, fmt.Errorf("unable to parse greeting message: %w", err)
	}

	return &PoolConfig{size, max, clientID(split[0]), "", sock}, nil
}
