#!/bin/sh
# Acceptance scenarios for the SPEC success criteria. Builds the binaries, runs
# three scenarios against a dev relay/agent, and prints PASS/FAIL. Uses a dev CA
# (certificate verification is never skipped: curl trusts the CA explicitly).
set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$WORK"' EXIT
BIN="$WORK/bin"; mkdir -p "$BIN"
echo "building binaries…"
( cd "$ROOT" && go build -o "$BIN/latch-relay" ./latch-relay && go build -o "$BIN/latchd" ./latchd ) || { echo "build failed"; exit 1; }

RD="$WORK/relay"; LD="$WORK/agent"; mkdir -p "$RD" "$LD"
CA="$RD/devca/ca.pem"
"$BIN/latch-relay" dev-ca init --dir "$RD/devca" --hosts "relay.local,*.relay.local,localhost,127.0.0.1" >/dev/null
"$BIN/latch-relay" run --dev --data-dir "$RD" --public-host 127.0.0.1 --base-domain relay.local \
  --https-addr 127.0.0.1:18443 --http-addr "" --agent-addr 127.0.0.1:17443 >"$WORK/relay.log" 2>&1 &
sleep 1
CONN=$("$BIN/latch-relay" token create --data-dir "$RD" --name laptop --hostnames "*.relay.local" --host 127.0.0.1 --port 17443 --dev | tail -1)
python3 -c "import http.server,socketserver
class H(http.server.BaseHTTPRequestHandler):
 def do_GET(self): self.send_response(200);self.end_headers();self.wfile.write(b'ACCEPTANCE-OK')
 def log_message(self,*a): pass
socketserver.TCPServer(('127.0.0.1',13000),H).serve_forever()" &
sleep 0.5
export LATCHD_API="http://127.0.0.1:14040"
"$BIN/latchd" run --data-dir "$LD" --listen 127.0.0.1:14040 >"$WORK/agent.log" 2>&1 &
sleep 1.5
"$BIN/latchd" relay add --url "$CONN" --ca-file "$CA" >/dev/null
ROUT=$("$BIN/latchd" route add --target 127.0.0.1:13000 --tunnel laptop --hostname app.relay.local)
req(){ curl -s --cacert "$CA" --resolve app.relay.local:18443:127.0.0.1 https://app.relay.local:18443/; }

FAIL=0
# Scenario 1: tunnel — share a local port over HTTPS.
start=$(date +%s)
for i in $(seq 1 30); do [ "$(req)" = "ACCEPTANCE-OK" ] && break; sleep 1; done
elapsed=$(( $(date +%s) - start ))
if [ "$(req)" = "ACCEPTANCE-OK" ] && [ "$elapsed" -lt 120 ]; then
  echo "PASS  scenario 1 (tunnel): HTTPS request reached the local service in ${elapsed}s (< 120s)"
else
  echo "FAIL  scenario 1 (tunnel)"; FAIL=1
fi

# Scenario 2: local mode (embedded Caddy, internal CA) — via the Go e2e test.
if ( cd "$ROOT" && go test ./latchd/internal/local/ -run TestLocalModeEndToEnd -count=1 >"$WORK/local.log" 2>&1 ); then
  echo "PASS  scenario 2 (local mode): embedded Caddy served HTTPS to a local upstream (TestLocalModeEndToEnd)"
else
  echo "FAIL  scenario 2 (local mode)"; FAIL=1
fi

# Scenario 3: agent offline → recovery.
AGENT_PID=$(pgrep -f "latchd run --data-dir $LD" | head -1)
kill "$AGENT_PID" 2>/dev/null
sleep 2
code=$(curl -s -o /dev/null -w '%{http_code}' --cacert "$CA" --resolve app.relay.local:18443:127.0.0.1 https://app.relay.local:18443/)
"$BIN/latchd" run --data-dir "$LD" --listen 127.0.0.1:14040 >"$WORK/agent2.log" 2>&1 &
start=$(date +%s)
for i in $(seq 1 15); do [ "$(req)" = "ACCEPTANCE-OK" ] && break; sleep 1; done
rec=$(( $(date +%s) - start ))
if [ "$code" = "503" ] && [ "$(req)" = "ACCEPTANCE-OK" ] && [ "$rec" -lt 10 ]; then
  echo "PASS  scenario 3 (recovery): offline → 503, restart → tunnel back in ${rec}s (< 10s)"
else
  echo "FAIL  scenario 3 (recovery): offline code=$code recovered in ${rec}s"; FAIL=1
fi

[ "$FAIL" = 0 ] && echo "ALL ACCEPTANCE SCENARIOS PASSED" || echo "SOME SCENARIOS FAILED"
exit $FAIL
