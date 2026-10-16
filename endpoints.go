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

// Endpoint ports one entry of electron-app/garmin/endpoints.js. URL joins base (the connectapi
// host) and the path; it never reads the hosts itself.
type Endpoint struct {
	Name, Type, AliasOf string // Type: daily, aggregated, activity, list; AliasOf: same URL as that endpoint
	Header              http.Header
	URL                 func(base string, p Params) string
}

func daily(name, aliasOf string, path func(p Params) string) Endpoint {
	return Endpoint{Name: name, Type: "daily", AliasOf: aliasOf, URL: func(base string, p Params) string { return base + path(p) }}
}

func aggregated(name string, path func(p Params) string) Endpoint {
	return Endpoint{Name: name, Type: "aggregated", URL: func(base string, p Params) string { return base + path(p) }}
}

// Daily ports endpoints.js:19-156.
var Daily = []Endpoint{
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

// Aggregated ports endpoints.js:163-238.
var Aggregated = []Endpoint{
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

// Activity ports endpoints.js:245-267 without activity_details and activity_split_summaries,
// which the export never requests.
var Activity = []Endpoint{
	{Name: "activity_splits", Type: "activity", URL: func(base string, p Params) string {
		return base + "/activity-service/activity/" + p.ActivityID + "/splits"
	}},
}

// List ports endpoints.js:274-299.
var List = []Endpoint{
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

var endpoints = func() map[string]Endpoint {
	m := map[string]Endpoint{}
	for _, list := range [][]Endpoint{Daily, Aggregated, Activity, List} {
		for _, ep := range list {
			m[ep.Name] = ep
		}
	}
	return m
}()

// Lookup finds an endpoint by its endpoints.js name.
func Lookup(name string) (Endpoint, bool) {
	ep, ok := endpoints[name]
	return ep, ok
}
