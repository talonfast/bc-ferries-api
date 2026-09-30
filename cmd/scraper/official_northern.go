package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/samuel-pratt/bc-ferries-api/cmd/db"
	"github.com/samuel-pratt/bc-ferries-api/cmd/models"
)

// The northern routes run once a day or less, publish no current conditions
// and no vessel, and are long enough that a Live Activity has to be split by
// leg. Their seasonal pages are the only timetable source: departure, final
// arrival, and the ordered intermediate stops, which carry no published time.

// NorthernRoutes are the origin-destination pages scraped for tracking.
var NorthernRoutes = [][2]string{
	{"PPH", "PPR"}, {"PPR", "PPH"}, // Inside Passage
	{"PPR", "PSK"}, {"PSK", "PPR"}, // Haida Gwaii
	{"PPH", "BEC"}, {"BEC", "PPH"}, // Discovery Coast
}

var northernTerminals = map[string]terminalDescription{
	"PPH": {Code: "PPH", TerminalName: "Port Hardy (Bear Cove)"},
	"PPR": {Code: "PPR", TerminalName: "Prince Rupert"},
	"PSK": {Code: "PSK", TerminalName: "Skidegate", IslandName: "Graham Island"},
	"BEC": {Code: "BEC", TerminalName: "Bella Coola"},
	"PBB": {Code: "PBB", TerminalName: "Bella Bella (McLoughlin Bay)"},
	"KLE": {Code: "KLE", TerminalName: "Klemtu"},
	"SHW": {Code: "SHW", TerminalName: "Shearwater"},
	"POF": {Code: "POF", TerminalName: "Ocean Falls"},
}

// The stop column names terminals the way the operator's route pages do.
// An unrecognized name rejects the sailing rather than guessing a code.
var northernStopCodes = map[string]string{
	"bella bella (mcloughlin bay)": "PBB",
	"bella bella":                  "PBB",
	"mcloughlin bay":               "PBB",
	"klemtu":                       "KLE",
	"shearwater":                   "SHW",
	"ocean falls":                  "POF",
	"bella coola":                  "BEC",
	"prince rupert":                "PPR",
	"port hardy (bear cove)":       "PPH",
	"port hardy":                   "PPH",
	"graham island (skidegate)":    "PSK",
	"skidegate":                    "PSK",
}

const northernScheduleSource = "bc-ferries-seasonal-northern"

var clockDurationPattern = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
var wordDurationPattern = regexp.MustCompile(`(?i)^(?:(\d+)\s*h)?\s*(?:(\d+)\s*m)?$`)

// publishedDuration reads "17:30", "8h", "7h 45m" or "20m".
func publishedDuration(value string) (time.Duration, bool) {
	value = normalizedText(value)
	if match := clockDurationPattern.FindStringSubmatch(value); match != nil {
		hours, _ := strconv.Atoi(match[1])
		minutes, _ := strconv.Atoi(match[2])
		return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute, true
	}
	match := wordDurationPattern.FindStringSubmatch(value)
	if match == nil || (match[1] == "" && match[2] == "") {
		return 0, false
	}
	hours, _ := strconv.Atoi(match[1])
	minutes, _ := strconv.Atoi(match[2])
	return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute, true
}

func northernPortCall(
	sailingID string,
	sequence int,
	description terminalDescription,
	role string,
	sourceURL string,
	scrapedAt string,
) models.PortCall {
	return models.PortCall{
		PortCallID:        fmt.Sprintf("%s:call:%02d:%s", sailingID, sequence, description.Code),
		Sequence:          sequence,
		TerminalCode:      description.Code,
		TerminalName:      description.TerminalName,
		IslandName:        description.IslandName,
		Role:              role,
		ScheduleSource:    northernScheduleSource,
		ScheduleSourceURL: sourceURL,
		ScheduleScrapedAt: scrapedAt,
	}
}

