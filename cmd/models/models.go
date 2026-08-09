package models

// For shared structs

/**************/
/* V2 Structs */
/**************/

type CapacityRoute struct {
	RouteCode        string            `json:"routeCode"`
	FromTerminalCode string            `json:"fromTerminalCode"`
	ToTerminalCode   string            `json:"toTerminalCode"`
	SailingDuration  string            `json:"sailingDuration"`
	Sailings         []CapacitySailing `json:"sailings"`
}

type CapacitySailing struct {
	// SailingID is a stable, versioned identity derived from the operator,
	// Vancouver service date, direction, scheduled departure, and occurrence.
	// Mutable observations such as vessel assignment and actual departure are
	// deliberately excluded.
	SailingID            string `json:"sailingId"`
	ScheduledDepartureAt string `json:"scheduledDepartureAt"`
	ScheduledArrivalAt   string `json:"scheduledArrivalAt,omitempty"`
	ScheduleSource       string `json:"scheduleSource"`
	ScheduleSourceURL    string `json:"scheduleSourceUrl"`
	ScheduleScrapedAt    string `json:"scheduleScrapedAt"`
	OperationalSource    string `json:"operationalSource"`
	// ItineraryRaw preserves the operator's exact normalized "via ... to ..."
	// description. PortCalls is the parsed, ordered representation; clients must
	// fall back to ItineraryRaw rather than guessing if a new terminal cannot be
	// recognized.
	ItineraryRaw        string     `json:"itineraryRaw,omitempty"`
	ItinerarySource     string     `json:"itinerarySource,omitempty"`
	ItineraryObservedAt string     `json:"itineraryObservedAt,omitempty"`
	PortCalls           []PortCall `json:"portCalls,omitempty"`

	// Deprecated compatibility fields. Their meaning changes with status:
	// future uses the scheduled departure, current/past use the actual
	// departure, current uses ETA for arrival, and past uses actual arrival.
	DepartureTime string `json:"time"`
	ArrivalTime   string `json:"arrivalTime"`

	// Explicit time semantics from BC Ferries' current-conditions page. Pointer
	// fields encode unavailable values as JSON null instead of an ambiguous
	// empty string.
	ScheduledDepartureTime string  `json:"scheduledDepartureTime"`
	ScheduledArrivalTime   string  `json:"scheduledArrivalTime,omitempty"`
	ActualDepartureTime    *string `json:"actualDepartureTime"`
	EstimatedArrivalTime   *string `json:"estimatedArrivalTime"`
	ActualArrivalTime      *string `json:"actualArrivalTime"`
	ServiceDate            string  `json:"serviceDate"`
	ScrapedAt              string  `json:"scrapedAt"`

	SailingStatus string `json:"sailingStatus"`
	Fill          int    `json:"fill"`
	CarFill       int    `json:"carFill"`
	OversizeFill  int    `json:"oversizeFill"`
	VesselName    string `json:"vesselName"`
	VesselStatus  string `json:"vesselStatus"`
}

// PortCall is one published call in a physical sailing. Sequence zero is the
// origin; subsequent calls retain the order BC Ferries publishes. Arrival and
// departure instants are optional until the individual-terminal timetable has
// enriched that call.
type PortCall struct {
	PortCallID           string `json:"portCallId"`
	Sequence             int    `json:"sequence"`
	TerminalCode         string `json:"terminalCode"`
	TerminalName         string `json:"terminalName"`
	IslandName           string `json:"islandName,omitempty"`
	Role                 string `json:"role"`
	ScheduledArrivalAt   string `json:"scheduledArrivalAt,omitempty"`
	ScheduledDepartureAt string `json:"scheduledDepartureAt,omitempty"`
	ScheduleSource       string `json:"scheduleSource,omitempty"`
	ScheduleSourceURL    string `json:"scheduleSourceUrl,omitempty"`
	ScheduleScrapedAt    string `json:"scheduleScrapedAt,omitempty"`
}

// OfficialScheduleRoute is the independently persisted timetable baseline
// published on BC Ferries' daily schedule page. It is never overwritten with
// current-conditions or AIS observations.
type OfficialScheduleRoute struct {
	RouteCode        string                    `json:"routeCode"`
	FromTerminalCode string                    `json:"fromTerminalCode"`
	ToTerminalCode   string                    `json:"toTerminalCode"`
	ServiceDate      string                    `json:"serviceDate"`
	SailingDuration  string                    `json:"sailingDuration"`
	Sailings         []OfficialScheduleSailing `json:"sailings"`
	SourceURL        string                    `json:"sourceUrl"`
	ScrapedAt        string                    `json:"scrapedAt"`
}

type OfficialScheduleSailing struct {
	SailingID              string     `json:"sailingId"`
	ServiceDate            string     `json:"serviceDate"`
	ScheduledDepartureTime string     `json:"scheduledDepartureTime"`
	ScheduledArrivalTime   string     `json:"scheduledArrivalTime"`
	ScheduledDepartureAt   string     `json:"scheduledDepartureAt"`
	ScheduledArrivalAt     string     `json:"scheduledArrivalAt"`
	PortCalls              []PortCall `json:"portCalls,omitempty"`
}

type NonCapacityResponse struct {
	Routes []NonCapacityRoute `json:"routes"`
}

type NonCapacityRoute struct {
	RouteCode        string               `json:"routeCode"`
	FromTerminalCode string               `json:"fromTerminalCode"`
	ToTerminalCode   string               `json:"toTerminalCode"`
	SailingDuration  string               `json:"sailingDuration"`
	Sailings         []NonCapacitySailing `json:"sailings"`
}

type NonCapacitySailing struct {
	DepartureTime string `json:"time"`
	ArrivalTime   string `json:"arrivalTime"`
	VesselName    string `json:"vesselName"`
	VesselStatus  string `json:"vesselStatus"`
}

/**************/
/* V1 Structs */
/**************/

type Route struct {
	SailingDuration string    `json:"sailingDuration"`
	Sailings        []Sailing `json:"sailings"`
}

type Sailing struct {
	DepartureTime string `json:"time"`
	ArrivalTime   string `json:"arrivalTime"`
	IsCancelled   bool   `json:"isCancelled"`
	Fill          int    `json:"fill"`
	CarFill       int    `json:"carFill"`
	OversizeFill  int    `json:"oversizeFill"`
	VesselName    string `json:"vesselName"`
	VesselStatus  string `json:"vesselStatus"`
}
