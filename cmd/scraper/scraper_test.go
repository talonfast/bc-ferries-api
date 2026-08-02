package scraper

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func TestParseCapacityRoute_PreservesScheduledAndOperationalTimes(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "html", "current_conditions_time_semantics.html")
	html, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}

	document, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	observedAt := time.Date(2026, time.August, 2, 22, 0, 0, 0, time.UTC)
	route := parseCapacityRoute(document, "TSA", "SWB", observedAt)
	if len(route.Sailings) != 4 {
		t.Fatalf("expected 4 sailings, got %d", len(route.Sailings))
	}

	current := route.Sailings[0]
	if current.SailingStatus != "current" {
		t.Fatalf("expected current sailing, got %q", current.SailingStatus)
	}
	if current.ScheduledDepartureTime != "2:00 pm" {
		t.Fatalf("expected scheduled departure 2:00 pm, got %q", current.ScheduledDepartureTime)
	}
	if current.ActualDepartureTime == nil || *current.ActualDepartureTime != "2:09 pm" {
		t.Fatalf("expected actual departure 2:09 pm, got %#v", current.ActualDepartureTime)
	}
	if current.EstimatedArrivalTime == nil || *current.EstimatedArrivalTime != "3:44 pm" {
		t.Fatalf("expected ETA 3:44 pm, got %#v", current.EstimatedArrivalTime)
	}
	if current.ActualArrivalTime != nil {
		t.Fatalf("current sailing must not have an actual arrival, got %#v", current.ActualArrivalTime)
	}
	// The legacy fields remain byte-for-byte compatible with existing clients.
	if current.DepartureTime != "2:09 pm" || current.ArrivalTime != "3:44 pm" {
		t.Fatalf("unexpected compatibility fields: time=%q arrivalTime=%q", current.DepartureTime, current.ArrivalTime)
	}

	past := route.Sailings[1]
	if past.ScheduledDepartureTime != "7:00 am" {
		t.Fatalf("expected scheduled departure 7:00 am, got %q", past.ScheduledDepartureTime)
	}
	if past.ActualDepartureTime == nil || *past.ActualDepartureTime != "7:05 am" {
		t.Fatalf("expected actual departure 7:05 am, got %#v", past.ActualDepartureTime)
	}
	if past.ActualArrivalTime == nil || *past.ActualArrivalTime != "8:34 am" {
		t.Fatalf("expected actual arrival 8:34 am, got %#v", past.ActualArrivalTime)
	}
	if past.EstimatedArrivalTime != nil {
		t.Fatalf("past sailing must not retain an ETA, got %#v", past.EstimatedArrivalTime)
	}

	future := route.Sailings[2]
	if future.ScheduledDepartureTime != "4:00 pm" || future.ActualDepartureTime != nil {
		t.Fatalf("unexpected future departure fields: %#v", future)
	}
	if future.ServiceDate != "2026-08-03" {
		t.Fatalf("expected tomorrow service date, got %q", future.ServiceDate)
	}

	cancelled := route.Sailings[3]
	if cancelled.SailingStatus != "cancelled" || cancelled.ScheduledDepartureTime != "6:00 pm" {
		t.Fatalf("unexpected cancelled sailing: %#v", cancelled)
	}
	if cancelled.ActualDepartureTime != nil {
		t.Fatalf("cancelled sailing must not have an actual departure, got %#v", cancelled.ActualDepartureTime)
	}

	for _, sailing := range route.Sailings {
		if sailing.ScrapedAt != "2026-08-02T22:00:00Z" {
			t.Fatalf("unexpected scrape timestamp %q", sailing.ScrapedAt)
		}
	}
}

func TestCapacitySailingJSON_UsesNullForUnavailableOperationalTimes(t *testing.T) {
	fixture := `
	<table class="detail-departure-table"><tbody>
	<tr class="mobile-friendly-row">
	<td><p>4:00 pm Queen of New Westminster</p></td>
	<td><span class="cc-vessel-percent-full">25%</span></td>
	</tr>
	</tbody></table>`
	document, err := goquery.NewDocumentFromReader(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	route := parseCapacityRoute(
		document,
		"TSA",
		"SWB",
		time.Date(2026, time.August, 2, 22, 0, 0, 0, time.UTC),
	)
	if len(route.Sailings) != 1 {
		t.Fatalf("expected one sailing, got %d", len(route.Sailings))
	}

	encoded, err := json.Marshal(route.Sailings[0])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"actualDepartureTime", "estimatedArrivalTime", "actualArrivalTime"} {
		value, exists := payload[field]
		if !exists || value != nil {
			t.Fatalf("expected %s to be explicit JSON null, got %#v", field, value)
		}
	}
}

func TestPacificServiceDate_UsesVancouverCalendarDate(t *testing.T) {
	// This is 11:30 pm PDT on August 1. Using UTC, or a fixed standard-time
	// offset, would incorrectly assign it to August 2.
	observedAt := time.Date(2026, time.August, 2, 6, 30, 0, 0, time.UTC)
	if got := pacificServiceDate(observedAt, false); got != "2026-08-01" {
		t.Fatalf("expected Vancouver service date 2026-08-01, got %q", got)
	}
	if got := pacificServiceDate(observedAt, true); got != "2026-08-02" {
		t.Fatalf("expected tomorrow service date 2026-08-02, got %q", got)
	}
}

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
