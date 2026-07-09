package livetv

import (
	"testing"
	"time"
)

const sampleGuide = `<?xml version="1.0" encoding="UTF-8"?>
<tv>
  <channel id="bbc1.uk"><display-name>BBC One</display-name></channel>
  <programme start="20240115180000 +0000" stop="20240115190000 +0000" channel="bbc1.uk">
    <title>Evening News</title>
    <desc>The day's headlines.</desc>
    <category>News</category>
  </programme>
  <programme start="20240115190000 +0000" stop="20240115200000 +0000" channel="bbc1.uk">
    <title>Drama Night</title>
  </programme>
  <programme start="20240115180000" stop="20240115183000" channel="cnn.us">
    <title>World Now</title>
  </programme>
  <programme channel="broken.tv"><title>No Time</title></programme>
</tv>`

func TestParseXMLTV(t *testing.T) {
	progs, err := ParseXMLTV([]byte(sampleGuide))
	if err != nil {
		t.Fatal(err)
	}
	// The entry with no start time is skipped.
	if len(progs) != 3 {
		t.Fatalf("expected 3 programs, got %d: %+v", len(progs), progs)
	}

	news := progs[0]
	if news.Title != "Evening News" || news.ChannelTvgID != "bbc1.uk" {
		t.Fatalf("news parsed wrong: %+v", news)
	}
	if news.Description != "The day's headlines." || news.Category != "News" {
		t.Fatalf("news detail wrong: %+v", news)
	}
	want := time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC)
	if !news.Start.Equal(want) {
		t.Fatalf("news start = %v, want %v", news.Start, want)
	}
	if news.Stop.Sub(news.Start) != time.Hour {
		t.Fatalf("news duration = %v", news.Stop.Sub(news.Start))
	}
	if news.ID == "" {
		t.Fatal("program ID must be set")
	}

	// Timezone-less times parse as UTC.
	if progs[2].Title != "World Now" || progs[2].Start.Location() != time.UTC {
		t.Fatalf("cnn program wrong: %+v", progs[2])
	}
}

func TestGroupProgramsWindow(t *testing.T) {
	progs, _ := ParseXMLTV([]byte(sampleGuide))
	byTvg, start, end := groupPrograms(progs)
	if len(byTvg["bbc1.uk"]) != 2 {
		t.Fatalf("bbc should have 2 programs, got %d", len(byTvg["bbc1.uk"]))
	}
	if !start.Equal(time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC)) {
		t.Fatalf("window start = %v", start)
	}
	if !end.Equal(time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC)) {
		t.Fatalf("window end = %v", end)
	}
}
