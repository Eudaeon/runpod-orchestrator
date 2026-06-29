// Package tunnel exposes a local TCP port on a publicly reachable address so a
// RunPod pod can dial back to the orchestrator's reverse-ssh relay.
package tunnel

import "fmt"

// Tunnel is a live public endpoint forwarding to a local port. Close tears it
// down (and stops any helper process backing it).
type Tunnel struct {
	Host    string
	Port    int
	closeFn func() error
}

// Endpoint returns the public "host:port" the pod should dial.
func (t *Tunnel) Endpoint() string {
	return fmt.Sprintf("%s:%d", t.Host, t.Port)
}

// Close tears down the tunnel.
func (t *Tunnel) Close() error {
	if t == nil || t.closeFn == nil {
		return nil
	}
	return t.closeFn()
}
