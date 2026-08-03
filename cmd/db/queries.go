package db

import (
	"database/sql"
	"encoding/json"
	"log"
	"sort"
	"time"

	"github.com/samuel-pratt/bc-ferries-api/cmd/models"
)

type CapacityDataHealth struct {
	OperationalRoutes        int
	OperationalSailings      int
	OldestOperationalScrape  sql.NullTime
	OfficialTodayRoutes      int
	OfficialTodaySailings    int
	OfficialTomorrowRoutes   int
	OfficialTomorrowSailings int
	OldestOfficialScrape     sql.NullTime
}

/*
 * GetCapacitySailings
 *
 * Retrieves all capacity route records from the database, including parsed sailing data.
 *
 * Queries the `capacity_routes` table and unmarshals the `sailings` JSON column
 * into a slice of `models.CapacitySailing` for each route.
 *
 * @return []models.CapacityRoute - a slice of capacity routes with their sailings
 */
func GetCapacitySailings() []models.CapacityRoute {
	return mergeCapacityRoutes(getOperationalCapacitySailings(), getOfficialScheduleRoutes())
}

func getOperationalCapacitySailings() []models.CapacityRoute {
	var routes []models.CapacityRoute

	sqlStatement := `
		SELECT route_code, from_terminal_code, to_terminal_code, sailing_duration, sailings
		FROM capacity_routes
	`

	rows, err := Conn.Query(sqlStatement)
	if err != nil {
		log.Printf("GetCapacitySailings: query failed: %v", err)
		return routes
	}
	defer rows.Close()

	for rows.Next() {
		var route models.CapacityRoute
		var sailings []uint8

		err := rows.Scan(&route.RouteCode, &route.FromTerminalCode, &route.ToTerminalCode, &route.SailingDuration, &sailings)
		if err != nil {
			log.Printf("GetCapacitySailings: row scan failed: %v", err)
			continue
		}

		var content []models.CapacitySailing
		if err := json.Unmarshal(sailings, &content); err != nil {
			log.Printf("GetCapacitySailings: JSON unmarshal failed: %v", err)
			continue
		}

		route.Sailings = content
		routes = append(routes, route)
	}

	if err := rows.Err(); err != nil {
		log.Printf("GetCapacitySailings: row iteration error: %v", err)
	}

	return routes
}

func getOfficialScheduleRoutes() []models.OfficialScheduleRoute {
	var routes []models.OfficialScheduleRoute
	rows, err := Conn.Query(`
		SELECT route_code, from_terminal_code, to_terminal_code, service_date,
		       sailing_duration, sailings, source_url, scraped_at
		FROM official_schedule_routes
		WHERE service_date BETWEEN
		      (CURRENT_TIMESTAMP AT TIME ZONE 'America/Vancouver')::date
		      AND (CURRENT_TIMESTAMP AT TIME ZONE 'America/Vancouver')::date + 1
		ORDER BY route_code, service_date
	`)
	if err != nil {
		log.Printf("getOfficialScheduleRoutes: query failed: %v", err)
		return routes
	}
	defer rows.Close()

	for rows.Next() {
		var route models.OfficialScheduleRoute
		var serviceDate time.Time
		var scrapedAt time.Time
		var sailings []byte
		if err := rows.Scan(
			&route.RouteCode,
			&route.FromTerminalCode,
			&route.ToTerminalCode,
			&serviceDate,
			&route.SailingDuration,
			&sailings,
			&route.SourceURL,
			&scrapedAt,
		); err != nil {
			log.Printf("getOfficialScheduleRoutes: row scan failed: %v", err)
			continue
		}

		if err := json.Unmarshal(sailings, &route.Sailings); err != nil {
			log.Printf("getOfficialScheduleRoutes: JSON unmarshal failed: %v", err)
			continue
		}
		route.ServiceDate = serviceDate.Format("2006-01-02")
		route.ScrapedAt = scrapedAt.UTC().Format(time.RFC3339)
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		log.Printf("getOfficialScheduleRoutes: row iteration error: %v", err)
	}
	return routes
}

func GetCapacityDataHealth() (CapacityDataHealth, error) {
	var health CapacityDataHealth
	if err := Conn.QueryRow(`
		SELECT COUNT(DISTINCT route.route_code), COUNT(sailing),
		       MIN(NULLIF(sailing->>'scrapedAt', '')::timestamptz)
		FROM capacity_routes AS route
		CROSS JOIN LATERAL jsonb_array_elements(route.sailings) AS sailing
	`).Scan(
		&health.OperationalRoutes,
		&health.OperationalSailings,
		&health.OldestOperationalScrape,
	); err != nil {
		return health, err
	}

	if err := Conn.QueryRow(`
		WITH local_dates AS (
			SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'America/Vancouver')::date AS today
		)
		SELECT
			COUNT(*) FILTER (WHERE service_date = today),
			COALESCE(SUM(jsonb_array_length(sailings)) FILTER (WHERE service_date = today), 0),
			COUNT(*) FILTER (WHERE service_date = today + 1),
			COALESCE(SUM(jsonb_array_length(sailings)) FILTER (WHERE service_date = today + 1), 0),
			MIN(scraped_at) FILTER (WHERE service_date BETWEEN today AND today + 1)
		FROM official_schedule_routes, local_dates
	`).Scan(
		&health.OfficialTodayRoutes,
		&health.OfficialTodaySailings,
		&health.OfficialTomorrowRoutes,
		&health.OfficialTomorrowSailings,
		&health.OldestOfficialScrape,
	); err != nil {
		return health, err
	}
	return health, nil
}

