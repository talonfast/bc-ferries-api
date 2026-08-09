package db

import (
	"testing"

	"github.com/samuel-pratt/bc-ferries-api/cmd/models"
)

func TestMergeCapacityRoutes_PreservesOfficialScheduleAndOverlaysActuals(t *testing.T) {
	id := "bcf:v1:2026-08-03:TSA-SWB:1400:01"
	actualDeparture := "2:09 pm"
	estimatedArrival := "3:37 pm"
	operational := []models.CapacityRoute{{
		RouteCode: "TSASWB", FromTerminalCode: "TSA", ToTerminalCode: "SWB",
		Sailings: []models.CapacitySailing{{
			SailingID: id, ServiceDate: "2026-08-03",
			ScheduledDepartureTime: "2:00 pm", ScheduledDepartureAt: "2026-08-03T14:00:00-07:00",
			DepartureTime: "2:09 pm", ArrivalTime: "3:37 pm", ActualDepartureTime: &actualDeparture,
			EstimatedArrivalTime: &estimatedArrival, SailingStatus: "current", VesselName: "Coastal Celebration",
			OperationalSource: "bc-ferries-current-conditions", ScrapedAt: "2026-08-03T21:10:00Z",
		}},
	}}
	official := []models.OfficialScheduleRoute{{
		RouteCode: "TSASWB", FromTerminalCode: "TSA", ToTerminalCode: "SWB",
		ServiceDate: "2026-08-03", SailingDuration: "01:35",
		SourceURL: "https://www.bcferries.com/routes-fares/schedules/daily/TSA-SWB",
		ScrapedAt: "2026-08-03T20:00:00Z",
		Sailings: []models.OfficialScheduleSailing{{
			SailingID: id, ServiceDate: "2026-08-03",
			ScheduledDepartureTime: "2:00 pm", ScheduledArrivalTime: "3:35 pm",
			ScheduledDepartureAt: "2026-08-03T14:00:00-07:00",
			ScheduledArrivalAt:   "2026-08-03T15:35:00-07:00",
		}},
	}}

	routes := mergeCapacityRoutes(operational, official)
	if len(routes) != 1 || len(routes[0].Sailings) != 1 {
		t.Fatalf("unexpected merged shape: %#v", routes)
	}
	sailing := routes[0].Sailings[0]
	if sailing.ScheduledDepartureTime != "2:00 pm" || sailing.DepartureTime != "2:09 pm" {
		t.Fatalf("scheduled and actual departure were conflated: %#v", sailing)
	}
	if sailing.ScheduledArrivalTime != "3:35 pm" || sailing.EstimatedArrivalTime == nil || *sailing.EstimatedArrivalTime != "3:37 pm" {
		t.Fatalf("scheduled arrival and ETA were conflated: %#v", sailing)
	}
	if sailing.ScheduleSource != "bc-ferries-daily-schedule" {
		t.Fatalf("unexpected schedule source %q", sailing.ScheduleSource)
	}
	if sailing.VesselName != "Coastal Celebration" {
		t.Fatalf("operational vessel overlay was lost: %#v", sailing)
	}
}

func TestMergeCapacityRoutes_RetainsScheduleOnlyAndOperationalOnlySailings(t *testing.T) {
	scheduleOnlyID := "bcf:v1:2026-08-03:TSA-SWB:1400:01"
	extraID := "bcf:v1:2026-08-03:TSA-SWB:1430:01"
	operational := []models.CapacityRoute{{
		RouteCode: "TSASWB", FromTerminalCode: "TSA", ToTerminalCode: "SWB",
		Sailings: []models.CapacitySailing{{
			SailingID: extraID, ServiceDate: "2026-08-03",
			ScheduledDepartureTime: "2:30 pm", ScheduledDepartureAt: "2026-08-03T14:30:00-07:00",
			DepartureTime: "2:30 pm", SailingStatus: "future", ScrapedAt: "2026-08-03T20:00:00Z",
		}},
	}}
	official := []models.OfficialScheduleRoute{{
		RouteCode: "TSASWB", FromTerminalCode: "TSA", ToTerminalCode: "SWB",
		ServiceDate: "2026-08-03", SailingDuration: "01:35",
		SourceURL: "https://www.bcferries.com/routes-fares/schedules/daily/TSA-SWB",
		ScrapedAt: "2026-08-03T19:00:00Z",
		Sailings: []models.OfficialScheduleSailing{{
			SailingID: scheduleOnlyID, ServiceDate: "2026-08-03",
			ScheduledDepartureTime: "2:00 pm", ScheduledArrivalTime: "3:35 pm",
			ScheduledDepartureAt: "2026-08-03T14:00:00-07:00", ScheduledArrivalAt: "2026-08-03T15:35:00-07:00",
		}},
	}}

	routes := mergeCapacityRoutes(operational, official)
	if len(routes) != 1 || len(routes[0].Sailings) != 2 {
		t.Fatalf("expected both records, got %#v", routes)
	}
	if routes[0].Sailings[0].OperationalSource != "unavailable" {
		t.Fatalf("schedule-only record was not marked unavailable: %#v", routes[0].Sailings[0])
	}
	if routes[0].Sailings[1].ScheduleSource != "bc-ferries-current-conditions-fallback" {
		t.Fatalf("extra sailing fallback was not explicit: %#v", routes[0].Sailings[1])
	}
}

