package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"log"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"

	"github.com/samuel-pratt/bc-ferries-api/cmd/db"
	"github.com/samuel-pratt/bc-ferries-api/cmd/models"
	"github.com/samuel-pratt/bc-ferries-api/cmd/staticdata"
)

/*
 * MakeCurrentConditionsLink
 *
 * Makes a link to the current conditions page for a given departure and destination
 *
 * @param string departure
 * @param string destination
 *
 * @return string
 */
func MakeCurrentConditionsLink(departure, destination string) string {
	return "https://www.bcferries.com/current-conditions/" + departure + "-" + destination
}

/*
 * MakeScheduleLink
 *
 * Builds a link to the DAILY schedule page for a given departure and destination.
 * Daily pages reflect route-specific service updates in effect for the current day.
 *
 * @param string departure
 * @param string destination
 *
 * @return string
 */
func MakeScheduleLink(departure, destination string) string {
	return "https://www.bcferries.com/routes-fares/schedules/daily/" + departure + "-" + destination
}

func MakeScheduleLinkForDate(departure, destination string, serviceDate time.Time) string {
	values := url.Values{}
	values.Set("scheduleDate", serviceDate.In(vancouverLocation).Format("01/02/2006"))
	return MakeScheduleLink(departure, destination) + "?" + values.Encode()
}

/*
 * MakeSeasonalScheduleLink
 *
 * Builds a link to the SEASONAL schedule page for a given departure and destination.
 *
 * @param string departure
 * @param string destination
 *
 * @return string
 */
func MakeSeasonalScheduleLink(departure, destination string) string {
	return "https://www.bcferries.com/routes-fares/schedules/seasonal/" + departure + "-" + destination
}

var departedSailingPattern = regexp.MustCompile(
	`(?i)^(?P<Scheduled>\d{1,2}:\d{2} [ap]m) Departed (?P<Actual>\d{1,2}:\d{2} [ap]m) (?P<Vessel>.+)$`,
)

var scheduledSailingPattern = regexp.MustCompile(
	`(?i)^(?P<Scheduled>\d{1,2}:\d{2} [ap]m)(?P<Tomorrow> \(Tomorrow\))? (?P<Vessel>.+)$`,
)

var etaStatusPattern = regexp.MustCompile(`(?i)\beta\s*:`)

var sgiTerminalPattern = regexp.MustCompile(
	`(?i)(^to\s+|^via\s+|,\s*|\s+to\s+)` +
		`([[:alpha:]][[:alpha:]' -]*Island\s*\([[:alpha:]][[:alpha:]' -]+\))`,
)

type terminalDescription struct {
	Code         string
	TerminalName string
	IslandName   string
}

var terminalDescriptions = map[string]terminalDescription{
	"TSA": {Code: "TSA", TerminalName: "Tsawwassen"},
	"SWB": {Code: "SWB", TerminalName: "Swartz Bay"},
	"galiano island (sturdies bay)": {
		Code: "PSB", TerminalName: "Sturdies Bay", IslandName: "Galiano Island",
	},
	"mayne island (village bay)": {
		Code: "PVB", TerminalName: "Village Bay", IslandName: "Mayne Island",
	},
	"pender island (otter bay)": {
		Code: "POB", TerminalName: "Otter Bay", IslandName: "Pender Island",
	},
	"saturna island (lyall harbour)": {
		Code: "PST", TerminalName: "Lyall Harbour", IslandName: "Saturna Island",
	},
	"salt spring island (long harbour)": {
		Code: "PLH", TerminalName: "Long Harbour", IslandName: "Salt Spring Island",
	},
}

var vancouverLocation = mustLoadLocation("America/Vancouver")

func mustLoadLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("load timezone %s: %v", name, err))
	}
	return location
}

func normalizedText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func parseDepartedSailing(value string) (scheduled, actual, vessel string, ok bool) {
	matches := departedSailingPattern.FindStringSubmatch(normalizedText(value))
	if len(matches) != 4 {
		return "", "", "", false
	}
	return matches[1], matches[2], matches[3], true
}

func parseScheduledSailing(value string) (scheduled, vessel string, tomorrow, ok bool) {
	matches := scheduledSailingPattern.FindStringSubmatch(normalizedText(value))
	if len(matches) != 4 {
		return "", "", false, false
	}
	return matches[1], matches[3], matches[2] != "", true
}

