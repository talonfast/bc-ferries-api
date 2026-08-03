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
