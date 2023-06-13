// Package mulch provides shared methods, structures and variables
// used by mulery client library, server library and server application.
package mulch

import "time"

const SecretKeyHeader = "x-secret-key"

type Handshake struct {
	Size     int    `json:"size"`     // idle connections.
	MaxSize  int    `json:"max"`      // buffer pool size.
	ID       string `json:"id"`       // client ID
	Name     string `json:"name"`     // For logs only.
	Compress string `json:"compress"` // gzip, bzip, etc, not used yet.
	// ClientIDs is for you to identify your clients with your own ID(s).
	ClientIDs []interface{} `json:"clientIds"`
}

const HandshakeTimeout = 15 * time.Second
