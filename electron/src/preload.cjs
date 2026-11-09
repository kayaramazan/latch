// Minimal, safe preload. The UI talks to latchd over HTTP on the same origin;
// the shell exposes nothing privileged.
"use strict";
const { contextBridge } = require("electron");
contextBridge.exposeInMainWorld("latchDesktop", { isDesktop: true });
