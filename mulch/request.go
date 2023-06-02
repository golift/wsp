package mulch

import (
	"net/http"
	"net/url"
)

// HTTPRequest is a serializable version of http.Request (with only useful fields).
type HTTPRequest struct {
	Method        string              `json:"method"`
	URL           string              `json:"url"`
	Header        map[string][]string `json:"header"`
	ContentLength int64               `json:"contentLength"`
	RemoteAddr    string              `json:"remoteAddr"`
	Host          string              `json:"host"`
	Proto         string              `json:"proto"`
	RequestURI    string              `json:"requestUri"`
}

// SerializeHTTPRequest create a new HTTPRequest from a http.Request.
func SerializeHTTPRequest(req *http.Request) *HTTPRequest {
	return &HTTPRequest{
		URL:           req.URL.String(),
		Method:        req.Method,
		Header:        req.Header,
		ContentLength: req.ContentLength,
		RemoteAddr:    req.RemoteAddr,
		Host:          req.Host,
		Proto:         req.Proto,
		RequestURI:    req.RequestURI,
	}
}

// UnserializeHTTPRequest create a new http.Request from a HTTPRequest.
func UnserializeHTTPRequest(req *HTTPRequest) *http.Request {
	url, _ := url.Parse(req.URL)

	return &http.Request{
		Method:        req.Method,
		Header:        req.Header,
		ContentLength: req.ContentLength,
		URL:           url,
		RemoteAddr:    req.RemoteAddr,
		Host:          req.Host,
		Proto:         req.Proto,
		RequestURI:    req.RequestURI,
	}
}
