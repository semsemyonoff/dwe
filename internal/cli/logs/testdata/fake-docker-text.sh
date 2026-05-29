#!/bin/sh
# Fake docker binary for text-mode logs tests.
# Emits canned log lines to stdout and stderr, then exits 0.
echo "line one from stdout"
echo "line two from stdout"
echo "line from stderr" >&2
