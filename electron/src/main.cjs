// Latch desktop main process. It manages the bundled latchd daemon and shows
// its web UI in a window with a tray menu. No product logic lives here — latchd
// owns everything; this shell only starts/stops it and renders the UI.
"use strict";
const { app, BrowserWindow, Tray, Menu, shell, nativeImage } = require("electron");
const path = require("node:path");
const fs = require("node:fs");
const os = require("node:os");
const { autoUpdater } = require("electron-updater");
const { DaemonController } = require("./daemon.cjs");

const DATA_DIR = path.join(app.getPath("userData"), "data");
const LOG_FILE = path.join(app.getPath("logs"), "latchd.log");

function daemonBinary() {
  const name = process.platform === "win32" ? "latchd.exe" : "latchd";
  // Packaged: resources/bin/latchd; dev: ../resources/bin/latchd.
  const packaged = path.join(process.resourcesPath || "", "bin", name);
  if (fs.existsSync(packaged)) return packaged;
  return path.join(__dirname, "..", "resources", "bin", name);
}

let win = null;
let tray = null;
const daemon = new DaemonController({
  binPath: daemonBinary(),
  dataDir: DATA_DIR,
  args: ["--log-file", LOG_FILE],
  logger: { info: (m) => console.log(m) },
});

function createWindow(url) {
  win = new BrowserWindow({
    width: 1100, height: 760, show: true,
    webPreferences: { preload: path.join(__dirname, "preload.cjs"), contextIsolation: true },
    title: "Latch",
  });
  win.loadURL(url);
  win.on("close", (e) => {
    // Keep running in the tray instead of quitting.
    if (!app.isQuitting) { e.preventDefault(); win.hide(); }
  });
}

function buildTray(url) {
  const img = nativeImage.createEmpty();
  tray = new Tray(img);
  const menu = Menu.buildFromTemplate([
    { label: `Latch — ${url}`, enabled: false },
    { type: "separator" },
    { label: "Open", click: () => { if (win) { win.show(); } else { createWindow(url); } } },
    { label: "Open logs", click: () => shell.openPath(LOG_FILE) },
    { type: "separator" },
    { label: "Quit", click: () => { app.isQuitting = true; app.quit(); } },
  ]);
  tray.setToolTip("Latch");
  tray.setContextMenu(menu);
}

app.on("ready", async () => {
  fs.mkdirSync(DATA_DIR, { recursive: true });
  fs.mkdirSync(path.dirname(LOG_FILE), { recursive: true });
  try {
    const { url } = await daemon.start();
    createWindow(url);
    buildTray(url);
    if (app.isPackaged) autoUpdater.checkForUpdatesAndNotify().catch(() => {});
  } catch (e) {
    const { dialog } = require("electron");
    dialog.showErrorBox("Latch could not start", String(e && e.message || e) + "\n\nLogs: " + LOG_FILE);
    app.quit();
  }
});

app.on("before-quit", async (e) => {
  if (daemon.proc) {
    e.preventDefault();
    await daemon.stop();
    app.isQuitting = true;
    app.exit(0);
  }
});

app.on("window-all-closed", () => { /* stay alive in the tray */ });

// Avoid the unused import warning while keeping os available for future use.
void os;
