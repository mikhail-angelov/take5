package plate

import (
	"os"
	"testing"
)

func TestFrameFileNameRoundTrips(t *testing.T) {
	name := frameFileName(42, 19850)
	if name != "000042-000000019850.jpg" {
		t.Fatalf("frameFileName = %q", name)
	}
	seq, tMs, ok := parseFrameFileName(name)
	if !ok || seq != 42 || tMs != 19850 {
		t.Fatalf("parseFrameFileName(%q) = (%d, %d, %v)", name, seq, tMs, ok)
	}
}

func TestParseFrameFileNameRejectsOther(t *testing.T) {
	for _, name := range []string{"000042-000000019850.jpg.part", "not-a-frame.jpg", "000042.jpg", ""} {
		if _, _, ok := parseFrameFileName(name); ok {
			t.Errorf("parseFrameFileName(%q) unexpectedly ok", name)
		}
	}
}

func TestSegmentFileNameRoundTrips(t *testing.T) {
	name := segmentFileName(3, 40000, 61200)
	if name != "000003-000000040000-000000061200.mp4" {
		t.Fatalf("segmentFileName = %q", name)
	}
	info, ok := parseSegmentFileName(name)
	if !ok || info.seq != 3 || info.startMs != 40000 || info.endMs != 61200 {
		t.Fatalf("parseSegmentFileName(%q) = %+v, %v", name, info, ok)
	}
}

func TestParseSegmentFileNameRejectsOther(t *testing.T) {
	for _, name := range []string{"000003-000000040000-000000061200.mp4.part", "raw.mp4", ""} {
		if _, ok := parseSegmentFileName(name); ok {
			t.Errorf("parseSegmentFileName(%q) unexpectedly ok", name)
		}
	}
}

func TestBuildConcatListHoldsLastFrameAndStartsFirstAtBatchStart(t *testing.T) {
	frames := []frameRef{
		{tMs: 500, path: "/a/000001-000000000500.jpg"},
		{tMs: 800, path: "/a/000002-000000000800.jpg"},
	}
	list, err := buildConcatList(frames, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := "ffconcat version 1.0\n" +
		"file '/a/000001-000000000500.jpg'\n" +
		"duration 0.8000\n" +
		"file '/a/000002-000000000800.jpg'\n" +
		"duration 0.2000\n" +
		"file '/a/000002-000000000800.jpg'\n"
	if list != want {
		t.Fatalf("buildConcatList =\n%s\nwant\n%s", list, want)
	}
}

func TestBuildConcatListRejectsEmptyBatch(t *testing.T) {
	if _, err := buildConcatList(nil, 0, 1000); err == nil {
		t.Fatal("expected an error for an empty batch")
	}
}

func TestListFramesOrdersByTimestampAndSkipsPartFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/000002-000000000800.jpg", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/000001-000000000500.jpg", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A frame still mid-write when a crash hits: its .part must never be treated as committed.
	if err := os.WriteFile(dir+"/000003-000000001200.jpg.part", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	frames, err := listFrames(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("len(frames) = %d, want 2 (the .part file must be ignored)", len(frames))
	}
	if frames[0].tMs != 500 || frames[1].tMs != 800 {
		t.Fatalf("frames not ordered by timestamp: %+v", frames)
	}
}
