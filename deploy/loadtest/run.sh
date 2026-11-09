#!/bin/sh
# Stands up a dev relay + agent + local server, then runs the loadtest tool
# through the tunnel. Dev CA only; certificate verification is never skipped.
set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
W="$(mktemp -d)"; trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$W"' EXIT
( cd "$ROOT" && go build -o "$W/relay" ./latch-relay && go build -o "$W/latchd" ./latchd && go build -o "$W/loadtest" ./deploy/loadtest ) || exit 1
RD="$W/rd"; LD="$W/ld"; mkdir -p "$RD" "$LD"; CA="$RD/devca/ca.pem"
"$W/relay" dev-ca init --dir "$RD/devca" --hosts "relay.local,*.relay.local,localhost,127.0.0.1" >/dev/null
"$W/relay" run --dev --data-dir "$RD" --public-host 127.0.0.1 --base-domain relay.local --https-addr 127.0.0.1:19443 --http-addr "" --agent-addr 127.0.0.1:19743 >"$W/r.log" 2>&1 &
sleep 1
CONN=$("$W/relay" token create --data-dir "$RD" --name lt --hostnames "*.relay.local" --host 127.0.0.1 --port 19743 --dev | tail -1)
python3 -c "import http.server,socketserver
class H(http.server.BaseHTTPRequestHandler):
 def do_GET(self): self.send_response(200);self.end_headers();self.wfile.write(b'ok')
 def log_message(self,*a): pass
socketserver.TCPServer(('127.0.0.1',19000),H).serve_forever()" &
sleep 0.5
export LATCHD_API="http://127.0.0.1:19040"
"$W/latchd" run --data-dir "$LD" --listen 127.0.0.1:19040 >"$W/l.log" 2>&1 &
sleep 1.5
"$W/latchd" relay add --url "$CONN" --ca-file "$CA" >/dev/null
"$W/latchd" route add --target 127.0.0.1:19000 --tunnel lt --hostname app.relay.local >/dev/null
# Wait until the tunnel actually serves 200 before measuring.
for i in $(seq 1 20); do [ "$(curl -s -o /dev/null -w '%{http_code}' --cacert "$CA" --resolve app.relay.local:19443:127.0.0.1 https://app.relay.local:19443/)" = "200" ] && break; sleep 1; done
"$W/loadtest" -url https://app.relay.local:19443/ -ca "$CA" -resolve app.relay.local:19443:127.0.0.1 -c "${1:-50}" -d "${2:-10s}"