func pacificServiceDate(observedAt time.Time, tomorrow bool) string {
	date := observedAt.In(vancouverLocation)
	if tomorrow {
		date = date.AddDate(0, 0, 1)
	}
	return date.Format("2006-01-02")
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func canonicalSailingIdentity(
	fromTerminalCode string,
	toTerminalCode string,
	serviceDate string,
	scheduledDepartureTime string,
	occurrence int,
) (sailingID string, scheduledDepartureAt string, ok bool) {
	if occurrence < 1 {
		return "", "", false
	}

	serviceDate = strings.TrimSpace(serviceDate)
	scheduledDepartureTime = strings.ToLower(normalizedText(scheduledDepartureTime))
	scheduledAt, err := time.ParseInLocation(
		"2006-01-02 3:04 pm",
		serviceDate+" "+scheduledDepartureTime,
		vancouverLocation,
	)
	if err != nil {
		return "", "", false
	}

	from := strings.ToUpper(strings.TrimSpace(fromTerminalCode))
	to := strings.ToUpper(strings.TrimSpace(toTerminalCode))
	if from == "" || to == "" {
		return "", "", false
	}

	return fmt.Sprintf(
			"bcf:v1:%s:%s-%s:%s:%02d",
			serviceDate,
			from,
			to,
			scheduledAt.Format("1504"),
			occurrence,
		),
		scheduledAt.Format(time.RFC3339),
		true
}

func parseSGIPortCalls(
	raw string,
	fromTerminalCode string,
	sailingID string,
	scheduledDepartureAt string,
) []models.PortCall {
	raw = normalizedText(raw)
	if raw == "" || sailingID == "" {
		return nil
	}

	origin, ok := terminalDescriptions[strings.ToUpper(strings.TrimSpace(fromTerminalCode))]
	if !ok {
		return nil
	}
	matches := sgiTerminalPattern.FindAllStringSubmatchIndex(raw, -1)
	if len(matches) == 0 {
		return nil
	}

	calls := []models.PortCall{{
		PortCallID:           fmt.Sprintf("%s:call:%02d:%s", sailingID, 0, origin.Code),
		Sequence:             0,
		TerminalCode:         origin.Code,
		TerminalName:         origin.TerminalName,
		IslandName:           origin.IslandName,
		Role:                 "origin",
		ScheduledDepartureAt: scheduledDepartureAt,
	}}

	lowerRaw := strings.ToLower(raw)
	destinationBoundary := strings.LastIndex(lowerRaw, " to ")
	if strings.HasPrefix(lowerRaw, "to ") {
		destinationBoundary = 0
	}

	for _, bounds := range matches {
		terminalStart, terminalEnd := bounds[4], bounds[5]
		terminalKey := strings.ToLower(normalizedText(raw[terminalStart:terminalEnd]))
		description, known := terminalDescriptions[terminalKey]
		if !known {
			// Keep the raw itinerary on the sailing, but do not manufacture a
			// partial ordered route when the operator introduces a new name.
			return nil
		}
		sequence := len(calls)
		role := "stop"
		if destinationBoundary >= 0 && terminalStart > destinationBoundary {
			role = "destination"
		}
		calls = append(calls, models.PortCall{
			PortCallID:   fmt.Sprintf("%s:call:%02d:%s", sailingID, sequence, description.Code),
			Sequence:     sequence,
			TerminalCode: description.Code,
			TerminalName: description.TerminalName,
			IslandName:   description.IslandName,
			Role:         role,
		})
	}
	return calls
}

/*
 * ScrapeCapacityRoutes
 *
 * Scrapes capacity routes
 *
 * @return void
 */
func ScrapeCapacityRoutes() {
	departureTerminals := staticdata.GetCapacityDepartureTerminals()
	destinationTerminals := staticdata.GetCapacityDestinationTerminals()

	for i := 0; i < len(departureTerminals); i++ {
		for j := 0; j < len(destinationTerminals[i]); j++ {
			link := MakeCurrentConditionsLink(departureTerminals[i], destinationTerminals[i][j])

			// Make HTTP GET request
			client := &http.Client{}
			req, err := http.NewRequest("GET", link, nil)
			if err != nil {
				log.Printf("ScrapeCapacityRoutes: failed to create request for %s: %v", link, err)
				continue
			}

			req.Header.Add("User-Agent", "Mozilla")
			response, err := client.Do(req)
			if err != nil {
				log.Printf("ScrapeCapacityRoutes: failed to fetch %s: %v", link, err)
				continue
			}

			defer response.Body.Close()

			document, err := goquery.NewDocumentFromReader(response.Body)
			if err != nil {
				log.Printf("ScrapeCapacityRoutes: failed to parse response from %s: %v", link, err)
				continue
			}

			ScrapeCapacityRoute(document, departureTerminals[i], destinationTerminals[i][j])
		}
	}
}

/*
 * ScrapeCapacityRoute
 *
 * Scrapes capacity data for a given route
 *
 * @param *goquery.Document document
 * @param string fromTerminalCode
 * @param string toTerminalCode
 *
 * @return void
 */
func ScrapeCapacityRoute(document *goquery.Document, fromTerminalCode string, toTerminalCode string) {
	route := parseCapacityRoute(document, fromTerminalCode, toTerminalCode, time.Now())

	sailingsJson, err := json.Marshal(route.Sailings)
	if err != nil {
		log.Printf("ScrapeCapacityRoute: failed to marshal sailings for route %s: %v", route.RouteCode, err)
		return
	}

	sqlStatement := `
		INSERT INTO capacity_routes (
			route_code,
			from_terminal_code,
			to_terminal_code,
			sailing_duration,
			sailings
		)
		VALUES
			($1, $2, $3, $4, $5) ON CONFLICT (route_code) DO
		UPDATE
		SET
			route_code = EXCLUDED.route_code,
			from_terminal_code = EXCLUDED.from_terminal_code,
			to_terminal_code = EXCLUDED.to_terminal_code,
			sailing_duration = EXCLUDED.sailing_duration,
			sailings = EXCLUDED.sailings
		WHERE
			capacity_routes.route_code = EXCLUDED.route_code`
	_, err = db.Conn.Exec(
		sqlStatement,
		route.RouteCode,
		route.FromTerminalCode,
		route.ToTerminalCode,
		route.SailingDuration,
		sailingsJson,
	)
	if err != nil {
		log.Printf("ScrapeCapacityRoute: failed to insert route %s: %v", route.RouteCode, err)
	}
}

func parseCapacityRoute(
	document *goquery.Document,
	fromTerminalCode string,
	toTerminalCode string,
	observedAt time.Time,
) models.CapacityRoute {
	route := models.CapacityRoute{
		RouteCode:        fromTerminalCode + toTerminalCode,
		ToTerminalCode:   toTerminalCode,
		FromTerminalCode: fromTerminalCode,
		Sailings:         []models.CapacitySailing{},
	}
	occurrences := make(map[string]int)

	document.Find("table.detail-departure-table").Each(func(i int, table *goquery.Selection) {
		table.Find("tbody").Each(func(j int, tbody *goquery.Selection) {
			tbody.Find("tr.mobile-friendly-row").Each(func(k int, row *goquery.Selection) {
				// Init sailing
				sailing := models.CapacitySailing{
					ServiceDate: pacificServiceDate(observedAt, false),
					ScrapedAt:   observedAt.UTC().Format(time.RFC3339),
				}
				// BC Ferries renders an SGI sailing's stopping pattern in the
				// immediately following row. Only inspect that sibling: searching
				// farther ahead can incorrectly borrow the next sailing's itinerary
				// when the operator omits one.
				itineraryRaw := normalizedText(row.NextFiltered("tr.sgi-row").Text())
				rowTextLower := strings.ToLower(row.Text())
				updatesText := normalizedText(row.Find("div.cc-message-updates").Text())
				hasETA := etaStatusPattern.MatchString(updatesText) || strings.Contains(updatesText, "...")

				row.Find("td").Each(func(l int, td *goquery.Selection) {
					// Handle explicitly cancelled rows
					if strings.Contains(rowTextLower, "cancelled") {
						sailing.SailingStatus = "cancelled"

						if l == 0 {
							// Scheduled time and vessel
							scheduled, vessel, tomorrow, ok := parseScheduledSailing(td.Text())
							if ok {
								sailing.DepartureTime = scheduled
								sailing.ScheduledDepartureTime = scheduled
								sailing.VesselName = vessel
								sailing.ServiceDate = pacificServiceDate(observedAt, tomorrow)
							}
						} else if l == 1 {
							// Capture reason if present under the red text block
							// Prefer the second <p> which often holds the reason
							reason := strings.TrimSpace(td.Find("div.text-red p").Eq(1).Text())
							if reason == "" {
								// Fallback to the whole red block text
								reason = strings.TrimSpace(td.Find("div.text-red").Text())
							}
							if reason != "" {
								sailing.VesselStatus = reason
							}
						}
					} else if strings.Contains(rowTextLower, "arrived") {
						sailing.SailingStatus = "past"

						if l == 0 {
							scheduled, actual, vessel, ok := parseDepartedSailing(td.Find("p").Text())

							if !ok {
								fmt.Println("No matches found, regex error")
							} else {
								sailing.DepartureTime = actual
								sailing.ScheduledDepartureTime = scheduled
								sailing.ActualDepartureTime = stringPointer(actual)
								sailing.VesselName = vessel
							}
						} else if l == 1 {
							arrivalString := td.Find("div.cc-message-updates").Text()

							re := regexp.MustCompile(`(?i)arrived\s*:\s*(?P<ArrivalTime>\d{1,2}:\d{2} [ap]m)`)

							// Find the matches
							matches := re.FindStringSubmatch(strings.Join(strings.Fields(arrivalString), " "))

							if len(matches) == 0 {
								fmt.Println("No matches found, regex error")
							} else {
								// Extracting named group
								arrivalTime := matches[1]

								sailing.ArrivalTime = arrivalTime
								sailing.ActualArrivalTime = stringPointer(arrivalTime)
							}
						}
					} else if hasETA {
						sailing.SailingStatus = "current"

						if l == 0 {
							scheduled, actual, vessel, ok := parseDepartedSailing(td.Find("p").Text())

							if !ok {
								fmt.Println("No matches found, regex error")
							} else {
								sailing.DepartureTime = actual
								sailing.ScheduledDepartureTime = scheduled
								sailing.ActualDepartureTime = stringPointer(actual)
								sailing.VesselName = vessel
							}
						} else if l == 1 {
							etaString := td.Find("div.cc-message-updates").Text()

							re := regexp.MustCompile(`(?i)eta\s*:\s*(?P<ETA>\d{1,2}:\d{2} [ap]m|Variable)`)

							// Find the matches
							matches := re.FindStringSubmatch(strings.Join(strings.Fields(etaString), " "))

							if len(matches) == 0 {
								sailing.ArrivalTime = "..."
							} else {
								// Extracting named group
								etaTime := matches[1]

								sailing.ArrivalTime = etaTime
								sailing.EstimatedArrivalTime = stringPointer(etaTime)
							}
						}
					} else {
						sailing.SailingStatus = "future"

						if l == 0 {
							// schedule time, vessel
							scheduled, vessel, tomorrow, ok := parseScheduledSailing(td.Text())

							if !ok {
								fmt.Println("No matches found, regex error")
							} else {
								sailing.DepartureTime = scheduled
								sailing.ScheduledDepartureTime = scheduled
								sailing.VesselName = vessel
								sailing.ServiceDate = pacificServiceDate(observedAt, tomorrow)
							}
						} else if l == 1 {
							// details link
							// if word "Details" is in row, request from link, otherwise take percentage
							fillDetailsString := td.Text()

							if strings.Contains(fillDetailsString, "Details") {
								td.Find("a.vehicle-info-link").Each(func(m int, s *goquery.Selection) {
									href, exists := s.Attr("href")
									link := strings.ReplaceAll("https://www.bcferries.com"+href, " ", "%20")

									if exists {
										client := &http.Client{}
										req, err := http.NewRequest("GET", link, nil)
										if err != nil {
											log.Printf("ScrapeCapacityRoute: failed to create details request for %s: %v", link, err)
											return
										}

										req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
										response, err := client.Do(req)
										if err != nil {
											log.Printf("ScrapeCapacityRoute: failed to fetch details from %s: %v", link, err)
											return
										}

										defer response.Body.Close()

										fillDocument, err := goquery.NewDocumentFromReader(response.Body)
										if err != nil {
											log.Printf("ScrapeCapacityRoute: failed to parse fill details from %s: %v", link, err)
											return
										}

										// fmt.Println(fillDocument.Text())
										fillDocument.Find("p.vehicle-icon-text").Each(func(o int, percentageText *goquery.Selection) {
											if o == 0 {
												fillPercentage := strings.TrimSpace(percentageText.Text())

												if strings.Contains(strings.ToLower(fillPercentage), "full") {
													sailing.Fill = 100
													sailing.CarFill = 100
													sailing.OversizeFill = 100
												} else {
													fillPercentageInt, err := strconv.Atoi(strings.ReplaceAll(fillPercentage, "%", ""))
													if err != nil {
														// ... handle error
													}

													sailing.Fill = 100 - fillPercentageInt
												}
											} else if o == 1 {
												fillPercentage := strings.TrimSpace(percentageText.Text())

												if strings.Contains(strings.ToLower(fillPercentage), "full") {
													sailing.CarFill = 100
												} else {
													fillPercentageInt, err := strconv.Atoi(strings.ReplaceAll(fillPercentage, "%", ""))
													if err != nil {
														// ... handle error
													}

													sailing.CarFill = 100 - fillPercentageInt
												}
											} else if o == 2 {
												fillPercentage := strings.TrimSpace(percentageText.Text())

												if strings.Contains(strings.ToLower(fillPercentage), "full") {
													sailing.OversizeFill = 100
												} else {
													fillPercentageInt, err := strconv.Atoi(strings.ReplaceAll(fillPercentage, "%", ""))
													if err != nil {
														// ... handle error
													}

													sailing.OversizeFill = 100 - fillPercentageInt
												}
											}
										})

									}
								})
							} else {
								if strings.Contains(strings.ToLower(fillDetailsString), "full") {
									sailing.Fill = 100
									sailing.CarFill = 100
									sailing.OversizeFill = 100
								} else {
									fillPercentage := strings.TrimSpace(td.Find("span.cc-vessel-percent-full").Text())

									fillPercentageInt, err := strconv.Atoi(strings.ReplaceAll(fillPercentage, "%", ""))
									if err != nil {
										// ... handle error
									}

									sailing.Fill = 100 - fillPercentageInt
								}
							}
						}
					}
				})

				if sailing.ScheduledDepartureTime == "" || sailing.VesselName == "" {
					log.Printf(
						"parseCapacityRoute: skipping unparseable sailing row for route %s: %q",
						route.RouteCode,
						normalizedText(row.Text()),
					)
					return
				}

				occurrenceKey := sailing.ServiceDate + "\x00" + sailing.ScheduledDepartureTime
				occurrences[occurrenceKey]++
				sailingID, scheduledDepartureAt, ok := canonicalSailingIdentity(
					route.FromTerminalCode,
					route.ToTerminalCode,
					sailing.ServiceDate,
					sailing.ScheduledDepartureTime,
					occurrences[occurrenceKey],
				)
				if !ok {
					log.Printf(
						"parseCapacityRoute: skipping sailing with invalid canonical identity for route %s: %q",
						route.RouteCode,
						normalizedText(row.Text()),
					)
					return
				}
				sailing.SailingID = sailingID
				sailing.ScheduledDepartureAt = scheduledDepartureAt
				sailing.ScheduleSource = "bc-ferries-current-conditions"
				sailing.ScheduleSourceURL = MakeCurrentConditionsLink(
					route.FromTerminalCode,
					route.ToTerminalCode,
				)
				sailing.ScheduleScrapedAt = sailing.ScrapedAt
				sailing.OperationalSource = "bc-ferries-current-conditions"
				if itineraryRaw != "" {
					sailing.ItineraryRaw = itineraryRaw
					sailing.ItinerarySource = "bc-ferries-current-conditions"
					sailing.ItineraryObservedAt = sailing.ScrapedAt
					sailing.PortCalls = parseSGIPortCalls(
						itineraryRaw,
						route.FromTerminalCode,
						sailing.SailingID,
						sailing.ScheduledDepartureAt,
					)
				}

				// Add sailing to route
				route.Sailings = append(route.Sailings, sailing)
			})
		})
	})

	// Try to find sailing duration text in a case-insensitive way
	sailingDuration := ""
	document.Find("span").Each(func(_ int, s *goquery.Selection) {
		if sailingDuration != "" {
			return
		}
		txt := strings.ReplaceAll(s.Text(), "\u00A0", " ")
		if strings.Contains(strings.ToLower(txt), "sailing duration:") {
			sailingDuration = txt
		}
	})
	sailingDuration = strings.ReplaceAll(sailingDuration, "Sailing duration:", "")
	sailingDuration = strings.ReplaceAll(sailingDuration, "sailing duration:", "")
	sailingDuration = strings.TrimSpace(sailingDuration)
	route.SailingDuration = sailingDuration
	return route
}

// ScrapeOfficialCapacitySchedules stores today's and tomorrow's published
// daily timetables for capacity routes. A failed or redirected scrape never
// deletes the last known-good baseline.
func ScrapeOfficialCapacitySchedules() {
	ctx, cancel := newBrowserContext(context.Background())
	defer cancel()

	departures := staticdata.GetCapacityDepartureTerminals()
	destinations := staticdata.GetCapacityDestinationTerminals()
	now := time.Now()
	serviceDate := now.In(vancouverLocation)

	for dayOffset := 0; dayOffset <= 1; dayOffset++ {
		requestedDate := serviceDate.AddDate(0, 0, dayOffset)
		for i, departure := range departures {
			for _, destination := range destinations[i] {
				// These routes publish their authoritative baseline in seasonal
				// tables. The daily endpoints either redirect or omit their
				// physical island calls, so a separate adapter handles them below.
				if destination == "SGI" || (departure == "HSB" && destination == "BOW") {
					continue
				}
				sourceURL := MakeScheduleLinkForDate(departure, destination, requestedDate)
				// Isolate each navigation in a child tab. Cancelling a timeout on
				// the shared browser context terminates every later route in the
				// generation.
				pageCtx, pageCancel := chromedp.NewContext(ctx)
				requestCtx, requestCancel := context.WithTimeout(pageCtx, 45*time.Second)
				html, finalURL, err := fetchWithChromedp(requestCtx, sourceURL)
				requestCancel()
				pageCancel()
				if err != nil {
					log.Printf("ScrapeOfficialCapacitySchedules: fetch failed for %s: %v", sourceURL, err)
					continue
				}

				document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
				if err != nil {
					log.Printf("ScrapeOfficialCapacitySchedules: parse failed for %s: %v", sourceURL, err)
					continue
				}
				if !isDailySchedulePage(document, finalURL) {
					log.Printf("ScrapeOfficialCapacitySchedules: rejected non-daily response for %s (final URL %s)", sourceURL, finalURL)
					continue
				}

				route, ok := parseOfficialScheduleRoute(
					document,
					departure,
					destination,
					requestedDate.Format("2006-01-02"),
					sourceURL,
					now,
				)
				if !ok {
					log.Printf("ScrapeOfficialCapacitySchedules: rejected incomplete schedule for %s", sourceURL)
					continue
				}
				if err := persistOfficialScheduleRoute(route); err != nil {
					log.Printf("ScrapeOfficialCapacitySchedules: persist failed for %s: %v", sourceURL, err)
				}
			}
		}
	}

	scrapeOfficialSeasonalCapacitySchedules(ctx, serviceDate, now)
}

func parseOfficialScheduleRoute(
	document *goquery.Document,
	fromTerminalCode string,
	toTerminalCode string,
	expectedServiceDate string,
	sourceURL string,
	observedAt time.Time,
) (models.OfficialScheduleRoute, bool) {
	route := models.OfficialScheduleRoute{
		RouteCode:        fromTerminalCode + toTerminalCode,
		FromTerminalCode: fromTerminalCode,
		ToTerminalCode:   toTerminalCode,
		ServiceDate:      expectedServiceDate,
		Sailings:         []models.OfficialScheduleSailing{},
		SourceURL:        sourceURL,
		ScrapedAt:        observedAt.UTC().Format(time.RFC3339),
	}
	table := document.Find("#dailyScheduleTableOnward").First()
	if table.Length() == 0 {
		return route, false
	}

	timePattern := regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}\s*[ap]m\b`)
	durationPattern := regexp.MustCompile(`\b\d{1,2}:\d{2}\b`)
	occurrences := make(map[string]int)
	invalidRows := 0

	table.Find("tbody").First().ChildrenFiltered("tr.schedule-table-row").Each(func(_ int, row *goquery.Selection) {
		if row.Find("th").Length() > 0 {
			return
		}
		rowText := strings.ToLower(normalizedText(row.Text()))
		if strings.Contains(rowText, "dangerous goods only") || strings.Contains(rowText, "no passengers permitted") {
			return
		}

		departureCell := row.Find("td[data-sort]").First()
		dateSort, exists := departureCell.Attr("data-sort")
		if !exists {
			invalidRows++
			return
		}
		departureAt, err := time.ParseInLocation("01/02/2006 15:04:05", strings.TrimSpace(dateSort), vancouverLocation)
		if err != nil || departureAt.Format("2006-01-02") != expectedServiceDate {
			invalidRows++
			return
		}

		var clockTimes []string
		row.ChildrenFiltered("td").Each(func(_ int, cell *goquery.Selection) {
			if value := timePattern.FindString(normalizedText(cell.Text())); value != "" {
				clockTimes = append(clockTimes, strings.ToLower(normalizedText(value)))
			}
		})
		if len(clockTimes) < 2 {
			invalidRows++
			return
		}

		arrivalClock, err := time.ParseInLocation(
			"2006-01-02 3:04 pm",
			expectedServiceDate+" "+clockTimes[1],
			vancouverLocation,
		)
		if err != nil {
			invalidRows++
			return
		}
		if arrivalClock.Before(departureAt) {
			arrivalClock = arrivalClock.AddDate(0, 0, 1)
		}

		departureTime := departureAt.Format("3:04 pm")
		occurrenceKey := expectedServiceDate + "\x00" + departureTime
		occurrences[occurrenceKey]++
		sailingID, scheduledDepartureAt, ok := canonicalSailingIdentity(
			fromTerminalCode,
			toTerminalCode,
			expectedServiceDate,
			departureTime,
			occurrences[occurrenceKey],
		)
		if !ok {
			invalidRows++
			return
		}

		if route.SailingDuration == "" {
			row.ChildrenFiltered("td").Each(func(_ int, cell *goquery.Selection) {
				if route.SailingDuration != "" {
					return
				}
				text := normalizedText(cell.Text())
				if strings.Contains(strings.ToLower(text), "am") || strings.Contains(strings.ToLower(text), "pm") {
					return
				}
				route.SailingDuration = durationPattern.FindString(text)
			})
		}

		route.Sailings = append(route.Sailings, models.OfficialScheduleSailing{
			SailingID:              sailingID,
			ServiceDate:            expectedServiceDate,
			ScheduledDepartureTime: departureTime,
			ScheduledArrivalTime:   arrivalClock.Format("3:04 pm"),
			ScheduledDepartureAt:   scheduledDepartureAt,
			ScheduledArrivalAt:     arrivalClock.Format(time.RFC3339),
		})
	})

	// Reject partial pages. One malformed published row is safer to retain in
	// the previous generation than to silently publish an incomplete timetable.
	if len(route.Sailings) == 0 || invalidRows > 0 || route.SailingDuration == "" {
		return route, false
	}
	return route, true
}

func persistOfficialScheduleRoute(route models.OfficialScheduleRoute) error {
	sailingsJSON, err := json.Marshal(route.Sailings)
	if err != nil {
		return fmt.Errorf("marshal official sailings: %w", err)
	}

	_, err = db.Conn.Exec(`
		INSERT INTO official_schedule_routes (
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
		return fmt.Errorf("upsert official route %s on %s: %w", route.RouteCode, route.ServiceDate, err)
	}
	return nil
}

