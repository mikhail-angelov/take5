package render

import (
	"strconv"
	"strings"
)

// formatFixed replicates JS's `String(Number(value.toFixed(digits)))`: round to a fixed
// number of decimal places, then format with the shortest representation that round-trips
// — which for an already-rounded value means trimming trailing zeros (and a bare trailing
// dot). Both toFixed's rounding and FormatFloat's are correctly-rounded to the target digit;
// they can disagree only on an exact tie, which values derived from pixel coordinates and
// millisecond timestamps essentially never land on. This is the trap flagged in
// docs/plans/go-port.md ("toFixed(6) ... only a character-exact golden test catches it") —
// verified against the frozen fixture corpus, not just argued here.
func formatFixed(value float64, digits int) string {
	return trimTrailingZeros(strconv.FormatFloat(value, 'f', digits, 64))
}

func formatFixed4(value float64) string { return formatFixed(value, 4) }

// formatFixedKeep is toFixed without the trailing-zero trim, for contexts that always want
// exactly `digits` decimals (e.g. ffmpeg's `duration` lines, ASS timestamps).
func formatFixedKeep(value float64, digits int) string {
	return strconv.FormatFloat(value, 'f', digits, 64)
}

func trimTrailingZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
