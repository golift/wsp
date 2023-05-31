package client

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

var ErrNilBody = fmt.Errorf("got Write before WriteHeaders, body is nil")

func (connection *Connection) runMiddleware(req *http.Request) bool {
	writer := &req2Handler{
		req:  req,
		resp: &http.Response{},
		conn: connection,
	}

	connection.pool.client.Config.Handler(writer, req)
	writer.resp.Body.Close()
	writer.body.Close()

	return writer.err
}

type req2Handler struct {
	req  *http.Request
	resp *http.Response
	conn *Connection
	body io.WriteCloser
	mu   sync.Mutex
	err  bool
}

func (r *req2Handler) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.body == nil {
		r.err = true
		return 0, ErrNilBody
	}

	size, err := r.body.Write(data)
	if err != nil {
		r.err = true
	}

	r.resp.ContentLength += int64(size)

	return size, err
}

func (r *req2Handler) WriteHeader(statusCode int) {
	r.resp.StatusCode = statusCode
	r.resp.Status = http.StatusText(statusCode)
	r.body = r.conn.writeResponseHeaders(r.resp)
}

func (r *req2Handler) Header() http.Header {
	return r.resp.Header
}

var _ = http.ResponseWriter(&req2Handler{})
