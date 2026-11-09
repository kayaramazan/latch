# Release checklist

Automated on tag by `.github/workflows/release.yml` (goreleaser binaries +
cosign + multi-arch ghcr images). The desktop app and a few manual smoke checks
are below.

## Pre-release

- [ ] `make test` (unit + race), `make test-ui` (component + i18n), and
      `sh deploy/acceptance/run.sh` are green on `main`.
- [ ] `CHANGELOG.md` has a dated section for the new version.
- [ ] `docs/COMPAT.md` protocol range is correct.

## Binaries & images (automated)

- [ ] Tag `vX.Y.Z` pushed → goreleaser publishes archives + `checksums.txt`
      (cosign-signed) to GitHub Releases.
- [ ] Multi-arch images on `ghcr.io/<owner>/{latchd,latch-relay,latch-control}`.
- [ ] `deploy/install.sh --version vX.Y.Z` installs that release.

## Desktop app (electron)

Build per OS on its own runner (`cd electron`):

- [ ] Place the matching `latchd` binary in `electron/resources/bin/`
      (`latchd` or `latchd.exe`) — from the goreleaser build.
- [ ] macOS: `npm run dist:mac` → `.dmg`. Notarize with
      `APPLE_ID` / `APPLE_APP_SPECIFIC_PASSWORD` / `APPLE_TEAM_ID` set
      (electron-builder runs `notarytool`); verify with `spctl -a -t open`.
- [ ] Windows: `npm run dist:win` → NSIS installer (sign with a code-signing
      cert if available).
- [ ] Linux: `npm run dist:linux` → AppImage + `.deb`.

### Desktop smoke (each OS)

- [ ] App launches; the window shows the Latch UI (served by the bundled latchd).
- [ ] Tray menu: Open, Open logs, Quit work; closing the window hides to tray.
- [ ] Add-route screen suggests detected local ports.
- [ ] Quit stops the latchd process (no orphan).
- [ ] Auto-update: a newer published release is offered (electron-updater).

## Sign-off

- [ ] On a clean Linux VM, following the README runs both scenarios in ≤10 min.
- [ ] Real VPS + real ACME: tunnel and local scenarios serve valid public HTTPS.
