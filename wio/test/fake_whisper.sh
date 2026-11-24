#!/bin/sh
# Stands in for whisper-cli: checks it was given a real 16 kHz recording, then "transcribes" it.
while [ $# -gt 0 ]; do [ "$1" = "-f" ] && wav=$2; shift; done
[ "$(head -c 4 "$wav")" = RIFF ] || exit 1
[ "$(wc -c <"$wav")" -gt 16000 ] || exit 1
sleep 0.5 # long enough for the "Transcribing" status to be seen
echo " the words from whisper"
