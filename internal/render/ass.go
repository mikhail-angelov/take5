package render

import (
	"fmt"
	"math"
	"strings"

	"take5/internal/director"
)

// Builds the ASS overlay burned in by Pass B: synthetic cursor, click pulse and captions.
// Direct, character-exact port of src/render/ass.js — see docs/plans/go-port.md Phase 3
// gate: generated filter graphs and ASS files must match the frozen fixtures character for
// character, because they are what determines the picture, not the video.

// Classic arrow in a 12 x 20 box with its hotspot at (0,0), so \an7 + \pos puts the tip
// exactly on the target point.
var cursorShape = [][2]float64{
	{0, 0}, {0, 17}, {4.2, 13.2}, {6.8, 19.8}, {9.9, 18.5}, {7.3, 12}, {12.4, 12},
}

const cursorShapeHeight = 19.8

// Closed-hand cursor with the same top-left hotspot as cursorShape. It is shown while
// the primary pointer button is held during a drag.
var grabbingCursorShape = [][2]float64{
	{3, 0}, {6, 0}, {6, 7}, {8, 4}, {10, 5}, {8, 9}, {12, 8},
	{13, 10}, {11, 13}, {14, 14}, {13, 17}, {9, 17}, {7, 20},
	{4, 19}, {2, 14}, {0, 13}, {0, 8}, {3, 8},
}

func assTime(ms float64) string {
	total := math.Max(0, ms) / 1000
	hours := int(math.Floor(total / 3600))
	minutes := int(math.Floor(math.Mod(total, 3600) / 60))
	seconds := math.Mod(total, 60)
	secStr := formatFixedKeep(seconds, 2)
	for len(secStr) < 5 {
		secStr = "0" + secStr
	}
	return fmt.Sprintf("%d:%02d:%s", hours, minutes, secStr)
}

// assText neutralizes braces and backslashes, which are override-block syntax in ASS.
func assText(text string) string {
	replacer := strings.NewReplacer("\\", "/", "{", "(", "}", ")")
	return replacer.Replace(text)
}

func drawing(points [][2]float64, scale float64) string {
	parts := make([]string, len(points))
	for i, p := range points {
		x, y := p[0]*scale, p[1]*scale
		cmd := "l"
		if i == 0 {
			cmd = "m"
		}
		parts[i] = fmt.Sprintf("%s %s %s", cmd, formatFixedKeep(x, 2), formatFixedKeep(y, 2))
	}
	return strings.Join(parts, " ")
}

// circleDrawing is drawn with its bounding box starting at (0,0) so it can use the same
// \an7 placement as the cursor, whose hotspot is likewise its own origin. \an5 does not
// center a drawing the way it centers text, so it is avoided entirely.
func circleDrawing(radius float64) string {
	k := radius * 0.5523
	r := radius
	p := func(x, y float64) string {
		return formatFixedKeep(x+r, 2) + " " + formatFixedKeep(y+r, 2)
	}
	return "m " + p(-r, 0) + " " +
		"b " + p(-r, -k) + " " + p(-k, -r) + " " + p(0, -r) + " " +
		"b " + p(k, -r) + " " + p(r, -k) + " " + p(r, 0) + " " +
		"b " + p(r, k) + " " + p(k, r) + " " + p(0, r) + " " +
		"b " + p(-k, r) + " " + p(-r, k) + " " + p(-r, 0)
}

// createProjector maps a viewport-space point to its position in the final,
// camera-transformed frame. Mirrors cropOriginExpression so the overlay and the pixels
// agree.
func createProjector(camera []director.CameraKeyframe, viewport director.Viewport, video VideoProbe) func(tMs, x, y float64) (float64, float64) {
	sx := float64(video.Width) / viewport.Width
	sy := float64(video.Height) / viewport.Height

	return func(tMs, x, y float64) (float64, float64) {
		cam := cameraStateAt(camera, tMs, viewport)
		cropWidth := float64(video.Width) / cam.Zoom
		cropHeight := float64(video.Height) / cam.Zoom
		cropX := clamp(cam.Cx*sx-cropWidth/2, 0, float64(video.Width)-cropWidth)
		cropY := clamp(cam.Cy*sy-cropHeight/2, 0, float64(video.Height)-cropHeight)
		return (x*sx - cropX) * cam.Zoom, (y*sy - cropY) * cam.Zoom
	}
}

