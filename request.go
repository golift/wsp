package mulery

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
)

// HTTPRequest is a serializable version of http.Request (with only useful fields).
type HTTPRequest struct {
	Method        string              `json:"method"`
	URL           string              `json:"url"`
	Header        map[string][]string `json:"header"`
	ContentLength int64               `json:"contentLength"`
}

// Rule match HTTP requests to allow / deny access.
type Rule struct {
	Method       string
	URL          string
	Headers      map[string]string
	methodRegex  *regexp.Regexp
	urlRegex     *regexp.Regexp
	headersRegex map[string]*regexp.Regexp
}

// SerializeHTTPRequest create a new HTTPRequest from a http.Request.
func SerializeHTTPRequest(req *http.Request) *HTTPRequest {
	return &HTTPRequest{
		URL:           req.URL.String(),
		Method:        req.Method,
		Header:        req.Header,
		ContentLength: req.ContentLength,
	}
}

// UnserializeHTTPRequest create a new http.Request from a HTTPRequest.
func UnserializeHTTPRequest(req *HTTPRequest) (*http.Request, error) {
	url, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing URL failed: %w", err)
	}

	return &http.Request{
		Method:        req.Method,
		Header:        req.Header,
		ContentLength: req.ContentLength,
		URL:           url,
	}, nil
}

// NewRule creates a new Rule.
func NewRule(method string, url string, headers map[string]string) (*Rule, error) {
	rule := new(Rule)
	rule.Method = method
	rule.URL = url

	if headers != nil {
		rule.Headers = headers
	} else {
		rule.Headers = make(map[string]string)
	}

	return rule, rule.Compile()
}

// Compile the regular expressions.
func (rule *Rule) Compile() error {
	var err error

	if rule.Method != "" {
		rule.methodRegex, err = regexp.Compile(rule.Method)
		if err != nil {
			return fmt.Errorf("parsing regex failed: %w", err)
		}
	}

	if rule.URL != "" {
		rule.urlRegex, err = regexp.Compile(rule.URL)
		if err != nil {
			return fmt.Errorf("parsing regex failed: %w", err)
		}
	}

	rule.headersRegex = make(map[string]*regexp.Regexp)

	for header, regexStr := range rule.Headers {
		regex, err := regexp.Compile(regexStr)
		if err != nil {
			return fmt.Errorf("parsing regex failed: %w", err)
		}

		rule.headersRegex[header] = regex
	}

	return nil
}

// Match returns true if the http.Request matches the Rule.
func (rule *Rule) Match(req *http.Request) bool {
	if rule.methodRegex != nil && !rule.methodRegex.MatchString(req.Method) {
		return false
	}

	if rule.urlRegex != nil && !rule.urlRegex.MatchString(req.URL.String()) {
		return false
	}

	for headerName, regex := range rule.headersRegex {
		if !regex.MatchString(req.Header.Get(headerName)) {
			return false
		}
	}

	return true
}

func (rule *Rule) String() string {
	return fmt.Sprintf("%s %s %v", rule.Method, rule.URL, rule.Headers)
}
