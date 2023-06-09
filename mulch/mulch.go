// Package mulch provides shared methods, structures and variables
// used by mulery client library, server library and server application.
package mulch

const SecretKeyHeader = "x-secret-key"

type Handshake struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int    `json:"size"`
	MaxSize  int    `json:"max"`
	Compress bool   `json:"compress"`
}
