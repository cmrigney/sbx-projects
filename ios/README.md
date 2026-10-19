# sbx-projects for iPhone

A SwiftUI app for checking on and messaging the agents that `sbx-projects api` serves from your Mac. It needs iOS 17 and has no package dependencies.

## Setting up the Xcode project

The sources are in `Sources/`; the project file isn't checked in yet.

1. In Xcode, choose File › New › Project › iOS › App. Name it `SbxProjects`, with SwiftUI and Swift, no storage and no tests, and save it in this `ios/` folder without a new Git repository.
2. Delete the template's `ContentView.swift` and `SbxProjectsApp.swift`, then drag `Sources/` into the `SbxProjects` folder in the navigator (as a folder, added to the app target).
3. In the target's Build Settings, set **Info.plist File** to `Info.plist` (this folder's). Xcode merges it with the keys it generates. It adds Local Network and camera access, Bonjour browsing for `_sbxp._tcp`, local TLS, and the `sbxp://` URL scheme.
4. Set the deployment target to iOS 17.0.

## Trying it

```sh
./sbx-projects api --demo --pair
```

Run the app on a phone on the same Wi-Fi network as the Mac (the simulator can't scan QR codes, but it can pair by picking the Mac under On this network and typing the code). Scanning the QR code with the Camera app works too; it opens the app.

## How it fits together

| File | What it holds |
|---|---|
| `Model/Models.swift` | The JSON types from `internal/api/views.go` |
| `API/APIClient.swift` | Requests, the event stream, and certificate pinning |
| `API/Pairing.swift` | The paired Mac, its Keychain token, the pairing link and Bonjour browsing |
| `App/AppModel.swift` | The state the views show, kept live from the event stream |
| `Views/` | One file per screen, plus shared components and the markdown renderer |

The app keeps the event stream open while it is in the foreground and reconnects when it comes back. When the Mac stops answering, it looks for it again over Bonjour by the ID in its TXT record, in case its address changed.
