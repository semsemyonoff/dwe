#!/bin/sh
# Fake docker binary for JSON-mode logs tests.
# Emits timestamped lines (docker --timestamps format) to stdout and stderr.
echo "2026-05-29T07:30:00.000000000Z line-from-stdout"
echo "2026-05-29T07:30:01.000000000Z line-from-stderr" >&2
echo "2026-05-29T07:30:02.000000000Z another-stdout"
