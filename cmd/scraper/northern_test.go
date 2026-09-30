package scraper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/samuel-pratt/bc-ferries-api/cmd/models"
)

// The northern fixtures are the operator's seasonal pages captured on
// 29 September 2026, each showing its default Aug/Jun - Sep 30 season.

func northernFixture(t *testing.T, route string) *goquery.Document {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "html", "seasonal_northern_"+route+".html"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer file.Close()
	document, err := goquery.NewDocumentFromReader(file)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return document
}

func vancouverDate(t *testing.T, value string) time.Time {
	t.Helper()
	date, err := time.ParseInLocation("2006-01-02", value, vancouverLocation)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	return date
}

func parseNorthernFixture(t *testing.T, route, from, to, date string) (models.OfficialScheduleRoute, bool) {
	t.Helper()
	return parseNorthernScheduleRoute(
		northernFixture(t, route), from, to, vancouverDate(t, date),
		MakeSeasonalScheduleLink(from, to), time.Date(2026, 9, 29, 16, 0, 0, 0, time.UTC),
	)
}

func portCallSummary(calls []models.PortCall) string {
	parts := make([]string, 0, len(calls))
	for _, call := range calls {
		parts = append(parts, fmt.Sprintf("%d:%s:%s", call.Sequence, call.TerminalCode, call.Role))
	}
	return strings.Join(parts, " ")
}

func TestNorthern_InsidePassageCallsAtItsStopAndArrivesNextDay(t *testing.T) {
	route, ok := parseNorthernFixture(t, "PPH-PPR", "PPH", "PPR", "2026-09-29")
	if !ok || len(route.Sailings) != 1 {
		t.Fatalf("ok=%v sailings=%d, want one sailing", ok, len(route.Sailings))
	}
	sailing := route.Sailings[0]
	if sailing.ScheduledDepartureAt != "2026-09-29T07:30:00-07:00" {
		t.Errorf("departure %s", sailing.ScheduledDepartureAt)
	}
	if sailing.ScheduledArrivalAt != "2026-09-30T01:00:00-07:00" {
		t.Errorf("arrival %s, want 1:00 am the next day", sailing.ScheduledArrivalAt)
	}
	if got := portCallSummary(sailing.PortCalls); got != "0:PPH:origin 1:PBB:stop 2:PPR:destination" {
		t.Errorf("port calls %s", got)
	}
	stop := sailing.PortCalls[1]
	if stop.ScheduledArrivalAt != "" || stop.ScheduledDepartureAt != "" {
		t.Errorf("stop has invented times: %+v", stop)
	}
	if sailing.SailingID != "bcf:v1:2026-09-29:PPH-PPR:0730:01" {
		t.Errorf("sailing id %s", sailing.SailingID)
	}
}

func TestNorthern_WeekdayWithoutServiceIsAnEmptyTimetable(t *testing.T) {
	route, ok := parseNorthernFixture(t, "PPH-PPR", "PPH", "PPR", "2026-09-30")
	if !ok || len(route.Sailings) != 0 {
		t.Fatalf("ok=%v sailings=%d, want a valid empty day", ok, len(route.Sailings))
	}
}

// Tomorrow's page must not be read from today's default season.
func TestNorthern_RejectsADateOutsideTheDisplayedSeason(t *testing.T) {
	if _, ok := parseNorthernFixture(t, "PPH-PPR", "PPH", "PPR", "2026-10-01"); ok {
		t.Fatal("accepted 1 October from the page showing Aug 01 - Sep 30")
	}
	url, found := seasonURLForDate(northernFixture(t, "PPH-PPR"), vancouverDate(t, "2026-10-01"))
	want := "https://www.bcferries.com/routes-fares/schedules/seasonal/PPH-PPR?departureDate=20261001-20270203"
	if !found || url != want {
		t.Fatalf("season URL %q found=%v, want %q", url, found, want)
	}
}

