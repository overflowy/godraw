// Package assets holds what the app embeds: its handwriting font and its
// icons.
package assets

import "embed"

// DrawFont is Shantell Sans, the font of the "draw" style (SIL OFL, see
// fonts/OFL.txt).
//
//go:embed fonts/ShantellSans.ttf
var DrawFont []byte

// Icons holds Lucide's icons (ISC, see icons/LICENSE).
//
//go:embed icons/*.svg
var Icons embed.FS
