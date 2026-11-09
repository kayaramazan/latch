// Daemon lifecycle for the desktop shell: find a free port, spawn the bundled
// latchd binary, wait until its API is listening, and stop it cleanly. This is
// the only "logic" in the shell and it is pure process management — the daemon
// (latchd) owns all product behaviour.
"use strict";
const { spawn } = require("node:child_process");
const net = require("node:net");

function findOpenPort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

function waitForListening(port, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve, reject) => {
    const attempt = () => {
      const sock = net.connect(port, "127.0.0.1");
      sock.on("connect", () => { sock.destroy(); resolve(); });
      sock.on("error", () => {
        sock.destroy();
        if (Date.now() > deadline) reject(new Error(`timed out waiting for 127.0.0.1:${port}`));
        else setTimeout(attempt, 150);
      });
    };
    attempt();
  });
}

class DaemonController {
  constructor({ binPath, dataDir, args = [], retries = 3, logger = console } = {}) {
    this.binPath = binPath;
    this.dataDir = dataDir;
    this.extraArgs = args;
    this.retries = retries;
    this.log = logger;
    this.proc = null;
    this.port = null;
  }

  async start() {
    let lastErr;
    for (let i = 0; i < this.retries; i++) {
      const port = await findOpenPort();
      const args = ["run", "--listen", `127.0.0.1:${port}`];
      if (this.dataDir) args.push("--data-dir", this.dataDir);
      args.push(...this.extraArgs);
      const proc = spawn(this.binPath, args, { stdio: ["ignore", "pipe", "pipe"] });
      proc.stdout.on("data", (d) => this.log.info && this.log.info(`[latchd] ${String(d).trim()}`));
      proc.stderr.on("data", (d) => this.log.info && this.log.info(`[latchd] ${String(d).trim()}`));
      try {
        await waitForListening(port, 10000);
        this.proc = proc;
        this.port = port;
        return { port, url: `http://127.0.0.1:${port}` };
      } catch (e) {
        lastErr = e;
        proc.kill();
      }
    }
    throw lastErr || new Error("could not start latchd");
  }

  async stop() {
    if (!this.proc) return;
    const p = this.proc;
    this.proc = null;
    return new Promise((resolve) => {
      p.once("exit", () => resolve());
      p.kill("SIGTERM");
      setTimeout(() => { try { p.kill("SIGKILL"); } catch (e) {} resolve(); }, 3000);
    });
  }
}

module.exports = { findOpenPort, waitForListening, DaemonController };