// mergeCapacityRoutes makes the published timetable the baseline and overlays
// mutable current-conditions observations only when their canonical IDs match.
// It intentionally does not use vessel assignment, actual times, or AIS data
// as part of that join.
func mergeCapacityRoutes(
	operationalRoutes []models.CapacityRoute,
	officialRoutes []models.OfficialScheduleRoute,
) []models.CapacityRoute {
	operationalByID := make(map[string]models.CapacitySailing)
	for _, route := range operationalRoutes {
		for _, sailing := range route.Sailings {
			if sailing.SailingID != "" {
				operationalByID[sailing.SailingID] = sailing
			}
		}
	}

	mergedByRoute := make(map[string]*models.CapacityRoute)
	matchedOperational := make(map[string]struct{})
	for _, officialRoute := range officialRoutes {
		route := mergedByRoute[officialRoute.RouteCode]
		if route == nil {
			merged := models.CapacityRoute{
				RouteCode:        officialRoute.RouteCode,
				FromTerminalCode: officialRoute.FromTerminalCode,
				ToTerminalCode:   officialRoute.ToTerminalCode,
				SailingDuration:  officialRoute.SailingDuration,
				Sailings:         []models.CapacitySailing{},
			}
			mergedByRoute[officialRoute.RouteCode] = &merged
			route = &merged
		}
		if route.SailingDuration == "" {
			route.SailingDuration = officialRoute.SailingDuration
		}

		for _, official := range officialRoute.Sailings {
			merged, hasOperational := operationalByID[official.SailingID]
			if hasOperational {
				matchedOperational[official.SailingID] = struct{}{}
			} else {
				merged = models.CapacitySailing{
					DepartureTime:     official.ScheduledDepartureTime,
					ArrivalTime:       official.ScheduledArrivalTime,
					ScrapedAt:         officialRoute.ScrapedAt,
					SailingStatus:     "future",
					OperationalSource: "unavailable",
					VesselStatus:      "Operational status unavailable",
				}
			}

			// The official fields always win. In particular, an actual 14:09
			// departure can never replace a published 14:00 departure.
			merged.SailingID = official.SailingID
			merged.ServiceDate = official.ServiceDate
			merged.ScheduledDepartureTime = official.ScheduledDepartureTime
			merged.ScheduledArrivalTime = official.ScheduledArrivalTime
			merged.ScheduledDepartureAt = official.ScheduledDepartureAt
			merged.ScheduledArrivalAt = official.ScheduledArrivalAt
			merged.ScheduleSource = "bc-ferries-daily-schedule"
			merged.ScheduleSourceURL = officialRoute.SourceURL
			merged.ScheduleScrapedAt = officialRoute.ScrapedAt
			route.Sailings = append(route.Sailings, merged)
		}
	}

	// If the official page is missing or current conditions contains an added
	// sailing, retain that operational record and make the fallback provenance
	// explicit rather than dropping useful service information.
	for _, operationalRoute := range operationalRoutes {
		route := mergedByRoute[operationalRoute.RouteCode]
		if route == nil {
			copyRoute := operationalRoute
			copyRoute.Sailings = nil
			mergedByRoute[operationalRoute.RouteCode] = &copyRoute
			route = &copyRoute
		}
		for _, sailing := range operationalRoute.Sailings {
			if _, matched := matchedOperational[sailing.SailingID]; matched {
				continue
			}
			sailing.ScheduleSource = "bc-ferries-current-conditions-fallback"
			if sailing.ScheduleSourceURL == "" {
				sailing.ScheduleSourceURL = "https://www.bcferries.com/current-conditions/" +
					operationalRoute.FromTerminalCode + "-" + operationalRoute.ToTerminalCode
			}
			if sailing.ScheduleScrapedAt == "" {
				sailing.ScheduleScrapedAt = sailing.ScrapedAt
			}
			route.Sailings = append(route.Sailings, sailing)
		}
	}

	routes := make([]models.CapacityRoute, 0, len(mergedByRoute))
	for _, route := range mergedByRoute {
		sort.SliceStable(route.Sailings, func(i, j int) bool {
			return route.Sailings[i].ScheduledDepartureAt < route.Sailings[j].ScheduledDepartureAt
		})
		routes = append(routes, *route)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].RouteCode < routes[j].RouteCode })
	return routes
}

/*
 * GetNonCapacitySailings
 *
 * Retrieves all non-capacity route records from the database, including parsed sailing data.
 *
 * Queries the `non_capacity_routes` table and unmarshals the `sailings` JSON column
 * into a slice of `models.NonCapacitySailing` for each route.
 *
 * @return []models.NonCapacityRoute - a slice of non-capacity routes with their sailings
 */
func GetNonCapacitySailings() []models.NonCapacityRoute {
	var routes []models.NonCapacityRoute

	sqlStatement := `SELECT * FROM non_capacity_routes`

	rows, err := Conn.Query(sqlStatement)
	if err != nil {
		log.Printf("GetNonCapacitySailings: query failed: %v", err)
		return routes
	}
	defer rows.Close()

	for rows.Next() {
		var route models.NonCapacityRoute
		var sailings []uint8

		err := rows.Scan(&route.RouteCode, &route.FromTerminalCode, &route.ToTerminalCode, &route.SailingDuration, &sailings)
		if err != nil {
			log.Printf("GetNonCapacitySailings: row scan failed: %v", err)
			continue
		}

		var content []models.NonCapacitySailing
		if err := json.Unmarshal(sailings, &content); err != nil {
			log.Printf("GetNonCapacitySailings: JSON unmarshal failed: %v", err)
			continue
		}

		route.Sailings = content
		routes = append(routes, route)
	}

	if err := rows.Err(); err != nil {
		log.Printf("GetNonCapacitySailings: row iteration error: %v", err)
	}

	return routes
}
