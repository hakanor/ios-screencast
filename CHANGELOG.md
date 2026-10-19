# Changelog

## 0.1.1

- The runner now notices when the broadcast has started, so it returns to the home screen and no longer reports a failure and tries again.
- A broadcast that started counts as started even if the runner reports otherwise.
- The broadcast stream reconnects while the screen is still: the extension greets new connections instead of waiting for the next frame.

## 0.1.0

First release.

- `ios-screencast start`: connects to the phone over USB (port forwarding, iOS 17.4+ userspace tunnel, developer disk image), starts the XCTest runner without Xcode, starts the ReplayKit broadcast and serves a viewer at http://localhost:1881.
- Modes: `mix` (ReplayKit, switching to the runner while a system alert is visible), `broadcast`, `runner`.
- Quality options: `--fps`, `--scale`, `--bitrate`, also adjustable live in the viewer.
- Upright video in portrait and landscape; the viewer turns the device frame to match.
- Device info (name, model, iOS version) and real device frames generated from the local Xcode.
- Never starts the broadcast while the screen is already being recorded, so existing recordings are not stopped.
- `--broadcast-attempts`, `--stay`, `--stdout`, `--udid`, `devices`, `version`.
- IPAs: `ios-screencast.ipa` (app with the ReplayKit broadcast extension) and `ios-screencast-runner.ipa` (XCTest runner, ~50 fps screenshots while a system alert is visible), unsigned for signing with your own tool.
