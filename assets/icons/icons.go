// Package icons embeds flok-bar's menu bar template icons (black + alpha; macOS tints them
// for light and dark appearance). Regenerate with `make icons`.
package icons

import _ "embed"

//go:generate go run ./gen .

//go:embed flok.png
var Flok []byte

//go:embed flok-dot.png
var FlokDot []byte
