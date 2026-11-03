// Package assets holds what the app embeds: its fonts and its icons.
package assets

import "embed"

// The fonts of the font styles.
var (
	// DrawFont is Shantell Sans Informal (SIL OFL, see fonts/OFL-ShantellSans.txt).
	//
	//go:embed fonts/ShantellSans-Informal-Regular.ttf
	DrawFont []byte

	// SansFont, SerifFont and MonoFont are IBM Plex, as IBM released it (SIL OFL,
	// see fonts/OFL-IBMPlex.txt). Plex is a reserved name, so these files stay
	// unchanged, in their format too. Medium is the regular weight here.
	//
	//go:embed fonts/IBMPlexSans-Medium.ttf
	SansFont []byte
	//go:embed fonts/IBMPlexSerif-Medium.ttf
	SerifFont []byte
	//go:embed fonts/IBMPlexMono-Medium.ttf
	MonoFont []byte
)

// Icons holds Lucide's icons (ISC, see icons/LICENSE).
//
//go:embed icons/*.svg
var Icons embed.FS