func TestMergeCapacityRoutes_RejectsOfficialSGITimesForDifferentObservedItinerary(t *testing.T) {
	id := "bcf:v1:2026-08-03:SWB-SGI:0555:01"
	operational := []models.CapacityRoute{{
		RouteCode: "SWBSGI", FromTerminalCode: "SWB", ToTerminalCode: "SGI",
		Sailings: []models.CapacitySailing{{
			SailingID: id, ServiceDate: "2026-08-03",
			ScheduledDepartureAt: "2026-08-03T05:55:00-07:00",
			ItineraryRaw:         "via Saturna Island (Lyall Harbour) to Pender Island (Otter Bay)",
			PortCalls: []models.PortCall{
				{Sequence: 0, TerminalCode: "SWB", Role: "origin"},
				{Sequence: 1, TerminalCode: "PST", Role: "stop"},
				{Sequence: 2, TerminalCode: "POB", Role: "destination"},
			},
		}},
	}}
	official := []models.OfficialScheduleRoute{{
		RouteCode: "SWBSGI", FromTerminalCode: "SWB", ToTerminalCode: "SGI",
		ServiceDate: "2026-08-03", SourceURL: "https://www.bcferries.com/routes-fares/schedules",
		Sailings: []models.OfficialScheduleSailing{{
			SailingID: id, ServiceDate: "2026-08-03",
			ScheduledDepartureAt: "2026-08-03T05:55:00-07:00",
			ScheduledArrivalAt:   "2026-08-03T07:55:00-07:00",
			PortCalls: []models.PortCall{
				{Sequence: 0, TerminalCode: "SWB", Role: "origin"},
				{Sequence: 1, TerminalCode: "PST", Role: "stop"},
				{Sequence: 2, TerminalCode: "PVB", Role: "destination"},
			},
		}},
	}}

	routes := mergeCapacityRoutes(operational, official)
	if len(routes) != 1 || len(routes[0].Sailings) != 1 {
		t.Fatalf("expected only the honest operational fallback, got %#v", routes)
	}
	sailing := routes[0].Sailings[0]
	if sailing.ScheduleSource != "bc-ferries-current-conditions-fallback" ||
		sailing.PortCalls[2].TerminalCode != "POB" {
		t.Fatalf("official data overwrote a conflicting itinerary: %#v", sailing)
	}
}

func TestMergeCapacityRoutes_UsesOfficialSGIPortCallTimesWhenSequenceMatches(t *testing.T) {
	id := "bcf:v1:2026-08-03:SWB-SGI:0555:01"
	sequence := []models.PortCall{
		{Sequence: 0, TerminalCode: "SWB", Role: "origin"},
		{Sequence: 1, TerminalCode: "PST", Role: "stop"},
		{Sequence: 2, TerminalCode: "PVB", Role: "destination"},
	}
	operational := []models.CapacityRoute{{
		RouteCode: "SWBSGI", FromTerminalCode: "SWB", ToTerminalCode: "SGI",
		Sailings: []models.CapacitySailing{{
			SailingID: id, ServiceDate: "2026-08-03", PortCalls: sequence,
		}},
	}}
	officialCalls := append([]models.PortCall(nil), sequence...)
	officialCalls[1].ScheduledArrivalAt = "2026-08-03T07:05:00-07:00"
	officialCalls[1].ScheduleSource = "bc-ferries-seasonal-route-schedules"
	official := []models.OfficialScheduleRoute{{
		RouteCode: "SWBSGI", FromTerminalCode: "SWB", ToTerminalCode: "SGI",
		ServiceDate: "2026-08-03", SourceURL: "https://www.bcferries.com/routes-fares/schedules",
		Sailings: []models.OfficialScheduleSailing{{
			SailingID: id, ServiceDate: "2026-08-03", PortCalls: officialCalls,
		}},
	}}

	sailing := mergeCapacityRoutes(operational, official)[0].Sailings[0]
	if sailing.ScheduleSource != "bc-ferries-seasonal-route-schedules" ||
		sailing.PortCalls[1].ScheduledArrivalAt != "2026-08-03T07:05:00-07:00" {
		t.Fatalf("matching official calls were not applied: %#v", sailing)
	}
}
