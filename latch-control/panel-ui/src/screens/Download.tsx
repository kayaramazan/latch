import { Link } from "react-router-dom";
import { Copy } from "../lib";

const UNIX = "curl -sSL https://get.latch.dev/install.sh | sh";
const BREW = "brew install latch/tap/latchd";
const WIN = "winget install Latch.Agent";

export function Download() {
  return (
    <div className="space-y-4">
      <div className="card">
        <h2 className="font-medium">Install the agent</h2>
        <p className="text-sm text-gray-500">
          The agent (<code>latchd</code>) runs on the machine whose port you want to expose. It connects out to our
          relay — nothing needs to be open to the internet on your side.
        </p>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <Os title="macOS / Linux" cmd={UNIX} />
        <Os title="macOS (Homebrew)" cmd={BREW} />
        <Os title="Windows" cmd={WIN} />
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

function Os({ title, cmd }: { title: string; cmd: string }) {
  return (
    <div className="card space-y-2">
      <h3 className="font-medium">{title}</h3>
      <div className="flex items-center gap-2">
        <code className="code flex-1 rounded bg-gray-100 p-2 dark:bg-gray-800">{cmd}</code>
        <Copy text={cmd} />
      </div>
    </div>
  );
}
