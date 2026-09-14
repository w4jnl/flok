// Package icons embeds flok-bar's menu bar template icons (black + alpha; macOS tints them
// for light and dark appearance): the S3 mark, a terminal bracket holding one chevron and a
// cursor — a bar at rest, the w4j "4" dot when an agent is waiting on you. The @2x files
// (44px = 22pt on a Retina menu bar) are the ones the bar uses; 1x and 3x sit next to them
// for other consumers. Regenerate all sizes with `make icons`; see assets/brand/README.md.
package icons

import _ "embed"

//go:generate go run ./gen .

//go:embed flok@2x.png
var Flok []byte

//go:embed flok-dot@2x.png
var FlokDot []byte
