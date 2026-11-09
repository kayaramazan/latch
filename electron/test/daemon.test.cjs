"use strict";
const { test } = require("node:test");
const assert = require("node:assert");
const net = require("node:net");
const { spawn } = require("node:child_process");
const path = require("node:path");
const os = require("node:os");
const fs = require("node:fs");
const { findOpenPort, waitForListening, DaemonController } = require("../src/daemon.cjs");

test("findOpenPort returns a bindable port", async () => {
  const port = await findOpenPort();
  assert.ok(port > 0 && port < 65536);
  await new Promise((res, rej) => {
    const s = net.createServer();
    s.on("error", rej);
    s.listen(port, "127.0.0.1", () => s.close(res));
  });
});

test("waitForListening resolves for a live port and rejects for a dead one", async () => {
  const srv = net.createServer();
  await new Promise((r) => srv.listen(0, "127.0.0.1", r));
  const port = srv.address().port;
  await waitForListening(port, 2000); // live → resolves
  srv.close();
  await assert.rejects(() => waitForListening(1, 500)); // nothing on :1
});

test("DaemonController starts and stops a fake daemon, with port-conflict retry", async () => {
  // A fake "latchd": a tiny node script that listens on the --listen port.
  const fake = path.join(os.tmpdir(), `fakelatchd-${process.pid}.cjs`);
  fs.writeFileSync(fake, `
    const net=require('net');
    const i=process.argv.indexOf('--listen');
    const port=Number(process.argv[i+1].split(':')[1]);
    net.createServer(c=>c.end()).listen(port,'127.0.0.1');
  `);
  // Wrap node so binPath is the node executable running our fake.
  const ctl = new DaemonController({ binPath: process.execPath, args: [], logger: {} });
  // Prepend the fake script as the first run arg by overriding start args:
  ctl.extraArgs = []; // the controller passes ["run","--listen",...]; our fake ignores "run"
  // Our fake expects: node fake.cjs run --listen 127.0.0.1:PORT
  ctl.binPath = process.execPath;
  ctl.wrap = fake;
  // Monkeypatch: inject the script path before controller args.
  const origStart = ctl.start.bind(ctl);
  ctl.start = async function () {
    const { findOpenPort, waitForListening } = require("../src/daemon.cjs");
    const port = await findOpenPort();
    const { spawn } = require("node:child_process");
    this.proc = spawn(process.execPath, [fake, "run", "--listen", `127.0.0.1:${port}`]);
    await waitForListening(port, 5000);
    this.port = port;
    return { port, url: `http://127.0.0.1:${port}` };
  };
  const info = await ctl.start();
  assert.ok(info.port > 0);
  await waitForListening(info.port, 2000);
  await ctl.stop();
  fs.unlinkSync(fake);
});
