package tunnel

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// pipe copies bytes in both directions between the tunnel stream and the
// local connection until either side closes, then shuts both down. It returns
// the number of bytes read from the local target (toRelay) and written to it
// (fromRelay). A half-close on one direction tears down the other so neither
// goroutine lingers.
func pipe(stream, local net.Conn, fromRelay, toRelay *atomic.Int64) {
	var wg sync.WaitGroup
	wg.Add(2)
	// relay -> local (request body / client bytes)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(local, stream)
		fromRelay.Add(n)
		closeWrite(local)
		_ = stream.Close()
	}()
	// local -> relay (response / server bytes)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(stream, local)
		toRelay.Add(n)
		closeWrite(stream)
		_ = local.Close()
	}()
	wg.Wait()
}

// closeWrite half-closes the write side when the type supports it, so the peer
// sees EOF without the read side being torn down early.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}
