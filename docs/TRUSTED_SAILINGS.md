# Trusted sailing data model

The API treats timetable facts, operational reports, and vessel telemetry as
three different kinds of evidence. They may describe the same sailing, but one
source must not silently rewrite another.

## Canonical identity

When BC Ferries does not expose an operator trip identifier, the API uses this
versioned fallback:

```text
bcf:v1:<Vancouver service date>:<FROM>-<TO>:<scheduled HHmm>:<occurrence>
```

For example, the published 14:00 Tsawwassen to Swartz Bay sailing on 2026-08-03
is `bcf:v1:2026-08-03:TSA-SWB:1400:01`. Its ID remains unchanged if it departs
at 14:09, changes vessel, is delayed, or is cancelled.

`occurrence` is one-based and disambiguates the unusual case where a route
publishes more than one departure at the same minute. If BC Ferries later
exposes a durable trip identifier, that identifier should be stored separately
and introduced through a new identity version after a migration period.

## Source precedence

1. **Published baseline:** the BC Ferries daily schedule supplies service date,
   scheduled departure, scheduled arrival, and duration.
2. **Operational overlay:** BC Ferries current conditions supplies cancellation,
   actual departure, ETA, actual arrival, vessel, and capacity.
3. **AIS observation:** position, speed, heading, and observed timestamp may
   refine live progress and confirm approach or arrival. AIS never changes a
   sailing ID or a published scheduled time.

The merge joins only on canonical sailing ID. An operational record not found
in the published baseline is retained with
`scheduleSource=bc-ferries-current-conditions-fallback`. A published sailing
with no matching operational record is retained with
`operationalSource=unavailable`.

## Failure behaviour

- Official schedules are stored separately by route and Vancouver service date.
- A redirect, Queue-it page, wrong service date, empty table, or partially
  parsed table is rejected as a whole.
- A failed scrape leaves the last known-good schedule untouched.
- The API exposes source URLs and separate scrape timestamps so downstream
  health checks can detect stale data instead of treating it as fresh.
- Today and tomorrow are refreshed every four hours. Current conditions remain
  a one-minute operational feed.

The official source surfaces are the [daily schedule](https://www.bcferries.com/routes-fares/schedules/daily/TSA-SWB)
and [current conditions](https://www.bcferries.com/current-conditions/TSA-SWB)
pages. Scraping is an adapter around those operator-published pages, not a claim
that their HTML is a stable public API.
