package web

import (
	"fmt"
	"html/template"
)

// OCTICONS_SRC is the package directory of the unpacked npm tarball from
// "npm pack @primer/octicons@19.36.0". The generator checks its version.
//go:generate go run ../../tools/octicons -version 19.36.0 -src $OCTICONS_SRC

// octicon renders an Octicon as an inline SVG element for templates, as in
// {{ octicon "kebab-horizontal" 16 }}. The size is 16 by default and may be
// 24. The attributes match the output of toSVG in @primer/octicons with the
// fill and focusable options set. An unknown name or size is an error, so a
// typo fails the render instead of drawing nothing.
func octicon(name string, size ...int) (template.HTML, error) {
	height := 16
	switch len(size) {
	case 0:
	case 1:
		height = size[0]
	default:
		return "", fmt.Errorf("octicon %q: want at most one size, got %d", name, len(size))
	}
	if height != 16 && height != 24 {
		return "", fmt.Errorf("octicon %q: size %d is not 16 or 24", name, height)
	}
	paths, ok := octiconPaths[fmt.Sprintf("%s-%d", name, height)]
	if !ok {
		return "", fmt.Errorf("octicon %q: no such icon in @primer/octicons %s", name, octiconVersion)
	}
	// The name is a key of octiconPaths, so it is safe inside the class
	// attribute without escaping.
	return template.HTML(fmt.Sprintf(`<svg version="1.1" width="%[2]d" height="%[2]d" viewBox="0 0 %[2]d %[2]d" class="octicon octicon-%[1]s" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false">%[3]s</svg>`, name, height, paths)), nil
}
