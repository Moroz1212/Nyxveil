package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAPITimeRFC3339Z(t *testing.T) {
	var at APITime
	if err := json.Unmarshal([]byte(`"2026-09-05T16:03:33.2935527Z"`), &at); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 5, 16, 3, 33, 293552700, time.UTC)
	if !at.Time().Equal(want) {
		t.Fatalf("got %v want %v", at.Time(), want)
	}
}

func TestAPITimeRFC3339Offset(t *testing.T) {
	var at APITime
	if err := json.Unmarshal([]byte(`"2026-09-05T16:03:33.2935527+00:00"`), &at); err != nil {
		t.Fatal(err)
	}
	if at.Time().Location() != time.UTC && at.Time().UTC().Hour() != 16 {
		t.Fatalf("%v", at.Time())
	}
	if got := at.Time().UTC(); got.Hour() != 16 || got.Nanosecond() != 293552700 {
		t.Fatalf("%v", got)
	}
}

func TestAPITimeDotNet7FractionNoZoneAsUTC(t *testing.T) {
	// Exact production Control Plane timestamp that broke Go time.Time.
	const raw = `"2026-09-05T16:03:33.2935527"`
	var at APITime
	if err := json.Unmarshal([]byte(raw), &at); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 5, 16, 3, 33, 293552700, time.UTC)
	if !at.Time().Equal(want) {
		t.Fatalf("got %v want %v (must be UTC, not local)", at.Time(), want)
	}
	if at.Time().Location() != time.UTC {
		t.Fatalf("location=%v", at.Time().Location())
	}
}

func TestAPITimeRejectsInvalid(t *testing.T) {
	bad := []string{
		`"05/09/2026"`,
		`"Sep 5, 2026"`,
		`"2026-09-05 16:03:33"`,
		`"4:03 PM"`,
		`"not-a-time"`,
	}
	for _, b := range bad {
		var at APITime
		if err := json.Unmarshal([]byte(b), &at); err == nil {
			t.Fatalf("accepted %s", b)
		}
	}
}

func TestAPITimeFractionalVariants(t *testing.T) {
	cases := []string{
		`"2026-09-05T16:03:33Z"`,
		`"2026-09-05T16:03:33.293Z"`,
		`"2026-09-05T16:03:33.2935527Z"`,
		`"2026-09-05T16:03:33.2935527"`,
		`"2026-09-05T16:03:33"`,
	}
	for _, c := range cases {
		var at APITime
		if err := json.Unmarshal([]byte(c), &at); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if at.IsZero() {
			t.Fatalf("zero for %s", c)
		}
	}
}

func TestRegistrationDecodesLegacyControlPlaneTimestamp(t *testing.T) {
	body := `{
  "node_id":"n1",
  "registered":true,
  "node_token":"",
  "config_version":1,
  "config":{
    "node_id":"n1",
    "location_id":"hel-1",
    "enabled":true,
    "draining":false,
    "maintenance_mode":false,
    "transport_policy_json":"{}",
    "capacity":100,
    "config_version":1,
    "updated_at":"2026-09-05T16:03:33.2935527"
  }
}`
	var resp RegisterResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Config == nil || resp.Config.LocationID != "hel-1" {
		t.Fatalf("%+v", resp)
	}
	want := time.Date(2026, 9, 5, 16, 3, 33, 293552700, time.UTC)
	if !resp.Config.UpdatedAt.Time().Equal(want) {
		t.Fatalf("updated_at=%v", resp.Config.UpdatedAt.Time())
	}
}

func TestRegistrationDecodesCanonicalUTC(t *testing.T) {
	body := strings.ReplaceAll(`{
  "node_id":"n1",
  "registered":true,
  "config_version":1,
  "config":{
    "node_id":"n1",
    "location_id":"hel-1",
    "enabled":true,
    "capacity":50,
    "config_version":1,
    "updated_at":"2026-09-05T16:03:33.2935527Z"
  }
}`, "\n", "")
	var resp RegisterResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Config.UpdatedAt.Time().Nanosecond() != 293552700 {
		t.Fatal(resp.Config.UpdatedAt.Time())
	}
}
