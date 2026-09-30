package scraper

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Seasonal timetable pages show one season at a time, by default the one in
// force today, and link to the others with ?departureDate=YYYYMMDD-YYYYMMDD.
// Scraping tomorrow from today's default page silently applies the ending
// season's timetable across a season boundary, so every seasonal parse checks
// the season on the page and every fetch follows the link that covers the
// requested service date.

var displayedSeasonPattern = regexp.MustCompile(
	`([A-Z][a-z]{2}) (\d{1,2}), (\d{4}) - ([A-Z][a-z]{2}) (\d{1,2}), (\d{4})`,
)

var seasonLinkPattern = regexp.MustCompile(`departureDate=(\d{8})-(\d{8})`)

// seasonRange is an inclusive range of Vancouver service dates, held as UTC
// midnights so comparisons never depend on an offset.
type seasonRange struct {
	start time.Time
	end   time.Time
}

func civilDate(value time.Time) time.Time {
	local := value.In(vancouverLocation)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

func (season seasonRange) covers(serviceDate time.Time) bool {
	day := civilDate(serviceDate)
	return !day.Before(season.start) && !day.After(season.end)
}

// displayedSeason reads the season the page is showing from its date-range
// selector. It reports false when the page has no selector at all.
func displayedSeason(document *goquery.Document) (seasonRange, bool) {
	text := normalizedText(document.Find(`a[data-target="#dateRangeModal"]`).First().Text())
	match := displayedSeasonPattern.FindStringSubmatch(text)
	if match == nil {
		return seasonRange{}, false
	}
	start, startErr := time.Parse("Jan 2, 2006", fmt.Sprintf("%s %s, %s", match[1], match[2], match[3]))
	end, endErr := time.Parse("Jan 2, 2006", fmt.Sprintf("%s %s, %s", match[4], match[5], match[6]))
	if startErr != nil || endErr != nil || end.Before(start) {
		return seasonRange{}, false
	}
	return seasonRange{start: start, end: end}, true
}

// seasonURLForDate finds the page's own link to the season covering a date.
func seasonURLForDate(document *goquery.Document, serviceDate time.Time) (string, bool) {
	var found string
	document.Find(`a[href*="departureDate="]`).EachWithBreak(func(_ int, link *goquery.Selection) bool {
		href := strings.TrimSpace(link.AttrOr("href", ""))
		match := seasonLinkPattern.FindStringSubmatch(href)
		if match == nil {
			return true
		}
		start, startErr := time.Parse("20060102", match[1])
		end, endErr := time.Parse("20060102", match[2])
		if startErr != nil || endErr != nil {
			return true
		}
		if !(seasonRange{start: start, end: end}).covers(serviceDate) {
			return true
		}
		if strings.HasPrefix(href, "/") {
			href = "https://www.bcferries.com" + href
		}
		found = href
		return false
	})
	return found, found != ""
}

// seasonalPages fetches timetable pages for one scrape generation, following
// season links as needed and fetching each URL at most once.
type seasonalPages struct {
	ctx   context.Context
	fetch func(context.Context, string) (*goquery.Document, error)
	byURL map[string]*goquery.Document
}

func newSeasonalPages(ctx context.Context) *seasonalPages {
	return &seasonalPages{
		ctx:   ctx,
		fetch: fetchOfficialScheduleDocument,
		byURL: make(map[string]*goquery.Document),
	}
}

func (pages *seasonalPages) get(url string) (*goquery.Document, error) {
	if document, ok := pages.byURL[url]; ok {
		return document, nil
	}
	document, err := pages.fetch(pages.ctx, url)
	if err != nil {
		return nil, err
	}
	pages.byURL[url] = document
	return document, nil
}

// forDate returns the page for baseURL whose season covers serviceDate, and
// the URL it came from.
func (pages *seasonalPages) forDate(baseURL string, serviceDate time.Time) (*goquery.Document, string, error) {
	document, err := pages.get(baseURL)
	if err != nil {
		return nil, "", err
	}
	season, known := displayedSeason(document)
	if !known || season.covers(serviceDate) {
		return document, baseURL, nil
	}
	seasonURL, found := seasonURLForDate(document, serviceDate)
	if !found {
		return nil, "", fmt.Errorf("no published season covers %s", civilDate(serviceDate).Format("2006-01-02"))
	}
	document, err = pages.get(seasonURL)
	if err != nil {
		return nil, "", err
	}
	return document, seasonURL, nil
}
