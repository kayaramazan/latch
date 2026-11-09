// Command loadtest fires concurrent HTTPS requests at a URL (through a Latch
// tunnel or local mode) and reports throughput and latency percentiles. It
// trusts a CA file explicitly; it never skips certificate verification.
//
//	go run ./deploy/loadtest -url https://app.relay.local:18443/ \
//	    -ca /path/ca.pem -resolve app.relay.local:18443:127.0.0.1 -c 50 -d 10s
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "", "target URL")
	caFile := flag.String("ca", "", "PEM CA to trust")
	resolve := flag.String("resolve", "", "host:port:ip override (like curl --resolve)")
	conc := flag.Int("c", 50, "concurrency")
	dur := flag.Duration("d", 10*time.Second, "duration")
	flag.Parse()
	if *url == "" {
		fmt.Fprintln(os.Stderr, "need -url")
		os.Exit(2)
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if *caFile != "" {
		pem, err := os.ReadFile(*caFile)
		if err != nil {
			panic(err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(pem)
		tlsCfg.RootCAs = pool
	}
	tr := &http.Transport{TLSClientConfig: tlsCfg, MaxIdleConnsPerHost: *conc}
	if *resolve != "" {
		parts := strings.SplitN(*resolve, ":", 3)
		hp, ip := parts[0]+":"+parts[1], parts[2]
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr == hp {
				addr = ip + ":" + parts[1]
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
	}
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	var ok, errc atomic.Int64
	var mu sync.Mutex
	var lat []time.Duration
	deadline := time.Now().Add(*dur)
	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				t0 := time.Now()
				resp, err := client.Get(*url)
				d := time.Since(t0)
				if err != nil || resp.StatusCode >= 500 {
					errc.Add(1)
					if resp != nil {
						resp.Body.Close()
					}
					continue
				}
				resp.Body.Close()
				ok.Add(1)
				mu.Lock()
				lat = append(lat, d)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(p/100*float64(len(lat)-1))]
	}
	total := ok.Load() + errc.Load()
	fmt.Printf("requests: %d  ok: %d  errors: %d  rps: %.0f\n", total, ok.Load(), errc.Load(), float64(ok.Load())/dur.Seconds())
	fmt.Printf("latency  p50: %s  p95: %s  p99: %s\n", pct(50), pct(95), pct(99))
}
