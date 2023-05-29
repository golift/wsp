package wsp

import (
	"fmt"
	"log"
	"net/http"
)

// HTTPResponse is a serializable version of http.Response (with only useful fields).
type HTTPResponse struct {
	StatusCode    int         `json:"statusCode"`
	Header        http.Header `json:"header"`
	ContentLength int64       `json:"contentLength"`
}

const ProxyErrorCode = 526

// SerializeHTTPResponse create a new HTTPResponse from a http.Response.
func SerializeHTTPResponse(resp *http.Response) *HTTPResponse {
	return &HTTPResponse{
		StatusCode:    resp.StatusCode,
		Header:        resp.Header,
		ContentLength: resp.ContentLength,
	}
}

// NewHTTPResponse creates a new HTTPResponse.
func NewHTTPResponse() *HTTPResponse {
	return &HTTPResponse{
		Header: http.Header{},
	}
}

// ProxyError log error and return a HTTP 526 error with the message.
func ProxyError(w http.ResponseWriter, err error) {
	log.Println(err)
	http.Error(w, err.Error(), ProxyErrorCode)
}

// ProxyErrorf log error and return a HTTP 526 error with the message.
func ProxyErrorf(w http.ResponseWriter, format string, args ...interface{}) {
	ProxyError(w, fmt.Errorf(format, args...))
}
