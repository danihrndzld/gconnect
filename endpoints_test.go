package gconnect_test

import (
	"testing"

	"github.com/danihrndzld/garmin-health-exporter/tauri-app/core/gconnect"
)

// jsURLs was built once from endpoints.js allEndpoints with GARMIN_FAKE_SERVER=http://127.0.0.1:1,
// date 2026-10-07, range 2026-10-01..2026-10-07, displayName dn and activityId 42.
// activity_details and activity_split_summaries are left out: the export never asks for them.
var jsURLs = map[string]string{
	"stats":                      "http://127.0.0.1:1/usersummary-service/usersummary/daily/dn?calendarDate=2026-10-07",
	"user_summary":               "http://127.0.0.1:1/usersummary-service/usersummary/daily/dn?calendarDate=2026-10-07",
	"heart_rates":                "http://127.0.0.1:1/wellness-service/wellness/dailyHeartRate/dn?date=2026-10-07",
	"rhr":                        "http://127.0.0.1:1/userstats-service/wellness/daily/dn?fromDate=2026-10-07&untilDate=2026-10-07&metricId=60",
	"hrv":                        "http://127.0.0.1:1/hrv-service/hrv/2026-10-07",
	"stress":                     "http://127.0.0.1:1/wellness-service/wellness/dailyStress/2026-10-07",
	"all_day_stress":             "http://127.0.0.1:1/wellness-service/wellness/dailyStress/2026-10-07",
	"sleep":                      "http://127.0.0.1:1/wellness-service/wellness/dailySleepData/dn?date=2026-10-07&nonSleepBufferMinutes=60",
	"body_battery":               "http://127.0.0.1:1/wellness-service/wellness/bodyBattery/reports/daily?startDate=2026-10-07&endDate=2026-10-07",
	"body_battery_events":        "http://127.0.0.1:1/wellness-service/wellness/bodyBattery/events/2026-10-07",
	"respiration":                "http://127.0.0.1:1/wellness-service/wellness/daily/respiration/2026-10-07",
	"spo2":                       "http://127.0.0.1:1/wellness-service/wellness/daily/spo2/2026-10-07",
	"steps":                      "http://127.0.0.1:1/wellness-service/wellness/dailySummaryChart/dn?date=2026-10-07",
	"intensity_minutes":          "http://127.0.0.1:1/wellness-service/wellness/daily/im/2026-10-07",
	"hydration":                  "http://127.0.0.1:1/usersummary-service/usersummary/hydration/daily/2026-10-07",
	"training_readiness":         "http://127.0.0.1:1/metrics-service/metrics/trainingreadiness/2026-10-07",
	"morning_training_readiness": "http://127.0.0.1:1/metrics-service/metrics/trainingreadiness/2026-10-07",
	"training_status":            "http://127.0.0.1:1/metrics-service/metrics/trainingstatus/aggregated/2026-10-07",
	"weigh_ins":                  "http://127.0.0.1:1/weight-service/weight/dayview/2026-10-07?includeAll=true",
	"max_metrics":                "http://127.0.0.1:1/metrics-service/metrics/maxmet/daily/2026-10-07/2026-10-07",
	"endurance_score":            "http://127.0.0.1:1/metrics-service/metrics/endurancescore?calendarDate=2026-10-07",
	"running_tolerance":          "http://127.0.0.1:1/metrics-service/metrics/runningtolerance/stats?startDate=2026-10-07&endDate=2026-10-07&aggregation=daily",
	"blood_pressure":             "http://127.0.0.1:1/bloodpressure-service/bloodpressure/range/2026-10-01/2026-10-07?includeAll=true",
	"agg_weigh_ins":              "http://127.0.0.1:1/weight-service/weight/range/2026-10-01/2026-10-07?includeAll=true",
	"daily_steps":                "http://127.0.0.1:1/usersummary-service/stats/steps/daily/2026-10-01/2026-10-07",
	"weekly_steps":               "http://127.0.0.1:1/usersummary-service/stats/steps/weekly/2026-10-07/52",
	"weekly_intensity":           "http://127.0.0.1:1/usersummary-service/stats/im/weekly/2026-10-01/2026-10-07",
	"body_composition":           "http://127.0.0.1:1/weight-service/weight/dateRange?startDate=2026-10-01&endDate=2026-10-07",
	"progress_summary":           "http://127.0.0.1:1/fitnessstats-service/activity?startDate=2026-10-01&endDate=2026-10-07&aggregation=lifetime&groupByParentActivityType=true&metric=duration",
	"race_predictions":           "http://127.0.0.1:1/metrics-service/metrics/racepredictions/latest/dn",
	"lactate_threshold":          "http://127.0.0.1:1/biometric-service/biometric/latestLactateThreshold",
	"cycling_ftp":                "http://127.0.0.1:1/biometric-service/biometric/latestFunctionalThresholdPower/CYCLING",
	"fitness_age":                "http://127.0.0.1:1/fitnessage-service/fitnessage/2026-10-07",
	"hill_score":                 "http://127.0.0.1:1/metrics-service/metrics/hillscore/stats?startDate=2026-10-01&endDate=2026-10-07&aggregation=daily",
	"activity_splits":            "http://127.0.0.1:1/activity-service/activity/42/splits",
	"activities_by_date":         "http://127.0.0.1:1/activitylist-service/activities/search/activities?startDate=2026-10-01&endDate=2026-10-07&start=0&limit=20",
	"goals":                      "http://127.0.0.1:1/goal-service/goal/goals?status=active&start=1&limit=30&sortOrder=asc",
	"social_profile":             "http://127.0.0.1:1/userprofile-service/socialProfile",
}

