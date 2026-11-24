# sbx-projects on a Wio Terminal

Browse projects and sandboxes, create sandboxes, and attach to them in a terminal of up to 45x17, typing on
a Bluetooth keyboard.

## What you need

- A Wio Terminal with RTL8720 firmware **2.1.3-ftl**. Stock firmware can't store keyboard bonds.
- A Bluetooth Low Energy keyboard. Classic-only keyboards can't work.
- `sbx-projects api` running on a Mac on the same network.
- The Seeed SAMD core 1.8.6 and these libraries:

```sh
arduino-cli config add board_manager.additional_urls https://files.seeedstudio.com/arduino/package_seeeduino_boards_index.json
arduino-cli core update-index
arduino-cli core install Seeeduino:samd@1.8.6
arduino-cli lib install "Seeed Arduino rpcWiFi" "Seeed Arduino rpcBLE" "Seeed Arduino rpcUnified" \
  "Seeed Arduino SFUD" "Seeed Arduino FS" ArduinoBearSSL ArduinoJson
```

## Flash it

```sh
cd wio
arduino-cli board list   # find the port
arduino-cli compile --upload --fqbn Seeeduino:samd:seeed_wio_terminal -p <port> SbxTerminal
```

## First run

1. Put the keyboard in pairing mode and switch the Wio on. Hold the top-left button (C) during the
   keyboard scan to skip it. Holding C while powering on forgets paired keyboards.
2. Choose a Wi-Fi network and type its password.
3. On the Mac, run `sbx-projects api --pair`.
4. Enter the Mac's IP address and the pairing code, then choose Continue.
5. Compare the certificate on the Wio with the `Certificate` line on the Mac. Continue only if
   they match, because this check is what stops another device on the network impersonating the Mac.

## Controls

| Control | Lists and forms | Terminal |
| --- | --- | --- |
| 5-way up / down | Move | Scroll one step |
| 5-way left / right | Back / open, or change a value | Scroll one page |
| 5-way press | Select | Return to the live screen |
| Button C (left) | Back | Detach; the agent keeps running |
| Button B (middle) | New sandbox | Restart, after the keyboard is lost |
| Button A (right) | Refresh | |

The keyboard's arrows, Enter, Esc and Tab work in lists and forms. In the terminal, every key goes
to the session, including Ctrl and Alt combinations.

## Text size

Choose **Settings > Text size** with left and right: Small (45x17), Medium (35x15) or Large
(32x11). It applies at once and is remembered. Reattach to use a new size in a session.

## Limits

- Scrolling uses tmux's history, so it works when attached to an agent but not in a shell.
- If the keyboard disconnects, reconnecting needs a restart, as rpcBLE allows one client per boot.
- The keyboard layout is US.
- Characters outside the built-in font, such as CJK and emoji, show as `?` at the correct width.
- The Wi-Fi password and device token are stored unencrypted in the last sector of the QSPI flash.
  If the Wio is lost, run `sbx-projects api devices rm "Wio Terminal"` on the Mac.

## Tests

`test/run.sh` runs the firmware's UI, TLS, HTTP, WebSocket and terminal code on a computer against
a real demo server and a real tmux. It needs g++, go, tmux, openssl and the two libraries above.
Bluetooth, Wi-Fi, the LCD, the buttons and flash storage are only exercised on the device.
