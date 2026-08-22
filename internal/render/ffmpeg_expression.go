package render

import (
	"fmt"
	"strconv"
)

// Generates FFmpeg eval expressions from keyframes. FFmpeg filter syntax is treated as
// generated backend code: no business logic upstream ever writes one by hand (spec 22).
// Direct, character-exact port of src/render/ffmpeg-expression.js — see docs/plans/go-port.md
// Phase 3 gate.

// easeExpression: FFmpeg's evaluator has no ternary operator, so easing is spelled out with
// if(). Matches easeInOutCubic in the Director exactly, keeping the burned-in overlay and
// the zoomed video in agreement frame for frame.
func easeExpression(p string) string {
	return fmt.Sprintf("if(lt(%s,0.5),4*%s*%s*%s,1-pow(-2*%s+2,3)/2)", p, p, p, p, p)
}

func num(value float64) string {
	return formatFixed4(value)
}

func numSeconds(tMs float64) float64 {
	// The rounding step itself is part of the contract: JS's num() rounds to 4dp before the
	// value is ever compared or subtracted, so t1<=t0 and a.value===b.value below must
	// compare the *rounded* seconds too, not the raw millisecond ratio.
	return roundTo(tMs/1000, 4)
}

func roundTo(v float64, decimals int) float64 {
	// Route through the same string formatting num() uses, so the rounding is identical —
	// not merely "close" — to what will be printed. See numformat.go.
	parsed, err := strconv.ParseFloat(formatFixed(v, decimals), 64)
	if err != nil {
		return v
	}
	return parsed
}

// Keyframe is a (time, value) point.
type Keyframe struct {
	TMs   float64
	Value float64
}

// PiecewiseExpression builds a piecewise expression interpolating keyframes with cubic
// ease-in-out. keyframes are ordered {tMs, value}; the value is held before the first and
// after the last keyframe. timeVar is the FFmpeg variable holding output time in seconds
// ("ot" inside zoompan).
func PiecewiseExpression(keyframes []Keyframe, timeVar string) string {
	if timeVar == "" {
		timeVar = "ot"
	}
	if len(keyframes) == 0 {
		return "0"
	}
	if len(keyframes) == 1 {
		return num(keyframes[0].Value)
	}

	expression := num(keyframes[len(keyframes)-1].Value)

	// Built back to front so each `if` falls through to the segments after it.
	for i := len(keyframes) - 1; i > 0; i-- {
		a := keyframes[i-1]
		b := keyframes[i]
		t0 := numSeconds(a.TMs)
		t1 := numSeconds(b.TMs)

		if t1 <= t0 || a.Value == b.Value {
			expression = fmt.Sprintf("if(lt(%s,%s),%s,%s)", timeVar, num(t1), num(a.Value), expression)
			continue
		}

		p := fmt.Sprintf("((%s-%s)/%s)", timeVar, num(t0), num(t1-t0))
		eased := easeExpression(p)
		value := fmt.Sprintf("(%s+%s*(%s))", num(a.Value), num(b.Value-a.Value), eased)
		expression = fmt.Sprintf("if(lt(%s,%s),%s,%s)", timeVar, num(t1), value, expression)
	}

	first := keyframes[0]
	return fmt.Sprintf("if(lt(%s,%s),%s,%s)", timeVar, num(numSeconds(first.TMs)), num(first.Value), expression)
}

// CropOriginExpression is the zoompan crop-origin expression. Keeps the crop window inside
// the frame using the same clamp the ASS overlay applies, so the synthetic cursor lands
// where the pixels do. axis is "x" or "y".
func CropOriginExpression(centreExpression, axis string) string {
	size := "ih"
	if axis == "x" {
		size = "iw"
	}
	window := fmt.Sprintf("(%s/zoom)", size)
	return fmt.Sprintf("max(0,min(%s-%s,(%s)-%s/2))", size, window, centreExpression, window)
}