/*
 * ScrapeNonCapacityRoutes
 *
 * Scrapes non-capacity routes
 *
 * @return void
 */
func ScrapeNonCapacityRoutes() {
	ctx, cancel := newBrowserContext(context.Background())
	defer cancel()

	departureTerminals := staticdata.GetNonCapacityDepartureTerminals()
	destinationTerminals := staticdata.GetNonCapacityDestinationTerminals()

	for i := 0; i < len(departureTerminals); i++ {
		for j := 0; j < len(destinationTerminals[i]); j++ {
			departure := departureTerminals[i]
			destination := destinationTerminals[i][j]

			dailyLink := MakeScheduleLink(departure, destination)
			html, finalURL, err := fetchWithChromedp(ctx, dailyLink)
			if err == nil {
				document, parseErr := goquery.NewDocumentFromReader(strings.NewReader(html))
				if parseErr == nil {
					isDaily := isDailySchedulePage(document, finalURL)
					if ScrapeNonCapacityRoute(document, departure, destination, isDaily) {
						continue
					}
				} else {
					log.Printf("ScrapeNonCapacityRoutes: failed to parse daily HTML for %s: %v", dailyLink, parseErr)
				}
			} else {
				log.Printf("ScrapeNonCapacityRoutes: daily fetch failed for %s: %v", dailyLink, err)
			}

			seasonalLink := MakeSeasonalScheduleLink(departure, destination)
			html, _, err = fetchWithChromedp(ctx, seasonalLink)
			if err != nil {
				log.Printf("ScrapeNonCapacityRoutes: seasonal fetch failed for %s: %v", seasonalLink, err)
				continue
			}

			document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
			if err != nil {
				log.Printf("ScrapeNonCapacityRoutes: failed to parse seasonal HTML for %s: %v", seasonalLink, err)
				continue
			}

			ScrapeNonCapacityRoute(document, departure, destination, false)
		}
	}
}