func stylesBlock(video VideoProbe) string {
	fontSize := int(math.Max(16, math.Round(float64(video.Height)*0.034)))
	marginV := int(math.Round(float64(video.Height) * 0.06))
	padding := int(math.Round(float64(fontSize) * 0.5))

	lines := []string{
		"[V4+ Styles]",
		"Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, " +
			"BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, " +
			"BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding",
		// Drawings carry their own colors; these styles only fix border and alignment.
		"Style: Cursor,Arial,20,&H00FFFFFF,&H00FFFFFF,&H00181818,&H00000000,0,0,0,0,100,100,0,0,1,2,0,7,0,0,0,1",
		"Style: Pulse,Arial,20,&H00FFFFFF,&H00FFFFFF,&H00FFFFFF,&H00000000,0,0,0,0,100,100,0,0,1,0,0,5,0,0,0,1",
		// BorderStyle 3 draws an opaque caption box in OutlineColour; it has to stay
		// readable over a light application background, so it is only slightly transparent.
		fmt.Sprintf(
			"Style: Label,Arial,%d,&H00FFFFFF,&H00FFFFFF,&H26140F0C,&H00000000,0,0,0,0,100,100,0,0,3,%d,0,2,40,40,%d,1",
			fontSize, padding, marginV,
		),
		fmt.Sprintf(
			"Style: Shortcut,Arial,%d,&H00FFFFFF,&H00FFFFFF,&H26241A0F,&H00000000,1,0,0,0,100,100,2,0,3,%d,0,2,40,40,%d,1",
			fontSize, padding, marginV,
		),
	}
	return strings.Join(lines, "\n")
}

func dialog(layer int, startMs, endMs float64, style, text string) string {
	return fmt.Sprintf("Dialogue: %d,%s,%s,%s,,0,0,0,,%s", layer, assTime(startMs), assTime(endMs), style, text) //nolint:misspell // ASS format spec
}

// cursorEvents: one event per frame would be correct but wasteful; the cursor is
// stationary most of the time, so identical consecutive samples collapse into a single
// event.
func cursorEvents(cursor director.CursorPlan, projector func(tMs, x, y float64) (float64, float64), cursorSizePx, frameMs, durationMs float64) []string {
	if cursor.Start == nil {
		return nil
	}

	scale := cursorSizePx / cursorShapeHeight
	shape := drawing(cursorShape, scale)
	grabbingShape := drawing(grabbingCursorShape, scale)
	border := formatFixedKeep(math.Max(1, cursorSizePx*0.09), 1)

	var events []string
	runStartMs := 0.0
	var runX, runY float64
	var runShape string
	haveRun := false

	flush := func(endMs float64) {
		if !haveRun {
			return
		}
		events = append(events, dialog(2, runStartMs, endMs, "Cursor", fmt.Sprintf(
			`{\an7\pos(%s,%s)\bord%s\shad0\1c&HFFFFFF&\3c&H181818&\p1}%s{\p0}`,
			formatFixedKeep(runX, 1), formatFixedKeep(runY, 1), border, runShape,
		)))
	}

	for tMs := 0.0; tMs < durationMs; tMs += frameMs {
		world := cursorPositionAt(cursor, tMs)
		sx, sy := projector(tMs, world.X, world.Y)
		currentShape := shape
		if cursorHeldAt(cursor, tMs) {
			currentShape = grabbingShape
		}
		if !haveRun || math.Abs(sx-runX) > 0.05 || math.Abs(sy-runY) > 0.05 || currentShape != runShape {
			flush(tMs)
			runStartMs = tMs
			runX, runY = sx, sy
			runShape = currentShape
			haveRun = true
		}
	}
	flush(durationMs)
	return events
}

