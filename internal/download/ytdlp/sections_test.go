package ytdlp

import (
	"context"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
)

func TestParseTimestamp(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
	}{
		{"90", 90},
		{"1:30", 90},
		{"01:30", 90},
		{"1:02:30", 3750},
		{"0:00", 0},
		{"1:30.5", 90.5},
		{"  2:00  ", 120},
	} {
		got, err := parseTimestamp(tc.in)
		if err != nil {
			t.Fatalf("parseTimestamp(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("parseTimestamp(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseTimestampRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "abc", "1:2:3:4", "1:70", "-5", "1m30s"} {
		if _, err := parseTimestamp(in); err == nil {
			t.Errorf("parseTimestamp(%q) = nil error, want failure", in)
		}
	}
}

func TestSectionArg(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end string
		want       string
	}{
		{"no trim", "", "", ""},
		{"both ends", "1:30", "4:00", "*90-240"},
		{"open end", "1:30", "", "*90-inf"},
		{"open start", "", "0:30", "*0-30"},
	} {
		got, err := sectionArg(core.DownloadRequest{SectionStart: tc.start, SectionEnd: tc.end})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: sectionArg = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSectionArgRejectsInvertedRange(t *testing.T) {
	_, err := sectionArg(core.DownloadRequest{SectionStart: "4:00", SectionEnd: "1:30"})
	if err == nil {
		t.Fatal("want error for end before start")
	}
	if !strings.Contains(err.Error(), "after start") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

// A trimmed request must actually reach yt-dlp as --download-sections.
func TestStartPassesDownloadSections(t *testing.T) {
	rec := &fakeRunner{}
	a := newAdapter(t, rec, nil)
	_, err := a.Start(context.Background(), core.DownloadRequest{
		ManualURL:    "https://www.youtube.com/watch?v=abc",
		Artist:       "A",
		Title:        "T",
		SectionStart: "1:30",
		SectionEnd:   "4:00",
	}, func(int) {})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(rec.gotArgs, " ")
	if !strings.Contains(args, "--download-sections *90-240") {
		t.Fatalf("args missing section: %s", args)
	}
	if !strings.Contains(args, "--force-keyframes-at-cuts") {
		t.Fatalf("args missing keyframe forcing: %s", args)
	}
}

// An untrimmed request must not pass the flag at all.
func TestStartOmitsDownloadSectionsWhenUntrimmed(t *testing.T) {
	rec := &fakeRunner{}
	a := newAdapter(t, rec, nil)
	if _, err := a.Start(context.Background(), core.DownloadRequest{
		ManualURL: "https://www.youtube.com/watch?v=abc", Artist: "A", Title: "T",
	}, func(int) {}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(rec.gotArgs, " "), "--download-sections") {
		t.Fatalf("unexpected section flag: %v", rec.gotArgs)
	}
}

func TestStartRejectsBadTimestamp(t *testing.T) {
	rec := &fakeRunner{}
	a := newAdapter(t, rec, nil)
	_, err := a.Start(context.Background(), core.DownloadRequest{
		ManualURL: "https://www.youtube.com/watch?v=abc", Title: "T", SectionStart: "banana",
	}, func(int) {})
	if err == nil {
		t.Fatal("want error for unparseable start time")
	}
}

func TestListChapters(t *testing.T) {
	rec := &fakeRunner{lines: []string{`{"id":"abc","chapters":[` +
		`{"title":"Intro","start_time":0,"end_time":30},` +
		`{"title":"Verse","start_time":30,"end_time":90}]}`}}
	a := newAdapter(t, rec, nil)
	chapters, err := a.ListChapters(context.Background(), "https://www.youtube.com/watch?v=abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 2 {
		t.Fatalf("chapters: %+v", chapters)
	}
	if chapters[0].Title != "Intro" || chapters[0].EndSec != 30 {
		t.Fatalf("first chapter: %+v", chapters[0])
	}
	if chapters[1].Title != "Verse" || chapters[1].StartSec != 30 {
		t.Fatalf("second chapter: %+v", chapters[1])
	}
	if strings.Contains(strings.Join(rec.gotArgs, " "), "--download-sections") {
		t.Fatal("chapter probe must not download")
	}
}

// A video with no chapters is a normal answer, not an error.
func TestListChaptersEmpty(t *testing.T) {
	rec := &fakeRunner{lines: []string{`{"id":"abc","chapters":null}`}}
	a := newAdapter(t, rec, nil)
	chapters, err := a.ListChapters(context.Background(), "https://www.youtube.com/watch?v=abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 0 {
		t.Fatalf("want no chapters, got %+v", chapters)
	}
}

// Untitled chapters still need a usable filename/track title.
func TestListChaptersNamesUntitled(t *testing.T) {
	rec := &fakeRunner{lines: []string{`{"chapters":[{"title":"","start_time":0,"end_time":10}]}`}}
	a := newAdapter(t, rec, nil)
	chapters, err := a.ListChapters(context.Background(), "https://www.youtube.com/watch?v=abc")
	if err != nil {
		t.Fatal(err)
	}
	if chapters[0].Title != "Chapter 1" {
		t.Fatalf("title: %q", chapters[0].Title)
	}
}
