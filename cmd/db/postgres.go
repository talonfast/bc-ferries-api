package db

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"

	"github.com/samuel-pratt/bc-ferries-api/cmd/config"
)

var Conn *sql.DB

/*
 * Init
 *
 * Initializes the global PostgreSQL database connection using the DSN from config.DB.URL.
 *
 * Opens a connection pool and assigns it to the Conn variable.
 * Panics if the connection cannot be established.
 *
 * @return void
 */
func Init() {
	var err error
	Conn, err = sql.Open("postgres", config.DB.URL)
	if err != nil {
		panic(err)
	}
}

// Migrate applies additive, idempotent schema changes required by the running
// binary. init.sql still initializes new databases; this protects existing
// self-hosted databases whose persistent volume predates a new table.
func Migrate() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS official_schedule_routes (
			route_code VARCHAR(6) NOT NULL,
			service_date DATE NOT NULL,
			from_terminal_code VARCHAR(3) NOT NULL,
			to_terminal_code VARCHAR(3) NOT NULL,
			sailing_duration VARCHAR(7) NOT NULL,
			sailings JSONB NOT NULL,
			source_url TEXT NOT NULL,
			scraped_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (route_code, service_date)
		)`,
		`CREATE INDEX IF NOT EXISTS official_schedule_routes_service_date_idx
			ON official_schedule_routes (service_date)`,
	}

	for _, statement := range statements {
		if _, err := Conn.Exec(statement); err != nil {
			return fmt.Errorf("database migration failed: %w", err)
		}
	}
	return nil
}
