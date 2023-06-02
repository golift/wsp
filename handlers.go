package mulery

import "net/http"

func (c *Config) validateUpstream(next http.Handler) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if !c.allow.Contains(req.RemoteAddr) {
			resp.WriteHeader(http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(resp, req)
	})
}

func (c *Config) handleStatus(resp http.ResponseWriter, _ *http.Request) {
	http.Error(resp, "ok", http.StatusOK)
}

func (c *Config) handleAll(resp http.ResponseWriter, _ *http.Request) {
	http.Error(resp, "", http.StatusUnauthorized)
}
