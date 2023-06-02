package mulery

import (
	"encoding/json"
	"log"
	"net/http"
)

// HTTPResponse is a serializable version of http.Response (with only useful fields).
type HTTPResponse struct {
	StatusCode    int         `json:"statusCode"`
	Header        http.Header `json:"header"`
	ContentLength int64       `json:"contentLength"`
}

// Custom HTTP error codes shared by client and server.
const (
	ProxyErrorCode  = 526
	ClientErrorCode = 527
)

// SerializeHTTPResponse create a new HTTPResponse json blob from a http.Response.
func SerializeHTTPResponse(resp *http.Response) []byte {
	jsonResponse, _ := json.Marshal(&HTTPResponse{ //nolint:errchkjson // it wont error.
		StatusCode:    resp.StatusCode,
		Header:        resp.Header,
		ContentLength: resp.ContentLength,
	})

	return jsonResponse
}

// NewHTTPResponse creates a new HTTPResponse.
func NewHTTPResponse(code int, size int64) []byte {
	jsonResponse, _ := json.Marshal(&HTTPResponse{ //nolint:errchkjson // it wont error.
		Header:        make(http.Header),
		StatusCode:    code,
		ContentLength: size,
	})

	return jsonResponse
}

// ProxyError log error and return a HTTP 526 error with the message.
func ProxyError(w http.ResponseWriter, err error) {
	log.Println(err)
	http.Error(w, err.Error(), ProxyErrorCode)
}
