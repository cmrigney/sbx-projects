# sbx-projects for iPhone

A SwiftUI app for checking on and messaging the agents that `sbx-projects api` serves from your Mac. It needs iOS 17. Its one package dependency is [SwiftTerm](https://github.com/migueldeicaza/SwiftTerm), pinned to 1.18.0, the last release without a build plugin Xcode asks you to trust.

## Building

Open `SbxProjects/SbxProjects.xcodeproj` in Xcode 26 or later. The app folder is synchronized with the project, so new files in `SbxProjects/SbxProjects/` are built without adding them by hand. `SbxProjects/Info.plist` is merged with the keys Xcode generates; it adds Local Network and camera access, Bonjour browsing for `_sbxp._tcp`, local TLS, and the `sbxp://` URL scheme.

## Trying it

```sh
./sbx-projects api --demo --pair
```

Run the app on a phone on the same Wi-Fi network as the Mac (the simulator can't scan QR codes, but it can pair by picking the Mac under On this network and typing the code). Scanning the QR code with the Camera app works too; it opens the app.

## How it fits together

Paths are under `SbxProjects/SbxProjects/`.

| File | What it holds |
|---|---|
| `Model/Models.swift` | The JSON types from `internal/api/views.go` |
| `API/APIClient.swift` | Requests, the event stream, the terminal's WebSocket, and certificate pinning |
| `API/Pairing.swift` | The paired Macs and which one is shown, their Keychain tokens, the pairing link and Bonjour browsing |
| `App/AppModel.swift` | The state the views show, kept live from the event stream |
| `Views/` | One file per screen, plus shared components and the markdown renderer |
| `Views/TerminalScreen.swift` | The attached terminal: SwiftTerm fed from the WebSocket, detaching in the background and attaching again on return |

The app keeps the event stream open while it is in the foreground and reconnects when it comes back. When the Mac stops answering, it looks for it again over Bonjour by the ID in its TXT record, in case its address changed.
