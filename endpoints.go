package gconnect

import (
	"net/http"
	"strconv"
)

// Params fills an endpoint URL. Daily endpoints read Date; aggregated ones read Start and End;
// activity_splits reads ActivityID; activities_by_date pages with Offset and Limit (0 means 20).
type Params struct {
	Date, Start, End, DisplayName, ActivityID string
	Offset, Limit                             int
}

// Endpoint is one connectapi request. URL joins base (the connectapi host) and the path; it
// never reads the hosts itself.
type Endpoint struct {
	Name, Type, AliasOf string      // Type: daily, aggregated, activity, list, personal; AliasOf: same URL as that endpoint
	Header              http.Header // extra request headers, or nil
	URL                 func(base string, p Params) string
}

func daily(name, aliasOf string, path func(p Params) string) Endpoint {
	return Endpoint{Name: name, Type: "daily", AliasOf: aliasOf, URL: func(base string, p Params) string { return base + path(p) }}
}

func aggregated(name string, path func(p Params) string) Endpoint {
	return Endpoint{Name: name, Type: "aggregated", URL: func(base string, p Params) string { return base + path(p) }}
}

// dailyList is one request per day.
var dailyList = []Endpoint{
	daily("stats", "", func(p Params) string {
		return "/usersummary-service/usersummary/daily/" + p.DisplayName + "?calendarDate=" + p.Date
	}),
	daily("user_summary", "stats", func(p Params) string {
		return "/usersummary-service/usersummary/daily/" + p.DisplayName + "?calendarDate=" + p.Date
	}),
	daily("heart_rates", "", func(p Params) string {
		return "/wellness-service/wellness/dailyHeartRate/" + p.DisplayName + "?date=" + p.Date
	}),
	daily("rhr", "", func(p Params) string {
		return "/userstats-service/wellness/daily/" + p.DisplayName + "?fromDate=" + p.Date + "&untilDate=" + p.Date + "&metricId=60"
	}),
	daily("hrv", "", func(p Params) string { return "/hrv-service/hrv/" + p.Date }),
	daily("stress", "", func(p Params) string { return "/wellness-service/wellness/dailyStress/" + p.Date }),
	daily("all_day_stress", "stress", func(p Params) string { return "/wellness-service/wellness/dailyStress/" + p.Date }),
	daily("sleep", "", func(p Params) string {
		return "/wellness-service/wellness/dailySleepData/" + p.DisplayName + "?date=" + p.Date + "&nonSleepBufferMinutes=60"
	}),
	daily("body_battery", "", func(p Params) string {
		return "/wellness-service/wellness/bodyBattery/reports/daily?startDate=" + p.Date + "&endDate=" + p.Date
	}),
	daily("body_battery_events", "", func(p Params) string { return "/wellness-service/wellness/bodyBattery/events/" + p.Date }),
	daily("respiration", "", func(p Params) string { return "/wellness-service/wellness/daily/respiration/" + p.Date }),
	daily("spo2", "", func(p Params) string { return "/wellness-service/wellness/daily/spo2/" + p.Date }),
	daily("steps", "", func(p Params) string {
		return "/wellness-service/wellness/dailySummaryChart/" + p.DisplayName + "?date=" + p.Date
	}),
	daily("intensity_minutes", "", func(p Params) string { return "/wellness-service/wellness/daily/im/" + p.Date }),
	daily("hydration", "", func(p Params) string { return "/usersummary-service/usersummary/hydration/daily/" + p.Date }),
	daily("training_readiness", "", func(p Params) string { return "/metrics-service/metrics/trainingreadiness/" + p.Date }),
	// Filtered for AFTER_WAKEUP_RESET by the caller.
	daily("morning_training_readiness", "training_readiness", func(p Params) string { return "/metrics-service/metrics/trainingreadiness/" + p.Date }),
	daily("training_status", "", func(p Params) string { return "/metrics-service/metrics/trainingstatus/aggregated/" + p.Date }),
	daily("weigh_ins", "", func(p Params) string { return "/weight-service/weight/dayview/" + p.Date + "?includeAll=true" }),
	daily("max_metrics", "", func(p Params) string { return "/metrics-service/metrics/maxmet/daily/" + p.Date + "/" + p.Date }),
	daily("endurance_score", "", func(p Params) string { return "/metrics-service/metrics/endurancescore?calendarDate=" + p.Date }),
	daily("running_tolerance", "", func(p Params) string {
		return "/metrics-service/metrics/runningtolerance/stats?startDate=" + p.Date + "&endDate=" + p.Date + "&aggregation=daily"
	}),
}

// aggregatedList is one request per date range.
var aggregatedList = []Endpoint{
	aggregated("blood_pressure", func(p Params) string {
		return "/bloodpressure-service/bloodpressure/range/" + p.Start + "/" + p.End + "?includeAll=true"
	}),
	aggregated("agg_weigh_ins", func(p Params) string {
		return "/weight-service/weight/range/" + p.Start + "/" + p.End + "?includeAll=true"
	}),
	// Garmin allows at most 28 days per request; the caller chunks.
	aggregated("daily_steps", func(p Params) string { return "/usersummary-service/stats/steps/daily/" + p.Start + "/" + p.End }),
	aggregated("weekly_steps", func(p Params) string { return "/usersummary-service/stats/steps/weekly/" + p.End + "/52" }),
	aggregated("weekly_intensity", func(p Params) string { return "/usersummary-service/stats/im/weekly/" + p.Start + "/" + p.End }),
	aggregated("body_composition", func(p Params) string {
		return "/weight-service/weight/dateRange?startDate=" + p.Start + "&endDate=" + p.End
	}),
	aggregated("progress_summary", func(p Params) string {
		return "/fitnessstats-service/activity?startDate=" + p.Start + "&endDate=" + p.End + "&aggregation=lifetime&groupByParentActivityType=true&metric=duration"
	}),
	aggregated("race_predictions", func(p Params) string { return "/metrics-service/metrics/racepredictions/latest/" + p.DisplayName }),
	aggregated("lactate_threshold", func(Params) string { return "/biometric-service/biometric/latestLactateThreshold" }),
	aggregated("cycling_ftp", func(Params) string { return "/biometric-service/biometric/latestFunctionalThresholdPower/CYCLING" }),
	aggregated("fitness_age", func(p Params) string { return "/fitnessage-service/fitnessage/" + p.End }),
	aggregated("hill_score", func(p Params) string {
		return "/metrics-service/metrics/hillscore/stats?startDate=" + p.Start + "&endDate=" + p.End + "&aggregation=daily"
	}),
}