func TestNorthern_HaidaGwaiiHonoursDateExceptions(t *testing.T) {
	// "10:30 am Except on Jun 3, 10, Sep 16, 23 & 30" is the only Wednesday row.
	route, ok := parseNorthernFixture(t, "PPR-PSK", "PPR", "PSK", "2026-09-30")
	if !ok || len(route.Sailings) != 0 {
		t.Fatalf("30 September: ok=%v sailings=%d, want excluded", ok, len(route.Sailings))
	}

	route, ok = parseNorthernFixture(t, "PPR-PSK", "PPR", "PSK", "2026-09-24")
	if !ok || len(route.Sailings) != 1 {
		t.Fatalf("24 September: ok=%v sailings=%d", ok, len(route.Sailings))
	}
	sailing := route.Sailings[0]
	if sailing.ScheduledDepartureTime != "10:30 am" || sailing.ScheduledArrivalAt != "2026-09-24T17:30:00-07:00" {
		t.Errorf("sailing %+v", sailing)
	}
	if got := portCallSummary(sailing.PortCalls); got != "0:PPR:origin 1:PSK:destination" {
		t.Errorf("port calls %s", got)
	}
}

func TestNorthern_OvernightHaidaGwaiiCrossing(t *testing.T) {
	route, ok := parseNorthernFixture(t, "PPR-PSK", "PPR", "PSK", "2026-09-28")
	if !ok || len(route.Sailings) != 1 {
		t.Fatalf("ok=%v sailings=%d", ok, len(route.Sailings))
	}
	if got := route.Sailings[0].ScheduledArrivalAt; got != "2026-09-29T06:00:00-07:00" {
		t.Errorf("arrival %s", got)
	}
}

func TestNorthern_DiscoveryCoastNonStop(t *testing.T) {
	route, ok := parseNorthernFixture(t, "PPH-BEC", "PPH", "BEC", "2026-09-30")
	if !ok || len(route.Sailings) != 1 {
		t.Fatalf("ok=%v sailings=%d", ok, len(route.Sailings))
	}
	sailing := route.Sailings[0]
	if sailing.ScheduledArrivalAt != "2026-09-30T17:30:00-07:00" {
		t.Errorf("arrival %s", sailing.ScheduledArrivalAt)
	}
	if got := portCallSummary(sailing.PortCalls); got != "0:PPH:origin 1:BEC:destination" {
		t.Errorf("port calls %s", got)
	}
}

func TestSeasonalPages_FollowsTheSeasonLinkForADateOutsideTheDefault(t *testing.T) {
	summer := northernFixture(t, "PPH-PPR")
	winterURL := "https://www.bcferries.com/routes-fares/schedules/seasonal/PPH-PPR?departureDate=20261001-20270203"
	winter, _ := goquery.NewDocumentFromReader(strings.NewReader(`<html></html>`))
	fetched := map[string]int{}
	pages := &seasonalPages{
		ctx:   context.Background(),
		byURL: map[string]*goquery.Document{},
		fetch: func(_ context.Context, url string) (*goquery.Document, error) {
			fetched[url]++
			switch url {
			case MakeSeasonalScheduleLink("PPH", "PPR"):
				return summer, nil
			case winterURL:
				return winter, nil
			}
			return nil, fmt.Errorf("unexpected fetch %s", url)
		},
	}

	for _, date := range []string{"2026-09-30", "2026-10-01", "2026-10-02"} {
		document, url, err := pages.forDate(MakeSeasonalScheduleLink("PPH", "PPR"), vancouverDate(t, date))
		if err != nil {
			t.Fatalf("%s: %v", date, err)
		}
		wantWinter := date != "2026-09-30"
		if (document == winter) != wantWinter || (url == winterURL) != wantWinter {
			t.Errorf("%s: got %s", date, url)
		}
	}
	for url, count := range fetched {
		if count != 1 {
			t.Errorf("fetched %s %d times", url, count)
		}
	}
}

func TestPublishedDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"17:30":  17*time.Hour + 30*time.Minute,
		"8h":     8 * time.Hour,
		"7h 45m": 7*time.Hour + 45*time.Minute,
		"20m":    20 * time.Minute,
	}
	for text, want := range cases {
		if got, ok := publishedDuration(text); !ok || got != want {
			t.Errorf("%q: %v %v", text, got, ok)
		}
	}
	if _, ok := publishedDuration("Varies"); ok {
		t.Error("parsed Varies")
	}
}
