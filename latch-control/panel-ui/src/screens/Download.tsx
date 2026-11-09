import { useState } from "react";
import { Link } from "react-router-dom";
import { Copy } from "../lib";
import { INSTALL, RELEASES_URL, detectOS, OS_LABEL, OS } from "../download";

export function Download() {
  const [os, setOs] = useState<OS>(detectOS());

  return (
    <div className="space-y-4">
      <div className="card">
        <h2 className="font-medium">Install the agent</h2>
        <p className="text-sm text-gray-500">
          The agent (<code>latchd</code>) runs on the machine whose port you want to expose. It connects out to our
          relay — nothing needs to be open to the internet on your side. We detected <b>{OS_LABEL[os]}</b>.
        </p>
      </div>

      {/* OS switcher */}
      <div className="flex gap-1">
        {(["mac", "linux", "windows"] as OS[]).map((o) => (
          <button key={o} className={os === o ? "btn" : "btn-ghost"} onClick={() => setOs(o)}>
            {OS_LABEL[o]}
          </button>
        ))}
      </div>

      {(os === "mac" || os === "linux" || os === "unknown") && (
        <div className="card space-y-2">
          <h3 className="font-medium">One-line install ({OS_LABEL[os === "unknown" ? "linux" : os]})</h3>
          <div className="flex items-center gap-2">
            <code className="code flex-1 rounded bg-gray-100 p-2 dark:bg-gray-800">{INSTALL.unix}</code>
            <Copy text={INSTALL.unix} />
          </div>
          <p className="text-xs text-gray-500">Auto-detects your architecture, installs <code>latchd</code> to <code>/usr/local/bin</code>, and is safe to re-run.</p>
        </div>
      )}

      {os === "windows" && (
        <div className="card space-y-2">
          <h3 className="font-medium">Windows</h3>
          <p className="text-sm text-gray-500">
            Download <code>latchd_…_windows_amd64.zip</code> from the releases page, unzip it, and run <code>latchd.exe</code> from a terminal.
          </p>
          <a className="btn" href={RELEASES_URL} target="_blank" rel="noopener">Download for Windows ↗</a>
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-2">
        <div className="card space-y-2">
          <h3 className="font-medium">All downloads</h3>
          <p className="text-sm text-gray-500">Signed binaries and checksums for every OS and architecture.</p>
          <a className="btn-ghost" href={RELEASES_URL} target="_blank" rel="noopener">Browse releases ↗</a>
        </div>
        <div className="card space-y-2">
          <h3 className="font-medium">Docker</h3>
          <div className="flex items-center gap-2">
            <code className="code flex-1 rounded bg-gray-100 p-2 dark:bg-gray-800">{INSTALL.docker}</code>
            <Copy text={INSTALL.docker} />
          </div>
        </div>
      </div>

      <div className="card space-y-2">
        <h2 className="font-medium">Connect & expose a port</h2>
        <ol className="list-decimal space-y-1 pl-5 text-sm text-gray-600 dark:text-gray-300">
          <li>Create a tunnel on the <Link to="/tunnels" className="text-brand">Tunnels</Link> tab and copy its connection string.</li>
          <li>On the machine you want to expose, paste it:</li>
        </ol>
        <pre className="block">{`latchd relay add --url 'latch://…(your connection string)…'
latchd route add --target 127.0.0.1:3000 --tunnel <relay-id> --hostname <your-tunnel-host>`}</pre>
        <p className="text-sm text-gray-500">
          Your service is then live at <code>https://&lt;your-tunnel-host&gt;</code> with valid HTTPS. You can also run
          <code> latchd run</code> and manage everything from the agent's own local dashboard at
          <code> http://127.0.0.1:4040</code>.
        </p>
      </div>
    </div>
  );
}
