// Package mulery provides an application wrapper around the mulery/server module.
// This is full of assumptions, but very configurable.
// Use it as-is, or use it as an example for your own server package.
package mulery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	apachelog "github.com/lestrrat-go/apache-logformat/v2"
	"golift.io/cnfgfile"
	"golift.io/mulery/mulch"
	"golift.io/mulery/server"
)

// Config is the input data to run this app. Read from a config file.
type Config struct {
	ListenAddr string `json:"listenAddr" toml:"listen_addr" yaml:"listenAddr" xml:"listen_addr"`
	AuthURL    string `json:"authUrl" toml:"auth_url" yaml:"authUrl" xml:"auth_url"`
	AuthHeader string `json:"authHeader" toml:"auth_header" yaml:"authHeader" xml:"auth_header"`
	// List of IPs or CIDRs that are allowed to make requests to clients.
	Upstreams  []string `json:"upstreams" toml:"upstreams" yaml:"upstreams" xml:"upstreams"`
	SSLCrtPath string   `json:"sslCrtPath" toml:"ssl_crt_path" yaml:"sslCrtPath" xml:"ssl_crt_path"`
	SSLKeyPath string   `json:"sslKeyPath" toml:"ssl_key_path" yaml:"sslKeyPath" xml:"ssl_key_path"`
	*server.Config
	dispatch *server.Server
	client   *http.Client
	server   *http.Server
	allow    AllowedIPs
}

var ErrInvalidKey = fmt.Errorf("provided key is not authorized")

const keyLen = 36

// LoadConfigFile does what its name implies.
func LoadConfigFile(path string) (*Config, error) {
	config := &Config{
		Config: server.NewConfig(),
		client: &http.Client{},
	}
	config.Config.KeyValidator = config.KeyValidator

	if err := cnfgfile.Unmarshal(config, path); err != nil {
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}

	return config, nil
}

// Start HTTP server.
func (c *Config) Start() {
	c.dispatch = server.NewServer(c.Config)
	c.allow = MakeIPs(c.Upstreams)
	smx := http.NewServeMux()
	apache, _ := apachelog.New(`%h %{X-User-ID}i - %t "%r" %>s %b "%{` + c.IDHeader + `}i" "%{User-agent}i" - %{ms}Tms`)

	smx.Handle("/register", http.HandlerFunc(c.dispatch.HandleRegister)) // apache log can't do websockets.
	smx.Handle("/request/", apache.Wrap(http.StripPrefix("/request",
		c.validateUpstream(http.HandlerFunc(c.dispatch.HandleRequest))), os.Stdout))
	smx.Handle("/status", apache.Wrap(c.validateUpstream(http.HandlerFunc(c.handleStatus)), os.Stdout))
	smx.Handle("/", apache.Wrap(http.HandlerFunc(c.handleAll), os.Stdout))

	// Dispatch connection from available pools to client requests
	// in a separate thread from the server thread.
	go c.dispatch.StartDispatcher()

	c.server = &http.Server{
		Addr:        c.ListenAddr,
		Handler:     smx,
		ReadTimeout: c.Config.Timeout,
	}

	go func() {
		var err error

		if c.SSLCrtPath != "" && c.SSLKeyPath != "" {
			err = c.server.ListenAndServeTLS(c.SSLCrtPath, c.SSLKeyPath)
		} else {
			err = c.server.ListenAndServe()
		}

		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalln("Web server failed, exiting:", err)
		}
	}()
}

func (c *Config) Shutdown() {
	c.dispatch.Shutdown()
}

// KeyValidator validates client secret keys against an nginx auth proxy.
// The actual auth proxy is: http://github.com/Notifiarr/mysql-auth-proxy
func (c *Config) KeyValidator(ctx context.Context, header http.Header) (string, error) {
	key := header.Get(mulch.SecretKeyHeader)
	if key == "" || len(key) != keyLen {
		return "", fmt.Errorf("%w: keyLen: %d!=%d", ErrInvalidKey, len(key), keyLen)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.AuthURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating auth proxy request: %w", err)
	}

	req.Header.Add(c.AuthHeader, key)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connecting to auth proxy: %w", err)
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body) // avoid memory leak

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: status: %s", ErrInvalidKey, resp.Status)
	}

	return key, nil
}
