package scraper

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestParseDailyScheduleSailings_ParsesUpdatedSWBTSAOnwardTimes(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "html", "daily_schedule.html")
	html, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found at %s: %v", fixturePath, err)
	}

	document, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		t.Fatalf("failed to parse fixture HTML: %v", err)
	}

	sailings, duration, ok := parseDailyScheduleSailings(document)
	if !ok {
		t.Fatalf("expected daily sailings to be parsed")
	}

	if len(sailings) != 10 {
		t.Fatalf("expected 10 onward sailings, got %d", len(sailings))
	}

	if duration != "01:35" {
		t.Fatalf("expected duration 01:35, got %q", duration)
	}

	seen := make(map[string]bool, len(sailings))
	for _, sailing := range sailings {
		seen[sailing.DepartureTime] = true
	}

	if !seen["8:00 am"] {
		t.Fatalf("expected parsed sailings to include 8:00 am")
	}

	if !seen["12:00 pm"] {
		t.Fatalf("expected parsed sailings to include 12:00 pm")
	}
}

func TestIsDailySchedulePage_UsesFinalRedirectURL(t *testing.T) {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<html><head><link rel="canonical" href="https://www.bcferries.com/routes-fares/schedules/daily/PSB-PST"></head></html>`))
	if err != nil {
		t.Fatal(err)
	}

	if isDailySchedulePage(document, "https://www.bcferries.com/routes-fares/schedules/seasonal/PSB-PST") {
		t.Fatal("expected the final seasonal URL to override the original daily canonical URL")
	}
}

func TestIsDailySchedulePage_FallsBackToCanonicalURL(t *testing.T) {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<html><head><link rel="canonical" href="https://www.bcferries.com/routes-fares/schedules/daily/SWB-TSA"></head></html>`))
	if err != nil {
		t.Fatal(err)
	}

	if !isDailySchedulePage(document, "") {
		t.Fatal("expected the daily canonical URL to identify a daily schedule")
	}
}

func TestParseDailyScheduleSailings_RequiresOnwardDailyTable(t *testing.T) {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<html><body>
			<table class="table-seasonal-schedule">
				<thead><tr data-schedule-day="Saturdays"><th>Depart</th><th>Arrive</th></tr></thead>
				<tbody><tr class="schedule-table-row"><td>12:10 pm</td><td>1:03 pm</td></tr></tbody>
			</table>
		</body></html>`))
	if err != nil {
		t.Fatal(err)
	}

	sailings, _, ok := parseDailyScheduleSailings(document)
	if ok || len(sailings) != 0 {
		t.Fatalf("expected seasonal markup not to be parsed as daily, got %#v", sailings)
	}
}

func TestParseDailyScheduleSailings_IgnoresReturnTable(t *testing.T) {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<html><body>
			<table id="dailyScheduleTableOnward">
				<tbody><tr class="schedule-table-row">
					<td></td><td>6:00 am</td><td>7:35 am</td><td>01:35</td>
				</tr></tbody>
			</table>
			<table id="dailyScheduleTableReturn">
				<tbody><tr class="schedule-table-row">
					<td></td><td>9:00 am</td><td>10:35 am</td><td>01:35</td>
				</tr></tbody>
			</table>
		</body></html>`))
	if err != nil {
		t.Fatal(err)
	}

	sailings, duration, ok := parseDailyScheduleSailings(document)
	if !ok {
		t.Fatal("expected onward daily schedule to parse")
	}
	if len(sailings) != 1 || sailings[0].DepartureTime != "6:00 am" {
		t.Fatalf("expected only the onward sailing, got %#v", sailings)
	}
	if duration != "01:35" {
		t.Fatalf("expected duration 01:35, got %q", duration)
	}
}
