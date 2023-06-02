package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const defaultPort = 8080

// Config configures a Server.
type Config struct {
	Host        string        `json:"host" toml:"host" yaml:"host" xml:"host"`
	Port        int           `json:"port" toml:"port" yaml:"port" xml:"port"`
	Timeout     time.Duration `json:"timeout" toml:"timeout" yaml:"timeout" xml:"timeout"`
	IdleTimeout time.Duration `json:"idleTimeout" toml:"idle_timeout" yaml:"idletimeout" xml:"idle_timeout"`
	SecretKey   string        `json:"secretKey" toml:"secret_key" yaml:"secretkey" xml:"secret_key"`
	SSLCrtPath  string        `json:"sslCrtPath" toml:"ssl_crt_pah" yaml:"sslCrtPath" xml:"ssl_crt_path"`
	SSLKeyPath  string        `json:"sslKeyPath" toml:"ssl_key_path" yaml:"sslKeyPath" xml:"ssl_key_path"`
	// IDHeader sets the upstream header to parse for a remote client.
	// Default behavior is to send requests to clients randomly.
	// If this value is set, reququests can only be directed to clients with this header.
	IDHeader string `json:"idHeader" toml:"id_header" yaml:"idHeader" xml:"id_header"`
	// List of IPs or CIDRs that are allowed to make requests to clients.
	Upstreams []string `json:"upstreams" toml:"upstreams" yaml:"upstreams" xml:"upstreams"`
	// If a KeyValidator method is provided, then Secretkey is ignored.
	// If the validator returns a string then all pool IDs become a
	// sha256 of that string and the client's generated or provided id.
	// See the NewPool function to see that in action.
	// This allows you to let clients provide their own ID, but a secure
	// access-ID is created with your provided seed to prevent hash collisions.
	KeyValidator func(context.Context, http.Header) (string, error) `json:"-" toml:"-" yaml:"-" xml:"-"`
}

// NewConfig creates a new ProxyConfig.
func NewConfig() *Config {
	return &Config{
		Host:        "127.0.0.1",
		Port:        defaultPort,
		Timeout:     time.Second,
		IdleTimeout: time.Minute + time.Second,
	}
}

// AllowedIPs determines who make requests.
type AllowedIPs struct {
	Input []string
	Nets  []*net.IPNet
}

var _ = fmt.Stringer(AllowedIPs{})

// String turns a list of allowedIPs into a printable masterpiece.
func (n AllowedIPs) String() string {
	if len(n.Nets) < 1 {
		return "(none)"
	}

	s := ""

	for i := range n.Nets {
		if s != "" {
			s += ", "
		}

		s += n.Nets[i].String()
	}

	return s
}

// Contains returns true if an IP is allowed.
func (n AllowedIPs) Contains(ip string) bool {
	ip = strings.Trim(ip[:strings.LastIndex(ip, ":")], "[]")

	for i := range n.Nets {
		if n.Nets[i].Contains(net.ParseIP(ip)) {
			return true
		}
	}

	return false
}

// MakeIPs turns a list of CIDR strings (or plain IPs) into a list of net.IPNet.
// This "allowed" list is later used to check incoming IPs from web requests.
func MakeIPs(upstreams []string) AllowedIPs {
	a := AllowedIPs{
		Input: make([]string, len(upstreams)),
		Nets:  []*net.IPNet{},
	}

	for idx, ipAddr := range upstreams {
		a.Input[idx] = ipAddr

		if !strings.Contains(ipAddr, "/") {
			if strings.Contains(ipAddr, ":") {
				ipAddr += "/128"
			} else {
				ipAddr += "/32"
			}
		}

		if _, i, err := net.ParseCIDR(ipAddr); err == nil {
			a.Nets = append(a.Nets, i)
		}
	}

	return a
}

func (s *Server) validateUpstream(next http.Handler) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if !s.allow.Contains(req.RemoteAddr) {
			resp.WriteHeader(http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(resp, req)
	})
}
