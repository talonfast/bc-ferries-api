package cron

import (
	"time"

	"github.com/go-co-op/gocron"
	"github.com/samuel-pratt/bc-ferries-api/cmd/scraper"
)

/*
 * SetupCron
 *
 * Initializes and starts scheduled background scraping tasks using gocron.
 *
 * - Scrapes capacity route data every 1 minute.
 * - Scrapes official capacity schedules, then non-capacity route data, every
 *   4 hours. They share one singleton job so only one Chromium workload runs
 *   at a time.
 * - Skips an overlapping run of the same scraper if the previous run has not
 *   completed yet.
 *
 * The scheduler runs asynchronously in the background.
 *
 * @return void
 */
func SetupCron() {
	s := gocron.NewScheduler(time.UTC)

	s.Every(1).Minute().StartImmediately().SingletonMode().Do(func() {
		scraper.ScrapeCapacityRoutes()
	})

	s.Every(4).Hour().StartImmediately().SingletonMode().Do(func() {
		scraper.ScrapeOfficialCapacitySchedules()
		scraper.ScrapeNonCapacityRoutes()
	})

	s.StartAsync()
}