// parseNorthernScheduleRoute builds one service date's sailings for a
// northern route. It reports false for anything it cannot fully account for:
// a page from another season, an unknown stop, or a published duration that
// disagrees with the departure and arrival clocks (a sign the arrival was
// rolled onto the wrong day).
func parseNorthernScheduleRoute(
	document *goquery.Document,
	fromTerminalCode string,
	toTerminalCode string,
	serviceDate time.Time,
	sourceURL string,
	observedAt time.Time,
) (models.OfficialScheduleRoute, bool) {
	date := serviceDate.In(vancouverLocation)
	dateText := date.Format("2006-01-02")
	scrapedAt := observedAt.UTC().Format(time.RFC3339)
	route := models.OfficialScheduleRoute{
		RouteCode:        fromTerminalCode + toTerminalCode,
		FromTerminalCode: fromTerminalCode,
		ToTerminalCode:   toTerminalCode,
		ServiceDate:      dateText,
		Sailings:         []models.OfficialScheduleSailing{},
		SourceURL:        sourceURL,
		ScrapedAt:        scrapedAt,
	}
	origin, originKnown := northernTerminals[fromTerminalCode]
	destination, destinationKnown := northernTerminals[toTerminalCode]
	if !originKnown || !destinationKnown {
		return route, false
	}

	rows, duration, ok := parseSeasonalRowsForDate(document, date)
	route.SailingDuration = duration
	if !ok {
		return route, false
	}

	occurrences := make(map[string]int)
	for _, row := range rows {
		departure, err := time.ParseInLocation(
			"2006-01-02 3:04 pm", dateText+" "+row.sailing.DepartureTime, vancouverLocation,
		)
		if err != nil {
			return route, false
		}
		arrival, err := time.ParseInLocation(
			"2006-01-02 3:04 pm", dateText+" "+row.sailing.ArrivalTime, vancouverLocation,
		)
		if err != nil {
			return route, false
		}
		if !arrival.After(departure) {
			arrival = arrival.AddDate(0, 0, 1)
		}
		if published, known := publishedDuration(duration); known {
			difference := arrival.Sub(departure) - published
			if difference < -time.Minute || difference > time.Minute {
				log.Printf("parseNorthernScheduleRoute: %s on %s runs %v by the clock but publishes %s",
					route.RouteCode, dateText, arrival.Sub(departure), duration)
				return route, false
			}
		}

		departureTime := departure.Format("3:04 pm")
		occurrences[departureTime]++
		sailingID, scheduledDepartureAt, valid := canonicalSailingIdentity(
			fromTerminalCode, toTerminalCode, dateText, departureTime, occurrences[departureTime],
		)
		if !valid {
			return route, false
		}

		originCall := northernPortCall(sailingID, 0, origin, "origin", sourceURL, scrapedAt)
		originCall.ScheduledDepartureAt = scheduledDepartureAt
		portCalls := []models.PortCall{originCall}
		for _, stop := range row.stops {
			code, known := northernStopCodes[strings.ToLower(stop)]
			if !known {
				log.Printf("parseNorthernScheduleRoute: unknown stop %q on %s", stop, sourceURL)
				return route, false
			}
			portCalls = append(portCalls, northernPortCall(
				sailingID, len(portCalls), northernTerminals[code], "stop", sourceURL, scrapedAt,
			))
		}
		final := northernPortCall(sailingID, len(portCalls), destination, "destination", sourceURL, scrapedAt)
		final.ScheduledArrivalAt = arrival.Format(time.RFC3339)
		portCalls = append(portCalls, final)

		route.Sailings = append(route.Sailings, models.OfficialScheduleSailing{
			SailingID:              sailingID,
			ServiceDate:            dateText,
			ScheduledDepartureTime: departureTime,
			ScheduledArrivalTime:   arrival.Format("3:04 pm"),
			ScheduledDepartureAt:   scheduledDepartureAt,
			ScheduledArrivalAt:     arrival.Format(time.RFC3339),
			PortCalls:              portCalls,
		})
	}
	return route, true
}

// ScrapeNorthernSchedules stores today's and tomorrow's northern timetables.
// A failed or rejected page never replaces the last known-good row.
func ScrapeNorthernSchedules() {
	ctx, cancel := newBrowserContext(context.Background(), northernScrapeDeadline)
	defer cancel()
	scrapeNorthernSchedules(newSeasonalPages(ctx), time.Now())
}

func scrapeNorthernSchedules(pages *seasonalPages, now time.Time) {
	today := now.In(vancouverLocation)
	for _, date := range []time.Time{today, today.AddDate(0, 0, 1)} {
		for _, pair := range NorthernRoutes {
			baseURL := MakeSeasonalScheduleLink(pair[0], pair[1])
			document, sourceURL, err := pages.forDate(baseURL, date)
			if err != nil {
				log.Printf("ScrapeNorthernSchedules: fetch failed for %s: %v", baseURL, err)
				continue
			}
			route, ok := parseNorthernScheduleRoute(document, pair[0], pair[1], date, sourceURL, now)
			if !ok {
				log.Printf("ScrapeNorthernSchedules: rejected schedule for %s on %s", sourceURL, date.Format("2006-01-02"))
				continue
			}
			if err := persistNorthernScheduleRoute(route); err != nil {
				log.Printf("ScrapeNorthernSchedules: persist failed for %s on %s: %v", route.RouteCode, route.ServiceDate, err)
			}
		}
	}
}

func persistNorthernScheduleRoute(route models.OfficialScheduleRoute) error {
	sailingsJSON, err := json.Marshal(route.Sailings)
	if err != nil {
		return fmt.Errorf("marshal northern sailings: %w", err)
	}
	_, err = db.Conn.Exec(`
		INSERT INTO northern_schedule_routes (
			route_code, service_date, from_terminal_code, to_terminal_code,
			sailing_duration, sailings, source_url, scraped_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (route_code, service_date) DO UPDATE SET
			from_terminal_code = EXCLUDED.from_terminal_code,
			to_terminal_code = EXCLUDED.to_terminal_code,
			sailing_duration = EXCLUDED.sailing_duration,
			sailings = EXCLUDED.sailings,
			source_url = EXCLUDED.source_url,
			scraped_at = EXCLUDED.scraped_at
	`, route.RouteCode, route.ServiceDate, route.FromTerminalCode, route.ToTerminalCode,
		route.SailingDuration, sailingsJSON, route.SourceURL, route.ScrapedAt)
	if err != nil {
		return fmt.Errorf("upsert northern route %s on %s: %w", route.RouteCode, route.ServiceDate, err)
	}
	return nil
}
