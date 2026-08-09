package scraper

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"

	"github.com/samuel-pratt/bc-ferries-api/cmd/models"
)

var seasonalClockPattern = regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}\s*[ap]m\b`)

var seasonalDateTokenPattern = regexp.MustCompile(
	`(?i)(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|` +
		`jul(?:y)?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|` +
		`dec(?:ember)?)\s+(\d{1,2})|\b(\d{1,2})\b`,
)

var seasonalMonths = map[string]time.Month{
	"jan": time.January, "january": time.January,
	"feb": time.February, "february": time.February,
	"mar": time.March, "march": time.March,
	"apr": time.April, "april": time.April,
	"may": time.May,
	"jun": time.June, "june": time.June,
	"jul": time.July, "july": time.July,
	"aug": time.August, "august": time.August,
	"sep": time.September, "sept": time.September, "september": time.September,
	"oct": time.October, "october": time.October,
	"nov": time.November, "november": time.November,
	"dec": time.December, "december": time.December,
}

var southernGulfTerminalCodes = []string{"PSB", "PVB", "POB", "PST", "PLH"}

func normalizedWeekday(value string) string {
	value = strings.TrimSpace(strings.ToUpper(value))
	return strings.TrimSuffix(value, "S")
}

func mentionedSeasonalDates(note string) map[string]struct{} {
	dates := make(map[string]struct{})
	var currentMonth time.Month
	for _, match := range seasonalDateTokenPattern.FindAllStringSubmatch(strings.ToLower(note), -1) {
		dayText := match[3]
		if match[1] != "" {
			currentMonth = seasonalMonths[match[1]]
			dayText = match[2]
		}
		if currentMonth == 0 || dayText == "" {
			continue
		}
		day, err := strconv.Atoi(dayText)
		if err != nil || day < 1 || day > 31 {
			continue
		}
		dates[fmt.Sprintf("%02d-%02d", currentMonth, day)] = struct{}{}
	}
	return dates
}

func seasonalRowApplies(note string, serviceDate time.Time) bool {
	lower := strings.ToLower(normalizedText(note))
	dateKey := serviceDate.Format("01-02")
	if strings.Contains(lower, "only on") {
		_, included := mentionedSeasonalDates(lower)[dateKey]
		return included
	}
	if strings.Contains(lower, "except on") || strings.Contains(lower, "not available on") {
		_, excluded := mentionedSeasonalDates(lower)[dateKey]
		return !excluded
	}
	return true
}

// parseSeasonalScheduleSailingsForDate reads the operator's onward seasonal
// table for one Vancouver service date. The table carries weekday sections and
// row-level date exceptions; both are required before a sailing is accepted.
func parseSeasonalScheduleSailingsForDate(
	document *goquery.Document,
	serviceDate time.Time,
) ([]models.NonCapacitySailing, string, bool) {
	serviceDate = serviceDate.In(vancouverLocation)
	wantedDay := normalizedWeekday(serviceDate.Weekday().String())

	var scheduleTable *goquery.Selection
	document.Find("table.table-seasonal-schedule").EachWithBreak(func(_ int, table *goquery.Selection) bool {
		if table.Find("thead tr[data-schedule-day], thead [data-schedule-day], thead h4, thead b").Length() == 0 {
			return true
		}
		scheduleTable = table
		return false
	})
	if scheduleTable == nil || scheduleTable.Length() == 0 {
		return nil, "", false
	}

	var dayBody *goquery.Selection
	scheduleTable.Find("thead").EachWithBreak(func(_ int, header *goquery.Selection) bool {
		day := normalizedWeekday(header.Find("tr").First().AttrOr("data-schedule-day", ""))
		if day == "" {
			day = normalizedWeekday(header.Find("h4, b, th").First().Text())
		}
		if day != wantedDay && !strings.Contains(day, wantedDay) {
			return true
		}

		body := header.Next()
		for body.Length() > 0 && goquery.NodeName(body) != "tbody" {
			body = body.Next()
		}
		if body.Length() > 0 {
			dayBody = body
			return false
		}
		return true
	})
	if dayBody == nil || dayBody.Length() == 0 {
		return nil, "", false
	}

	var sailings []models.NonCapacitySailing
	duration := ""
	seen := make(map[string]struct{})
	dayBody.Find("tr.schedule-table-row").Each(func(_ int, row *goquery.Selection) {
		cells := row.Find("td")
		if cells.Length() < 3 {
			return
		}
		departureCell := cells.Eq(1)
		rowText := normalizedText(departureCell.Text())
		rowTextLower := strings.ToLower(rowText)
		if strings.Contains(rowTextLower, "dangerous goods only") ||
			strings.Contains(rowTextLower, "no passengers permitted") ||
			!seasonalRowApplies(rowText, serviceDate) {
			return
		}

		departure := seasonalClockPattern.FindString(rowText)
		arrival := seasonalClockPattern.FindString(normalizedText(cells.Eq(2).Text()))
		if departure == "" || arrival == "" {
			return
		}
		departure = strings.ToLower(normalizedText(departure))
		arrival = strings.ToLower(normalizedText(arrival))
		key := departure + "\x00" + arrival
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}

		var statuses []string
		departureCell.Find("p.red-text, p.text-black").Each(func(_ int, paragraph *goquery.Selection) {
			if value := normalizedText(paragraph.Text()); value != "" {
				statuses = append(statuses, value)
			}
		})
		sailings = append(sailings, models.NonCapacitySailing{
			DepartureTime: departure,
			ArrivalTime:   arrival,
			VesselStatus:  strings.Join(statuses, " | "),
		})

		if duration == "" && cells.Length() > 3 {
			duration = normalizedText(cells.Eq(3).Text())
		}
	})

	return sailings, duration, len(sailings) > 0 && duration != ""
}

func parseSeasonalOfficialScheduleRoute(
	document *goquery.Document,
	fromTerminalCode string,
	toTerminalCode string,
	serviceDate time.Time,
	sourceURL string,
	observedAt time.Time,
) (models.OfficialScheduleRoute, bool) {
	date := serviceDate.In(vancouverLocation)
	dateText := date.Format("2006-01-02")
	sailings, duration, ok := parseSeasonalScheduleSailingsForDate(document, date)
	route := models.OfficialScheduleRoute{
		RouteCode:        fromTerminalCode + toTerminalCode,
		FromTerminalCode: fromTerminalCode,
		ToTerminalCode:   toTerminalCode,
		ServiceDate:      dateText,
		SailingDuration:  duration,
		Sailings:         []models.OfficialScheduleSailing{},
		SourceURL:        sourceURL,
		ScrapedAt:        observedAt.UTC().Format(time.RFC3339),
	}
	if !ok {
		return route, false
	}

	occurrences := make(map[string]int)
	for _, sailing := range sailings {
		departure, err := time.ParseInLocation(
			"2006-01-02 3:04 pm",
			dateText+" "+strings.ToLower(sailing.DepartureTime),
			vancouverLocation,
		)
		if err != nil {
			return route, false
		}
		arrival, err := time.ParseInLocation(
			"2006-01-02 3:04 pm",
			dateText+" "+strings.ToLower(sailing.ArrivalTime),
			vancouverLocation,
		)
		if err != nil {
			return route, false
		}
		if arrival.Before(departure) {
			arrival = arrival.AddDate(0, 0, 1)
		}

		departureTime := departure.Format("3:04 pm")
		occurrences[departureTime]++
		sailingID, scheduledDepartureAt, valid := canonicalSailingIdentity(
			fromTerminalCode,
			toTerminalCode,
			dateText,
			departureTime,
			occurrences[departureTime],
		)
		if !valid {
			return route, false
		}
		route.Sailings = append(route.Sailings, models.OfficialScheduleSailing{
			SailingID:              sailingID,
			ServiceDate:            dateText,
			ScheduledDepartureTime: departureTime,
			ScheduledArrivalTime:   arrival.Format("3:04 pm"),
			ScheduledDepartureAt:   scheduledDepartureAt,
			ScheduledArrivalAt:     arrival.Format(time.RFC3339),
		})
	}
	return route, len(route.Sailings) > 0
}

func terminalDescriptionForCode(code string) (terminalDescription, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, description := range terminalDescriptions {
		if description.Code == code {
			return description, true
		}
	}
	return terminalDescription{}, false
}

type scheduledTerminalCall struct {
	description terminalDescription
	arrival     time.Time
	sourceURL   string
	scrapedAt   string
}

// buildOfficialSGIRoute groups the physical-terminal timetable pages by their
// common origin departure. Their published arrival instants establish the call
// order. Ambiguous same-terminal rows are rejected rather than guessed.
func buildOfficialSGIRoute(
	fromTerminalCode string,
	serviceDate time.Time,
	physicalRoutes map[string]models.OfficialScheduleRoute,
	observedAt time.Time,
) (models.OfficialScheduleRoute, bool) {
	dateText := serviceDate.In(vancouverLocation).Format("2006-01-02")
	route := models.OfficialScheduleRoute{
		RouteCode:        fromTerminalCode + "SGI",
		FromTerminalCode: fromTerminalCode,
		ToTerminalCode:   "SGI",
		ServiceDate:      dateText,
		SailingDuration:  "Varies",
		Sailings:         []models.OfficialScheduleSailing{},
		SourceURL:        "https://www.bcferries.com/routes-fares/schedules",
		ScrapedAt:        observedAt.UTC().Format(time.RFC3339),
	}
	origin, ok := terminalDescriptionForCode(fromTerminalCode)
	if !ok {
		return route, false
	}

	callsByDeparture := make(map[string][]scheduledTerminalCall)
	seenTerminal := make(map[string]struct{})
	ambiguous := make(map[string]struct{})
	for terminalCode, physicalRoute := range physicalRoutes {
		description, known := terminalDescriptionForCode(terminalCode)
		if !known || physicalRoute.ServiceDate != dateText {
			return route, false
		}
		for _, sailing := range physicalRoute.Sailings {
			departure, departureErr := time.Parse(time.RFC3339, sailing.ScheduledDepartureAt)
			arrival, arrivalErr := time.Parse(time.RFC3339, sailing.ScheduledArrivalAt)
			if departureErr != nil || arrivalErr != nil || !arrival.After(departure) {
				return route, false
			}
			departureKey := departure.Format(time.RFC3339)
			terminalKey := departureKey + "\x00" + terminalCode
			if _, duplicate := seenTerminal[terminalKey]; duplicate {
				ambiguous[departureKey] = struct{}{}
				continue
			}
			seenTerminal[terminalKey] = struct{}{}
			callsByDeparture[departureKey] = append(callsByDeparture[departureKey], scheduledTerminalCall{
				description: description,
				arrival:     arrival,
				sourceURL:   physicalRoute.SourceURL,
				scrapedAt:   physicalRoute.ScrapedAt,
			})
		}
	}

	departureKeys := make([]string, 0, len(callsByDeparture))
	for key := range callsByDeparture {
		if _, rejected := ambiguous[key]; !rejected {
			departureKeys = append(departureKeys, key)
		}
	}
	sort.Strings(departureKeys)
	for _, departureKey := range departureKeys {
		departure, err := time.Parse(time.RFC3339, departureKey)
		if err != nil {
			continue
		}
		calls := callsByDeparture[departureKey]
		sort.Slice(calls, func(i, j int) bool {
			if calls[i].arrival.Equal(calls[j].arrival) {
				return calls[i].description.Code < calls[j].description.Code
			}
			return calls[i].arrival.Before(calls[j].arrival)
		})
		if len(calls) == 0 {
			continue
		}
		// Two different terminals published at the same instant cannot establish
		// a trustworthy call order, so leave this sailing to the explicit
		// current-conditions fallback.
		hasArrivalTie := false
		for index := 1; index < len(calls); index++ {
			if calls[index-1].arrival.Equal(calls[index].arrival) {
				hasArrivalTie = true
				break
			}
		}
		if hasArrivalTie {
			continue
		}

		departureTime := departure.In(vancouverLocation).Format("3:04 pm")
		sailingID, scheduledDepartureAt, valid := canonicalSailingIdentity(
			fromTerminalCode,
			"SGI",
			dateText,
			departureTime,
			1,
		)
		if !valid {
			continue
		}
		portCalls := []models.PortCall{{
			PortCallID:           fmt.Sprintf("%s:call:%02d:%s", sailingID, 0, origin.Code),
			Sequence:             0,
			TerminalCode:         origin.Code,
			TerminalName:         origin.TerminalName,
			IslandName:           origin.IslandName,
			Role:                 "origin",
			ScheduledDepartureAt: scheduledDepartureAt,
			ScheduleSource:       "bc-ferries-seasonal-route-schedules",
			ScheduleSourceURL:    route.SourceURL,
			ScheduleScrapedAt:    route.ScrapedAt,
		}}
		for index, call := range calls {
			role := "stop"
			if index == len(calls)-1 {
				role = "destination"
			}
			sequence := index + 1
			portCalls = append(portCalls, models.PortCall{
				PortCallID:         fmt.Sprintf("%s:call:%02d:%s", sailingID, sequence, call.description.Code),
				Sequence:           sequence,
				TerminalCode:       call.description.Code,
				TerminalName:       call.description.TerminalName,
				IslandName:         call.description.IslandName,
				Role:               role,
				ScheduledArrivalAt: call.arrival.Format(time.RFC3339),
				ScheduleSource:     "bc-ferries-seasonal-route-schedules",
				ScheduleSourceURL:  call.sourceURL,
				ScheduleScrapedAt:  call.scrapedAt,
			})
		}
		finalArrival := calls[len(calls)-1].arrival
		route.Sailings = append(route.Sailings, models.OfficialScheduleSailing{
			SailingID:              sailingID,
			ServiceDate:            dateText,
			ScheduledDepartureTime: departureTime,
			ScheduledArrivalTime:   finalArrival.In(vancouverLocation).Format("3:04 pm"),
			ScheduledDepartureAt:   scheduledDepartureAt,
			ScheduledArrivalAt:     finalArrival.Format(time.RFC3339),
			PortCalls:              portCalls,
		})
	}
	return route, len(route.Sailings) > 0
}

func fetchOfficialScheduleDocument(
	parent context.Context,
	sourceURL string,
) (*goquery.Document, error) {
	pageContext, pageCancel := chromedp.NewContext(parent)
	defer pageCancel()
	requestContext, requestCancel := context.WithTimeout(pageContext, 45*time.Second)
	defer requestCancel()

	html, _, err := fetchWithChromedp(requestContext, sourceURL)
	if err != nil {
		return nil, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	return document, nil
}

func scrapeOfficialSeasonalCapacitySchedules(
	ctx context.Context,
	serviceDate time.Time,
	observedAt time.Time,
) {
	serviceDates := []time.Time{serviceDate, serviceDate.AddDate(0, 0, 1)}

	// Horseshoe Bay-Bowen Island is a fixed-arrival capacity route whose daily
	// URL redirects to the official seasonal table.
	bowenURL := MakeSeasonalScheduleLink("HSB", "BOW")
	if document, err := fetchOfficialScheduleDocument(ctx, bowenURL); err == nil {
		for _, date := range serviceDates {
			if route, ok := parseSeasonalOfficialScheduleRoute(
				document, "HSB", "BOW", date, bowenURL, observedAt,
			); ok {
				if err := persistOfficialScheduleRoute(route); err != nil {
					log.Printf("scrapeOfficialSeasonalCapacitySchedules: persist failed for %s on %s: %v", route.RouteCode, route.ServiceDate, err)
				}
			}
		}
	} else {
		log.Printf("scrapeOfficialSeasonalCapacitySchedules: fetch failed for %s: %v", bowenURL, err)
	}

	for _, origin := range []string{"SWB", "TSA"} {
		physicalByDate := make(map[string]map[string]models.OfficialScheduleRoute)
		complete := true
		for _, destination := range southernGulfTerminalCodes {
			sourceURL := MakeSeasonalScheduleLink(origin, destination)
			 document, err := fetchOfficialScheduleDocument(ctx, sourceURL)
			if err != nil {
				log.Printf("scrapeOfficialSeasonalCapacitySchedules: fetch failed for %s: %v", sourceURL, err)
				complete = false
				break
			}
			for _, date := range serviceDates {
				route, ok := parseSeasonalOfficialScheduleRoute(
					document, origin, destination, date, sourceURL, observedAt,
				)
				if !ok {
					log.Printf("scrapeOfficialSeasonalCapacitySchedules: rejected incomplete schedule for %s on %s", sourceURL, date.In(vancouverLocation).Format("2006-01-02"))
					complete = false
					break
				}
				dateText := date.In(vancouverLocation).Format("2006-01-02")
				if physicalByDate[dateText] == nil {
					physicalByDate[dateText] = make(map[string]models.OfficialScheduleRoute)
				}
				physicalByDate[dateText][destination] = route
			}
			if !complete {
				break
			}
		}
		if !complete {
			continue
		}
		for _, date := range serviceDates {
			dateText := date.In(vancouverLocation).Format("2006-01-02")
			routes := physicalByDate[dateText]
			if len(routes) != len(southernGulfTerminalCodes) {
				continue
			}
			grouped, ok := buildOfficialSGIRoute(origin, date, routes, observedAt)
			if !ok {
				continue
			}
			if err := persistOfficialScheduleRoute(grouped); err != nil {
				log.Printf("scrapeOfficialSeasonalCapacitySchedules: persist failed for %s on %s: %v", grouped.RouteCode, grouped.ServiceDate, err)
			}
		}
	}
}
