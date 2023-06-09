// Package mulch provides shared methods, structures and variables
// used by mulery client library, server library and server application.
package mulch

const SecretKeyHeader = "x-secret-key"

type Handshake struct {
	Size     int    `json:"size"`     // idle connection reaper counter.
	MaxSize  int    `json:"max"`      // buffer pool size.
	ID       string `json:"id"`       // client ID
	Name     string `json:"name"`     // For logs only.
	Compress string `json:"compress"` // gzip, bzip, etc.
}