// activityList is one request per activity.
var activityList = []Endpoint{
	{Name: "activity_splits", Type: "activity", URL: func(base string, p Params) string {
		return base + "/activity-service/activity/" + p.ActivityID + "/splits"
	}},
}

// listList pages through lists, plus the profile.
var listList = []Endpoint{
	{Name: "activities_by_date", Type: "list", URL: func(base string, p Params) string {
		limit := p.Limit
		if limit == 0 {
			limit = 20
		}
		return base + "/activitylist-service/activities/search/activities?startDate=" + p.Start + "&endDate=" + p.End +
			"&start=" + strconv.Itoa(p.Offset) + "&limit=" + strconv.Itoa(limit)
	}},
	// start is 1 based and the header is required, or Garmin answers [] (python-garminconnect #431).
	{Name: "goals", Type: "list", Header: http.Header{"Sec-Fetch-Site": {"same-origin"}}, URL: func(base string, _ Params) string {
		return base + "/goal-service/goal/goals?status=active&start=1&limit=30&sortOrder=asc"
	}},
	{Name: "social_profile", Type: "list", URL: func(base string, _ Params) string { return base + "/userprofile-service/socialProfile" }},
}

func personal(name string, path func(p Params) string) Endpoint {
	return Endpoint{Name: name, Type: "personal", URL: func(base string, p Params) string { return base + path(p) }}
}

// personalList holds the Personal API paths that connectapi spells differently. Each takes
// Start and End and answers one entry per day, except personal_fitness_age, which reads End. The Personal API host also answers
// daily_steps, agg_weigh_ins, body_battery, hrv and activities_by_date at their connectapi
// paths, and 404 to the rest of the table (measured 2026-10-24).
var personalList = []Endpoint{
	// {"data": [...]}, at most 90 days per request: steps, calories, heart rate, stress,
	// intensity minutes, Body Battery, respiration and pulse ox in one row per day.
	personal("personal_daily_summaries", func(p Params) string {
		return "/usersummary-service/v2/dailysummaries?startDate=" + p.Start + "&endDate=" + p.End
	}),
	// [...] of dailySleepDTO shaped rows, at most 31 days per request.
	personal("personal_sleep", func(p Params) string {
		return "/sleep-service/sleep/dailySleepSummariesByDate?startDate=" + p.Start + "&endDate=" + p.End
	}),
	// {"hrvSummaries": [...]} of hrvSummary shaped rows, at most 90 days per request.
	personal("personal_hrv", func(p Params) string { return "/hrv-service/hrv/daily/" + p.Start + "/" + p.End }),
	// {"fitnessAge", "chronologicalAge", "achievableFitnessAge", "components", ...} on End.
	personal("personal_fitness_age", func(p Params) string { return "/fitnessage-service/fitnessage/v2/" + p.End }),
	// [...] of the rows body_battery answers for one day, at most 31 days per request.
	personal("personal_body_battery", func(p Params) string {
		return "/wellness-service/wellness/bodyBattery/reports/daily?startDate=" + p.Start + "&endDate=" + p.End
	}),
}

var endpoints = func() map[string]Endpoint {
	m := map[string]Endpoint{}
	for _, list := range [][]Endpoint{dailyList, aggregatedList, activityList, listList, personalList} {
		for _, ep := range list {
			m[ep.Name] = ep
		}
	}
	return m
}()

// Lookup finds an endpoint by name. The result is a copy the caller may change.
func Lookup(name string) (Endpoint, bool) {
	ep, ok := endpoints[name]
	return clone(ep), ok
}

func clone(ep Endpoint) Endpoint {
	ep.Header = ep.Header.Clone()
	return ep
}

func cloneAll(list []Endpoint) []Endpoint {
	out := make([]Endpoint, len(list))
	for i, ep := range list {
		out[i] = clone(ep)
	}
	return out
}

// Daily returns the endpoints that take Params.Date (and DisplayName for six of them), in a
// fixed order. Each call returns a new copy.
func Daily() []Endpoint { return cloneAll(dailyList) }

// Aggregated returns the endpoints that take Params.Start and End (some also DisplayName), in
// a fixed order. Each call returns a new copy.
func Aggregated() []Endpoint { return cloneAll(aggregatedList) }

// Activity returns the endpoints that take Params.ActivityID. Each call returns a new copy.
func Activity() []Endpoint { return cloneAll(activityList) }

// List returns the activity list, goals and the social profile. Each call returns a new copy.
func List() []Endpoint { return cloneAll(listList) }

// PersonalRanges returns the Personal API range endpoints that have no connectapi twin, in a fixed
// order. Request them with a Client whose Tokens hold a Personal API key. Each call returns a
// new copy.
func PersonalRanges() []Endpoint { return cloneAll(personalList) }