const pulseStartAlpha = 0x50
const pulseStartRadiusRatio = 0.1
const pulseEndRadiusRatio = 1.3

// clickEvents is sampled per frame rather than animated with \t, for two reasons: a pulse
// anchored to one position slides off its target whenever the camera is still moving when
// the click lands, and \fscx on a drawing shifts its alignment box. The radius is baked
// into the path instead, so the circle is always exactly centered on the click.
func clickEvents(cursor director.CursorPlan, projector func(tMs, x, y float64) (float64, float64), cursorSizePx, frameMs, durationMs float64) []string {
	pulseMs := cursor.ClickPulseMs
	startRadius := cursorSizePx * pulseStartRadiusRatio
	endRadius := cursorSizePx * pulseEndRadiusRatio

	var events []string
	for _, click := range cursor.Clicks {
		endMs := math.Min(click.TMs+pulseMs, durationMs)
		for tMs := math.Ceil(click.TMs/frameMs) * frameMs; tMs < endMs; tMs += frameMs {
			p := (tMs - click.TMs) / pulseMs
			// Expands quickly then settles, while fading out linearly.
			q := 1 - p
			radius := startRadius + (endRadius-startRadius)*(1-q*q*q)
			alpha := strings.ToUpper(fmt.Sprintf("%02x", int(math.Round(pulseStartAlpha+(255-pulseStartAlpha)*p))))
			sx, sy := projector(tMs, click.X, click.Y)
			events = append(events, dialog(1, tMs, math.Min(tMs+frameMs, endMs), "Pulse", fmt.Sprintf(
				`{\an7\pos(%s,%s)\bord0\shad0\1c&HFFFFFF&\1a&H%s&\p1}%s{\p0}`,
				formatFixedKeep(sx-radius, 1), formatFixedKeep(sy-radius, 1), alpha, circleDrawing(radius),
			)))
		}
	}
	return events
}

func annotationEvents(annotations []director.Annotation) []string {
	events := make([]string, len(annotations))
	for i, a := range annotations {
		style := "Label"
		if a.Kind == "shortcut" {
			style = "Shortcut"
		}
		events[i] = dialog(0, a.StartMs, a.EndMs, style, assText(a.Text))
	}
	return events
}

// BuildAss generates ASS subtitle content for the rendered video.
func BuildAss(project director.Project, video VideoProbe, fps int) string {
	viewport := project.Viewport
	projector := createProjector(project.Camera, viewport, video)
	// Keep the cursor a constant on-screen size, proportional to the recording.
	// Size comes from the project (set by Director from config at planning time).
	cursorSizePx := project.Cursor.SizePx * (float64(video.Height) / viewport.Height)

	frameMs := 1000.0 / float64(fps)
	durationMs := float64(project.DurationMs)

	events := make([]string, 0, len(project.Annotations)+len(project.Cursor.Clicks)*50+len(project.Cursor.Moves)*50)
	events = append(events, annotationEvents(project.Annotations)...)
	events = append(events, clickEvents(project.Cursor, projector, cursorSizePx, frameMs, durationMs)...)
	events = append(events, cursorEvents(project.Cursor, projector, cursorSizePx, frameMs, durationMs)...)

	lines := make([]string, 0, 12+len(events)+1)
	lines = append(lines,
		"[Script Info]",
		"ScriptType: v4.00+",
		fmt.Sprintf("PlayResX: %d", video.Width),
		fmt.Sprintf("PlayResY: %d", video.Height),
		"WrapStyle: 2",
		"ScaledBorderAndShadow: yes",
		"YCbCr Matrix: None",
		"",
		stylesBlock(video),
		"",
		"[Events]",
		"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text",
	)
	lines = append(lines, events...)
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}
