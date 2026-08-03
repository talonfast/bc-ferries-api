CREATE TABLE capacity_routes (
    route_code VARCHAR(6) PRIMARY KEY,
    from_terminal_code VARCHAR(3) NOT NULL,
    to_terminal_code VARCHAR(3) NOT NULL,
    sailing_duration VARCHAR(7) NOT NULL,
    sailings JSONB NOT NULL
);

CREATE TABLE non_capacity_routes (
    route_code VARCHAR(6) PRIMARY KEY,
    from_terminal_code VARCHAR(3) NOT NULL,
    to_terminal_code VARCHAR(3) NOT NULL,
    sailing_duration VARCHAR(7) NOT NULL,
    sailings JSONB NOT NULL
);

CREATE TABLE official_schedule_routes (
    route_code VARCHAR(6) NOT NULL,
    service_date DATE NOT NULL,
    from_terminal_code VARCHAR(3) NOT NULL,
    to_terminal_code VARCHAR(3) NOT NULL,
    sailing_duration VARCHAR(7) NOT NULL,
    sailings JSONB NOT NULL,
    source_url TEXT NOT NULL,
    scraped_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (route_code, service_date)
);

CREATE INDEX official_schedule_routes_service_date_idx
    ON official_schedule_routes (service_date);
