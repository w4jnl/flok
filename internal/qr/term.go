package qr

import "strings"

// Terminal draws the symbol for a terminal: two module rows per text line in half blocks, with
// quiet light modules around it (the standard asks for four, a camera is happier with them).
// Every line sets black on white itself, so a dark theme does not invert the code: a camera
// reads dark on light, and a QR drawn light on dark is read by fewer of them.
func (m *Matrix) Terminal(quiet int) string {
	total := m.Size + 2*quiet
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < m.Size && y < m.Size && m.Dark(x, y)
	}
	var b strings.Builder
	for y := 0; y < total; y += 2 {
		b.WriteString("\x1b[30;47m")
		for x := 0; x < total; x++ {
			top, bottom := dark(x, y), y+1 < total && dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}

// TerminalSize is the picture Terminal draws, in cells: columns and lines.
func (m *Matrix) TerminalSize(quiet int) (cols, rows int) {
	total := m.Size + 2*quiet
	return total, (total + 1) / 2
}