func newBrowserContext(parent context.Context) (context.Context, context.CancelFunc) {
	options := append([]chromedp.ExecAllocatorOption(nil), chromedp.DefaultExecAllocatorOptions[:]...)
	if strings.EqualFold(os.Getenv("CHROME_NO_SANDBOX"), "true") {
		// Containers already provide the process boundary. Self-hosted Compose
		// further runs this process non-root, read-only, without Linux
		// capabilities, and with no-new-privileges. This flag is never enabled
		// implicitly for local or non-container execution.
		options = append(options, chromedp.NoSandbox)
	}

	allocatorContext, allocatorCancel := chromedp.NewExecAllocator(parent, options...)
	browserContext, browserCancel := chromedp.NewContext(allocatorContext)
	return browserContext, func() {
		browserCancel()
		allocatorCancel()
	}
}

/*
 * ScrapeNonCapacityRoute
 *
 * Scrapes schedule data for a given route
 *
 * @param *goquery.Document document
 * @param string fromTerminalCode
 * @param string toTerminalCode
 * @param bool isDaily
 *
 * @return bool - True when route data was parsed and persisted
 */
func ScrapeNonCapacityRoute(document *goquery.Document, fromTerminalCode, toTerminalCode string, isDaily bool) bool {
	loc, err := time.LoadLocation("America/Vancouver")
	if err != nil {
		log.Printf("ScrapeNonCapacityRoute: failed to load PT location: %v", err)
		return false
	}

	normalizeDay := func(s string) string {
		s = strings.TrimSpace(strings.ToUpper(s))
		// Treat trailing "S" as optional: MONDAY == MONDAYS
		if strings.HasSuffix(s, "S") {
			s = strings.TrimSuffix(s, "S")
		}
		return s
	}
	todayNorm := normalizeDay(time.Now().In(loc).Weekday().String()) // e.g. "MONDAY"

	route := models.NonCapacityRoute{
		RouteCode:        fromTerminalCode + toTerminalCode,
		FromTerminalCode: fromTerminalCode,
		ToTerminalCode:   toTerminalCode,
		Sailings:         []models.NonCapacitySailing{},
	}
	sailingDuration := ""
	if isDaily {
		if dailySailings, dailyDuration, ok := parseDailyScheduleSailings(document); ok {
			route.Sailings = dailySailings
			sailingDuration = dailyDuration
		}
	}

	if len(route.Sailings) == 0 {
		// ---- Step 1: find the seasonal schedule table that contains weekday theads
		var scheduleTable *goquery.Selection
		document.Find("table.table-seasonal-schedule").Each(func(_ int, t *goquery.Selection) {
			if scheduleTable != nil {
				return
			}
			// Heuristic: a real schedule table has thead rows with day labels
			if t.Find("thead tr[data-schedule-day], thead [data-schedule-day], thead h4, thead b").Length() > 0 {
				scheduleTable = t
			}
		})
		// Fallback to the historical assumption (2nd table) if heuristic fails
		if scheduleTable == nil {
			scheduleTable = document.Find("table.table-seasonal-schedule").Eq(1)
		}
		if scheduleTable == nil || scheduleTable.Length() == 0 {
			log.Printf("ScrapeNonCapacityRoute: seasonal schedule table not found")
			return false
		}

		// ---- Step 2: find the <thead> whose day matches today (MONDAY vs MONDAYS, any case)
		var dayBody *goquery.Selection
		scheduleTable.Find("thead").Each(func(_ int, thead *goquery.Selection) {
			if dayBody != nil {
				return
			}

			// Prefer the attribute if present.
			dayAttr := thead.Find("tr").First().AttrOr("data-schedule-day", "")
			dayAttrNorm := normalizeDay(dayAttr)

			match := (dayAttrNorm != "" && dayAttrNorm == todayNorm)
			if !match {
				// Fallback: try visible text inside thead (e.g., MONDAY Depart)
				txt := thead.Find("h4, b, th").First().Text()
				txtNorm := normalizeDay(txt)
				// If the text contains the weekday token (e.g., "MONDAY DEPART"), accept it.
				match = (txtNorm == todayNorm) || strings.Contains(txtNorm, todayNorm)
			}

			if match {
				// ---- Step 3: go to the NEXT sibling under the table; skip to the first <tbody>
				tb := thead.Next()
				for tb.Length() > 0 && goquery.NodeName(tb) != "tbody" {
					tb = tb.Next()
				}
				if tb.Length() > 0 && goquery.NodeName(tb) == "tbody" {
					dayBody = tb
				}
			}
		})

		if dayBody == nil {
			log.Printf("ScrapeNonCapacityRoute: no tbody found for today (%s) in second table", todayNorm)
			return false
		}

		clean := func(s string) string {
			s = strings.ReplaceAll(s, "\u00a0", " ") // NBSP -> space
			return strings.TrimSpace(s)
		}

		// Parse a list of month/day mentions from a status string like
		// "Only on Sep 14, 28 & Oct 12" or "Except on Oct 13".
		// Returns a set keyed by "MM-DD" for quick lookup.
		parseMentionedDates := func(note string, year int) map[string]struct{} {
			res := make(map[string]struct{})
			if note == "" {
				return res
			}
			lower := strings.ToLower(note)

			monthMap := map[string]time.Month{
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

			// 1) Find explicit Month Day pairs
			mdRe := regexp.MustCompile(`(?i)(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\s+(\d{1,2})`)
			matches := mdRe.FindAllStringSubmatch(lower, -1)

			for _, m := range matches {
				monKey := m[1]
				dayStr := m[2]
				if mon, ok := monthMap[monKey]; ok {
					if d, err := strconv.Atoi(dayStr); err == nil {
						key := fmt.Sprintf("%02d-%02d", int(mon), d)
						res[key] = struct{}{}
					}
				}
			}

			// 2) Handle shorthand days following a month (e.g., "Sep 14, 28 & Oct 12")
			//    For each segment that starts with a month, capture trailing , <day> pieces until next month appears
			segRe := regexp.MustCompile(`(?i)(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\s+\d{1,2}([^a-z]*)`)
			pos := 0
			for {
				loc := segRe.FindStringSubmatchIndex(lower[pos:])
				if loc == nil {
					break
				}
				// Extract month for this segment
				seg := lower[pos+loc[0] : pos+loc[1]]
				mon := mdRe.FindStringSubmatch(seg)
				if len(mon) >= 3 {
					monKey := mon[1]
					if monVal, ok := monthMap[monKey]; ok {
						// After the first "Month DD", scan the tail for , DD patterns
						tail := seg[len(mon[0]):]
						// Match bare days like ", 28" without unsupported lookaheads
						ddRe := regexp.MustCompile(`(?i)[,&\s]+(\d{1,2})\b`)
						ddMatches := ddRe.FindAllStringSubmatch(tail, -1)
						for _, dm := range ddMatches {
							if d, err := strconv.Atoi(dm[1]); err == nil {
								key := fmt.Sprintf("%02d-%02d", int(monVal), d)
								res[key] = struct{}{}
							}
						}
					}
				}
				pos += loc[1]
			}

			return res
		}

		// ---- Step 4: parse rows in the found <tbody>
		dayBody.Find("tr.schedule-table-row").Each(func(_ int, row *goquery.Selection) {
			tds := row.Find("td")
			if tds.Length() < 3 {
				return
			}

			// Extract clean departure time (first time token) and any status notes
			depCell := tds.Eq(1)
			depRaw := clean(depCell.Text())

			// Capture red/black status notes if present (e.g., Only on..., Except on..., Foot passengers only, Dangerous goods only)
			var statuses []string
			var redNotes []string
			depCell.Find("p").Each(func(_ int, p *goquery.Selection) {
				txt := clean(p.Text())
				if txt == "" {
					return
				}
				// Only keep informative notes, skip if it's just whitespace
				// Common classes include red-text italic-style or text-black
				if p.HasClass("red-text") || p.HasClass("text-black") {
					statuses = append(statuses, txt)
					if p.HasClass("red-text") {
						redNotes = append(redNotes, txt)
					}
				}
			})

			// Extract the first time-like token from the departure cell
			depTime := depRaw
			if re := regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}\s*[ap]m\b`); re != nil {
				if m := re.FindString(depRaw); m != "" {
					depTime = m
				}
			}

			// Extract clean arrival time (first time token)
			arrCell := tds.Eq(2)
			arrRaw := clean(arrCell.Text())
			arrTime := arrRaw
			if re := regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}\s*[ap]m\b`); re != nil {
				if m := re.FindString(arrRaw); m != "" {
					arrTime = m
				}
			}

			// Filter: drop dangerous goods only sailings outright
			depLower := strings.ToLower(depCell.Text())
			if strings.Contains(depLower, "dangerous goods only") || strings.Contains(depLower, "no passengers permitted") {
				return
			}

			// Apply exception rules: "Only on <dates>" and "Except on <dates>"
			// Build a combined note string from red notes
			combinedRed := strings.ToLower(strings.Join(redNotes, "; "))
			today := time.Now().In(loc)
			todayKey := fmt.Sprintf("%02d-%02d", int(today.Month()), today.Day())

			// If there is an "only on" note, include only if today is listed
			if strings.Contains(combinedRed, "only on") {
				dates := parseMentionedDates(combinedRed, today.Year())
				if _, ok := dates[todayKey]; !ok {
					return
				}
			}
			// BC Ferries uses both phrases for date-specific exclusions.
			if strings.Contains(combinedRed, "except on") || strings.Contains(combinedRed, "not available on") {
				dates := parseMentionedDates(combinedRed, today.Year())
				if _, ok := dates[todayKey]; ok {
					return
				}
			}

			s := models.NonCapacitySailing{
				DepartureTime: depTime,
				ArrivalTime:   arrTime,
			}
			if len(statuses) > 0 {
				s.VesselStatus = strings.Join(statuses, " | ")
			}

			if s.DepartureTime != "" || s.ArrivalTime != "" {
				route.Sailings = append(route.Sailings, s)
			}
		})

		// Optional: route-level duration (from the first row's 4th cell, if present)
		if firstRow := dayBody.Find("tr.schedule-table-row").First(); firstRow.Length() > 0 {
			if cell := firstRow.Find("td").Eq(3); cell.Length() > 0 {
				sailingDuration = clean(cell.Text())
			}
		}
	}

	if len(route.Sailings) == 0 {
		log.Printf("ScrapeNonCapacityRoute: no sailings parsed for %s", route.RouteCode)
		return false
	}

	// Defensive final check: malformed or repeated markup must not leak duplicate
	// sailings into the API response.
	uniqueSailings := make([]models.NonCapacitySailing, 0, len(route.Sailings))
	seenSailings := make(map[string]struct{}, len(route.Sailings))
	for _, sailing := range route.Sailings {
		if sailing.DepartureTime == "" || sailing.ArrivalTime == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(sailing.DepartureTime)) + "|" +
			strings.ToLower(strings.TrimSpace(sailing.ArrivalTime))
		if _, seen := seenSailings[key]; seen {
			continue
		}
		seenSailings[key] = struct{}{}
		uniqueSailings = append(uniqueSailings, sailing)
	}
	route.Sailings = uniqueSailings
	if len(route.Sailings) == 0 {
		log.Printf("ScrapeNonCapacityRoute: no valid sailings parsed for %s", route.RouteCode)
		return false
	}

	// ---- Step 5: save
	sailingsJSON, err := json.Marshal(route.Sailings)
	if err != nil {
		log.Printf("ScrapeNonCapacityRoute: marshal error for %s: %v", route.RouteCode, err)
		return false
	}

	sqlStatement := `
		INSERT INTO non_capacity_routes (
			route_code, 
			from_terminal_code, 
			to_terminal_code, 
			sailing_duration,
			sailings
		) 
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (route_code) DO UPDATE SET
			from_terminal_code = EXCLUDED.from_terminal_code, 
			to_terminal_code = EXCLUDED.to_terminal_code,
			sailing_duration = EXCLUDED.sailing_duration,
			sailings = EXCLUDED.sailings 
	`
	_, err = db.Conn.Exec(sqlStatement,
		route.RouteCode, route.FromTerminalCode, route.ToTerminalCode, sailingDuration, sailingsJSON,
	)
	if err != nil {
		log.Printf("ScrapeNonCapacityRoute: DB insert/update failed for %s: %v", route.RouteCode, err)
		return false
	}

	return true
}

