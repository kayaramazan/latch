// Single source of truth for where the agent is distributed. Everything is
// published to GitHub Releases by the release workflow (goreleaser); the
// install script auto-detects OS/arch and fetches the right binary.

export const REPO = "kayaramazan/latch";
export const RELEASES_URL = `https://github.com/${REPO}/releases/latest`;
export const INSTALL_SH_URL = `https://github.com/${REPO}/releases/latest/download/install.sh`;

export const INSTALL = {
  unix: `curl -sSL ${INSTALL_SH_URL} | sh`,
  docker: `docker run --rm -p 4040:4040 ghcr.io/${REPO.split("/")[0]}/latchd:latest`,
};

export type OS = "mac" | "linux" | "windows" | "unknown";

export function detectOS(): OS {
  if (typeof navigator === "undefined") return "unknown";
  const s = `${navigator.platform} ${navigator.userAgent}`.toLowerCase();
  if (s.includes("mac")) return "mac";
  if (s.includes("win")) return "windows";
  if (s.includes("linux") || s.includes("android")) return "linux";
  return "unknown";
}

export const OS_LABEL: Record<OS, string> = {
  mac: "macOS",
  linux: "Linux",
  windows: "Windows",
  unknown: "your OS",
};
