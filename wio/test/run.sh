#!/usr/bin/env bash
# Builds the firmware's portable code for this computer and tests it against a real demo server
# and a real tmux. Needs g++, go, tmux, and the ArduinoBearSSL and ArduinoJson libraries.
# Screens are written to $OUT (default: a temporary directory).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
libs=${ARDUINO_LIBS:-$HOME/Arduino/libraries}
work=$(mktemp -d)
out=${OUT:-$work/screens}
mkdir -p "$out" "$work/bearssl" "$work/state"
server=
cleanup() {
  [ -n "$server" ] && kill "$server" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

inc=(-I"$libs/ArduinoBearSSL/src" -I"$libs/ArduinoJson/src")
echo "Building BearSSL"
find "$libs/ArduinoBearSSL/src/bearssl" -name '*.c' -print0 |
  xargs -0 -P "$(nproc)" -I{} sh -c 'gcc -O1 -w -I"$1/ArduinoBearSSL/src/bearssl" -I"$1/ArduinoBearSSL/src" -c "$3" -o "$2/bearssl/$(basename "$3").o"' _ "$libs" "$work" {}
ar rcs "$work/libbearssl.a" "$work"/bearssl/*.o

echo "Building the firmware"
src="$here/../SbxTerminal/src"
g++ -std=gnu++14 -O1 -g -w "${inc[@]}" "$here/e2e.cpp" "$src"/{vt,render,pinned_tls,net,api,app}.cpp \
  "$work/libbearssl.a" -o "$work/e2e"
g++ -std=gnu++14 -O1 -g -w "${inc[@]}" "$here/tmux_test.cpp" "$src"/{vt,render}.cpp -lutil -o "$work/tmux_test"

echo "Starting the demo server"
(cd "$repo" && go build -o "$work/sbx-projects" ./cmd/sbx-projects)
port=$((17000 + RANDOM % 2000))
"$work/sbx-projects" api --demo --pair --db "$work/state/db" --listen "127.0.0.1:$port" --log off \
  --projects-dir "$work/state/projects" --name wio-test \
  --whisper-bin "${WHISPER_BIN:-$here/fake_whisper.sh}" --whisper-model "${WHISPER_MODEL:-$here/fake_whisper.sh}" >"$work/server.log" 2>&1 &
server=$!
for _ in $(seq 100); do
  grep -q 'The code works once' "$work/server.log" 2>/dev/null && break
  sleep 0.1
done
code=$(grep -oE 'code=[0-9A-Z]{8}' "$work/server.log" | head -1 | cut -d= -f2)
if [ -z "$code" ]; then code=$(grep -oE '\b[0-9A-HJKMNP-TV-Z]{4}-?[0-9A-HJKMNP-TV-Z]{4}\b' "$work/server.log" | head -1 | tr -d -); fi
fp=$(openssl x509 -in "$work/state/api/cert.pem" -outform der | sha256sum | cut -d' ' -f1)

echo "--- advertisements"
g++ -std=gnu++14 -O1 -g -Wall "$here/adv_test.cpp" -o "$work/adv_test"
"$work/adv_test"
echo "--- end-to-end"
MIC_WAV="${MIC_WAV:-}" MIC_EXPECT="${MIC_EXPECT:-the words from whisper}" MIC_EXPECT_END="${MIC_EXPECT_END:-whisper}" \
  "$work/e2e" 127.0.0.1 "$port" "$code" "$fp" "$out"
echo "--- tmux"
for font in 0 1 2; do
  "$work/tmux_test" "$out/10-tmux-$font.ppm" "$font"
done
echo "Screens: $out"
