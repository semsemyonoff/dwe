package render

import (
	"fmt"
	"strings"
	"unicode/utf8"

	figure "github.com/common-nighthawk/go-figure"
)

// validFigureColors lists the color names accepted by go-figure's NewColorFigure,
// in sorted order (used for error messages).
// Invalid names cause log.Fatalf inside the library, so we validate upfront.
var validFigureColors = []string{
	"blue", "cyan", "gray", "green", "purple", "red", "white", "yellow",
}

var figureColorSet = func() map[string]bool {
	m := make(map[string]bool, len(validFigureColors))
	for _, c := range validFigureColors {
		m[c] = true
	}
	return m
}()

// asciiBlock holds the prepared output lines and the left padding for one text entry.
type asciiBlock struct {
	lines  []string
	prefix string
}

// prepareASCII renders text as ASCII art and computes the centering prefix.
//
// colorName behaviour:
//   - "" or ColorNone ("none") → plain output, no color
//   - valid go-figure color    → ANSI-colored output
//   - anything else            → error with the list of valid colors
func prepareASCII(text, font, colorName string, totalWidth int) (asciiBlock, error) {
	if colorName == "" {
		colorName = ColorNone
	}
	if colorName != ColorNone && !figureColorSet[colorName] {
		return asciiBlock{}, fmt.Errorf(
			"unknown ASCII color %q; valid colors: %s (or %q for plain output)",
			colorName,
			strings.Join(validFigureColors, ", "),
			ColorNone,
		)
	}

	plain := strings.TrimRight(figure.NewFigure(text, font, true).String(), "\n")
	plainLines := strings.Split(plain, "\n")

	maxWidth := 0
	for _, l := range plainLines {
		if n := utf8.RuneCountInString(l); n > maxWidth {
			maxWidth = n
		}
	}
	leftPad := max((totalWidth-maxWidth)/2, 0)

	var outputLines []string
	if figureColorSet[colorName] {
		colored := strings.TrimRight(figure.NewColorFigure(text, font, colorName, true).ColorString(), "\n")
		outputLines = strings.Split(colored, "\n")
	} else {
		outputLines = plainLines
	}

	return asciiBlock{
		lines:  outputLines,
		prefix: strings.Repeat(" ", leftPad),
	}, nil
}

// ASCII generates centered ASCII art for each entry in lines using go-figure.
// Centering is relative to the configured line width.
//
// font defaults to "standard". colorName must be one of go-figure's supported
// colors (blue, cyan, gray, green, purple, red, white, yellow), ColorNone ("none"),
// or empty string (defaults to ColorNone).
func (w *Writer) ASCII(lines []string, font, colorName string) error {
	if font == "" {
		font = "standard"
	}
	totalWidth := w.getLineWidth() + 2

	for _, text := range lines {
		block, err := prepareASCII(text, font, colorName, totalWidth)
		if err != nil {
			return err
		}
		for _, l := range block.lines {
			_, _ = fmt.Fprintf(w.w, "%s%s\n", block.prefix, l)
		}
	}
	return nil
}
