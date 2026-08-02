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
	// Deprecated compatibility fields. Their meaning changes with status:
	// future uses the scheduled departure, current/past use the actual
	// departure, current uses ETA for arrival, and past uses actual arrival.
	DepartureTime string `json:"time"`
	ArrivalTime   string `json:"arrivalTime"`

	// Explicit time semantics from BC Ferries' current-conditions page. Pointer
	// fields encode unavailable values as JSON null instead of an ambiguous
	// empty string.
	ScheduledDepartureTime string  `json:"scheduledDepartureTime"`
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
