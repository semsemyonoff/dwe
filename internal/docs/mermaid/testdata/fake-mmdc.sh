#!/bin/bash
# Fake mmdc for testing. Copies a fixed PNG to the output path.

set -e

OUTPUT_FILE=""
INPUT_FILE=""
VERSION="${1:---version}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        -o|--output)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        -i|--input)
            INPUT_FILE="$2"
            shift 2
            ;;
        --version|-v)
            echo "mermaid-cli/test 0.1.0"
            exit 0
            ;;
        *)
            shift
            ;;
    esac
done

if [ -z "$OUTPUT_FILE" ]; then
    echo "missing output file" >&2
    exit 1
fi

# Write a minimal PNG file (1x1 transparent PNG).
# This is the smallest valid PNG.
# Hex: 89 50 4E 47 0D 0A 1A 0A (PNG signature)
#      00 00 00 0D (IHDR chunk length)
#      49 48 44 52 (IHDR)
#      00 00 00 01 00 00 00 01 08 06 00 00 00 1F 15 C4 89 (IHDR data: 1x1, RGBA)
#      00 00 00 0A (IDAT chunk length)
#      49 44 41 54 (IDAT)
#      78 9C 63 00 01 00 00 05 00 01 0D 0A 2D B4 (IDAT data)
#      00 00 00 00 (IEND chunk length)
#      49 45 4E 44 (IEND)
#      AE 42 60 82 (IEND CRC)

printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\nIDAT\x78\x9cc\x00\x01\x00\x00\x05\x00\x01\x0d\x0a\x2d\xb4\x00\x00\x00\x00IEND\xaeB`\x82' > "$OUTPUT_FILE"