func TestEndpointTable(t *testing.T) {
	p := gconnect.Params{Date: "2026-10-07", Start: "2026-10-01", End: "2026-10-07", DisplayName: "dn", ActivityID: "42"}
	t.Run("CL-331 every endpoint builds the URL endpoints.js builds", func(t *testing.T) {
		seen := map[string]bool{}
		for _, list := range [][]gconnect.Endpoint{gconnect.Daily, gconnect.Aggregated, gconnect.Activity, gconnect.List} {
			for _, ep := range list {
				if seen[ep.Name] {
					t.Errorf("%s is listed twice", ep.Name)
				}
				seen[ep.Name] = true
				if got := ep.URL("http://127.0.0.1:1", p); got != jsURLs[ep.Name] {
					t.Errorf("%s:\n got %s\nwant %s", ep.Name, got, jsURLs[ep.Name])
				}
			}
		}
		if len(seen) != len(jsURLs) {
			t.Errorf("%d endpoints, want %d", len(seen), len(jsURLs))
		}
	})
	t.Run("CL-332 the 3 duplicate URLs carry AliasOf, and goals carries Sec-Fetch-Site", func(t *testing.T) {
		for name, of := range map[string]string{"user_summary": "stats", "all_day_stress": "stress", "morning_training_readiness": "training_readiness"} {
			if ep, _ := gconnect.Lookup(name); ep.AliasOf != of {
				t.Errorf("%s.AliasOf = %q, want %q", name, ep.AliasOf, of)
			}
		}
		if ep, _ := gconnect.Lookup("goals"); ep.Header.Get("Sec-Fetch-Site") != "same-origin" {
			t.Errorf("goals header = %v", ep.Header)
		}
	})
	t.Run("CL-333 Lookup finds each type and refuses an unknown name", func(t *testing.T) {
		for name, typ := range map[string]string{"hrv": "daily", "hill_score": "aggregated", "activity_splits": "activity", "goals": "list"} {
			if ep, ok := gconnect.Lookup(name); !ok || ep.Type != typ {
				t.Errorf("Lookup(%s) = %q %v", name, ep.Type, ok)
			}
		}
		for _, name := range []string{"nonexistent_xyz", "activity_details", "activity_split_summaries"} {
			if _, ok := gconnect.Lookup(name); ok {
				t.Errorf("Lookup(%s) found an endpoint", name)
			}
		}
	})
	t.Run("CL-334 activities_by_date pages with Offset and Limit", func(t *testing.T) {
		ep, _ := gconnect.Lookup("activities_by_date")
		got := ep.URL("http://x", gconnect.Params{Start: "2026-10-01", End: "2026-10-07", Offset: 40, Limit: 20})
		if want := "http://x/activitylist-service/activities/search/activities?startDate=2026-10-01&endDate=2026-10-07&start=40&limit=20"; got != want {
			t.Errorf("got %s", got)
		}
	})
}