func parseDailyScheduleSailings(document *goquery.Document) ([]models.NonCapacitySailing, string, bool) {
	clean := func(s string) string {
		s = strings.ReplaceAll(s, "\u00a0", " ") // NBSP -> space
		return strings.TrimSpace(s)
	}
	timeRe := regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}\s*[ap]m\b`)
	durationRe := regexp.MustCompile(`\b\d{1,2}:\d{2}\b`)
	extractTime := func(s string) string {
		return timeRe.FindString(clean(s))
	}

	var sailings []models.NonCapacitySailing
	sailingDuration := ""

	table := document.Find("#dailyScheduleTableOnward").First()
	if table.Length() == 0 {
		return sailings, sailingDuration, false
	}

	func() {

		tableSailings := make([]models.NonCapacitySailing, 0)
		tableDuration := ""

		rows := table.Find("tbody").First().ChildrenFiltered("tr.schedule-table-row")

		rows.Each(func(_ int, row *goquery.Selection) {
			if row.Find("th").Length() > 0 {
				return
			}

			tds := row.ChildrenFiltered("td")
			if tds.Length() < 2 {
				return
			}

			rowTextLower := strings.ToLower(clean(row.Text()))
			if strings.Contains(rowTextLower, "dangerous goods only") || strings.Contains(rowTextLower, "no passengers permitted") {
				return
			}

			var timeTokens []string
			tds.Each(func(_ int, td *goquery.Selection) {
				if m := extractTime(td.Text()); m != "" {
					timeTokens = append(timeTokens, m)
				}
			})
			if len(timeTokens) < 2 {
				return
			}
			departureTime := timeTokens[0]
			arrivalTime := ""
			if len(timeTokens) > 1 {
				arrivalTime = timeTokens[1]
			}

			if tableDuration == "" {
				tds.Each(func(_ int, td *goquery.Selection) {
					if tableDuration != "" {
						return
					}
					tdText := clean(td.Text())
					if tdText == "" {
						return
					}
					tdTextLower := strings.ToLower(tdText)
					if strings.Contains(tdTextLower, "am") || strings.Contains(tdTextLower, "pm") {
						return
					}
					if m := durationRe.FindString(tdText); m != "" {
						tableDuration = m
					}
				})
			}

			tableSailings = append(tableSailings, models.NonCapacitySailing{
				DepartureTime: departureTime,
				ArrivalTime:   arrivalTime,
			})
		})

		sailings = tableSailings
		sailingDuration = tableDuration
	}()

	return sailings, sailingDuration, len(sailings) > 0
}

/********************/
/* Helper Functions */
/********************/

/*
 * fetchWithChromedp
 *
 * Uses a headless Chrome browser to fetch and render the full HTML content of a given URL.
 * This is used to bypass JavaScript-based protections like Queue-it by executing the page
 * in a real browser environment.
 *
 * @param string url - The URL to navigate to
 *
 * @return string - The full outer HTML of the rendered page
 * @return error - Any error encountered during navigation or retrieval
 */
func fetchWithChromedp(ctx context.Context, url string) (string, string, error) {
	var html string
	var finalURL string
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Location(&finalURL),
		chromedp.OuterHTML("html", &html),
	)

	return html, finalURL, err
}

// isDailySchedulePage detects redirects from a daily URL to a seasonal page.
// The final browser URL is authoritative; canonical is a fallback for fixtures
// and pages whose client-side navigation leaves an ambiguous location.
func isDailySchedulePage(document *goquery.Document, finalURL string) bool {
	finalURL = strings.ToLower(finalURL)
	if strings.Contains(finalURL, "/schedules/seasonal/") {
		return false
	}
	if strings.Contains(finalURL, "/schedules/daily/") {
		return true
	}

	canonical, _ := document.Find(`link[rel="canonical"]`).First().Attr("href")
	canonical = strings.ToLower(canonical)
	return strings.Contains(canonical, "/schedules/daily/")
}
