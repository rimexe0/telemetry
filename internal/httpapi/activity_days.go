package httpapi

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/store"
)

// Activity's weeks, months and years on the phone: one compact row per local
// day, so averages over a period are worked out there from rows it keeps.

const (
	// activityDaysLimit is the most days one request covers; the phone asks
	// for a year a month at a time.
	activityDaysLimit = 62
	// activityDayCells is how many parts a day's bar has: ten minutes each.
	activityDayCells = 144
	// A day is final, and kept in the cache, once it ended this long ago:
	// devices that were offline may still deliver its records until then.
	activityDayFinalAfter = 24 * time.Hour
	activityDaysCacheSize = 1000
)

type activityDayDevice struct {
	Device             string  `json:"device"`
	ActiveMinutes      float64 `json:"active_minutes"`
	DistractionMinutes float64 `json:"distraction_minutes"`
	NoDataMinutes      float64 `json:"no_data_minutes"`
	// Reported means the device sent something that day; a day it did not
	// is unknown for it, never zero.
	Reported bool `json:"reported"`
}

type activityDayUsage struct {
	Device  string  `json:"device"`
	Name    string  `json:"name"`
	AppName string  `json:"app_name,omitempty"`
	Label   string  `json:"label,omitempty"`
	Minutes float64 `json:"minutes"`
}

type activityDayStay struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Lat  float64   `json:"lat"`
	Lon  float64   `json:"lon"`
	Home bool      `json:"home"`
	// Name is the nearest named place within 100 m, empty without one.
	Name string `json:"name,omitempty"`
}

type activityDay struct {
	Date string `json:"date"`
	// Final means the day ended a day ago or more, so it is not expected to
	// change; the phone asks again only for days that are not.
	Final bool `json:"final"`
	// Minutes is how much of the day the row covers: 1440 for a past day
	// (1380 or 1500 when the clocks change), up to now for today.
	Minutes            float64             `json:"minutes"`
	DistractionMinutes float64             `json:"distraction_minutes"`
	Devices            []activityDayDevice `json:"devices"`
	// AgentMinutes is time coding agents ran; null when it could not be read.
	AgentMinutes *float64           `json:"agent_minutes"`
	Apps         []activityDayUsage `json:"apps"`
	Sites        []activityDayUsage `json:"sites"`
	// Cells is the day as activity.DayCells draws it, ten minutes a letter.
	Cells     string `json:"cells"`
	Positions int    `json:"positions"`
	// OutsideMinutes is time away from home; null without positions or
	// before home is known. DistanceKM is null without positions.
	OutsideMinutes *float64          `json:"outside_minutes"`
	DistanceKM     *float64          `json:"distance_km"`
	Stays          []activityDayStay `json:"stays"`
}

type activityDaysCache struct {
	mu   sync.Mutex
	days map[string]activityDay
}

func (c *activityDaysCache) get(key string) (activityDay, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	day, ok := c.days[key]
	return day, ok
}

func (c *activityDaysCache) put(key string, day activityDay) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.days == nil {
		c.days = map[string]activityDay{}
	}
	if len(c.days) >= activityDaysCacheSize {
		// The oldest days go first; they are the least asked for again.
		keys := make([]string, 0, len(c.days))
		for known := range c.days {
			keys = append(keys, known)
		}
		sort.Strings(keys)
		for _, old := range keys[:len(keys)/4] {
			delete(c.days, old)
		}
	}
	c.days[key] = day
}

// labelsVersion names the owner's labels as they are, so a day counted with
// other labels is counted again, and the phone knows to ask again too.
func labelsVersion(labels []activity.Label) string {
	sorted := append([]activity.Label(nil), labels...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].Match < sorted[j].Match
	})
	hash := fnv.New64a()
	for _, label := range sorted {
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x01", label.Kind, label.Match, label.Label)
	}
	return fmt.Sprintf("%016x", hash.Sum64())
}

// activityDays gives the owner one compact row per local day in
// [from, to] (at most 62 days, none in the future): distraction, screen
// time per device, agent time, top apps and sites, the day as cells, and
// where the phone was. Finished days are cached.
func (s *Server) activityDays(writer http.ResponseWriter, request *http.Request) {
	if !s.owner(writer, request) {
		return
	}
	ctx := request.Context()
	location, err := store.OwnerLocation(ctx, s.repository)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	now := time.Now().UTC()
	local := now.In(location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	last, first := today, today.AddDate(0, 0, -6)
	query := request.URL.Query()
	if value := query.Get("to"); value != "" {
		if last, err = time.ParseInLocation("2006-01-02", value, location); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_query", "to must be YYYY-MM-DD")
			return
		}
		first = last.AddDate(0, 0, -6)
	}
	if value := query.Get("from"); value != "" {
		if first, err = time.ParseInLocation("2006-01-02", value, location); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_query", "from must be YYYY-MM-DD")
			return
		}
	}
	if last.After(today) {
		writeError(writer, http.StatusBadRequest, "invalid_query", "to must not be in the future")
		return
	}
	if last.Before(first) {
		writeError(writer, http.StatusBadRequest, "invalid_query", "from must not be after to")
		return
	}
	days := []time.Time{}
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		if len(days) == activityDaysLimit {
			writeError(writer, http.StatusBadRequest, "invalid_query", fmt.Sprintf("the range must be at most %d days", activityDaysLimit))
			return
		}
		days = append(days, day)
	}
	labels, err := s.repository.ListActivityLabels(ctx)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	summaryLabels := store.LabelsForSummary(labels)
	version := labelsVersion(summaryLabels)
	rows := make([]activityDay, len(days))
	missing := []int{}
	for i, day := range days {
		if row, ok := s.dayRows.get(version + day.Format("2006-01-02")); ok {
			rows[i] = row
		} else {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		reader, err := s.newDayReader(ctx, days[missing[0]], days[missing[len(missing)-1]], now, location, summaryLabels)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		for _, i := range missing {
			row, err := reader.day(ctx, days[i])
			if err != nil {
				writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
				return
			}
			if row.Final {
				s.dayRows.put(version+row.Date, row)
			}
			rows[i] = row
		}
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"timezone": location.String(), "version": version, "generated_at": now,
		"from": first.Format("2006-01-02"), "to": last.Format("2006-01-02"), "days": rows,
	})
}

// dayReader reads what a run of days needs once: positions, coding records,
// app names and the map, then each day's activity records on their own.
type dayReader struct {
	repository store.Repository
	location   *time.Location
	now        time.Time
	labels     []activity.Label
	names      activity.AppNames
	coding     []activity.Record
	codingOK   bool
	fixes      []places.Fix
	withMap    bool
}

func (s *Server) newDayReader(ctx context.Context, first, last, now time.Time, location *time.Location, labels []activity.Label) (*dayReader, error) {
	reader := &dayReader{repository: s.repository, location: location, now: now, labels: labels}
	from, to := first.UTC(), last.AddDate(0, 0, 1).UTC()
	if to.After(now) {
		to = now
	}
	var err error
	if reader.names, err = s.repository.AppNames(ctx); err != nil {
		return nil, err
	}
	reader.coding, err = s.repository.CodingRecords(ctx, from)
	switch {
	case errors.Is(err, store.ErrActivityRangeTooLarge):
		// Agent time stays unknown; the rest of the days still read.
	case err != nil:
		return nil, err
	default:
		reader.codingOK = true
	}
	// Two weeks of nights before each day find home.
	if reader.fixes, err = s.repository.LocationFixes(ctx, from.Add(-store.HomeLookback), to); err != nil {
		return nil, err
	}
	sort.SliceStable(reader.fixes, func(i, j int) bool { return reader.fixes[i].At.Before(reader.fixes[j].At) })
	imported, err := s.repository.LatestMapImport(ctx)
	if err != nil {
		return nil, err
	}
	reader.withMap = imported != nil
	return reader, nil
}

// between is the positions in [from, to).
func (r *dayReader) between(from, to time.Time) []places.Fix {
	start := sort.Search(len(r.fixes), func(i int) bool { return !r.fixes[i].At.Before(from) })
	end := sort.Search(len(r.fixes), func(i int) bool { return !r.fixes[i].At.Before(to) })
	return r.fixes[start:end]
}

func (r *dayReader) day(ctx context.Context, day time.Time) (activityDay, error) {
	from := day.UTC()
	dayEnd := day.AddDate(0, 0, 1).UTC()
	to := dayEnd
	if to.After(r.now) {
		to = r.now
	}
	row := activityDay{Date: day.Format("2006-01-02"), Final: !r.now.Before(dayEnd.Add(activityDayFinalAfter)), Minutes: round1(to.Sub(from).Minutes()),
		Devices: []activityDayDevice{}, Apps: []activityDayUsage{}, Sites: []activityDayUsage{}, Stays: []activityDayStay{}}
	records, _, err := r.repository.ActivityRecords(ctx, from, to)
	if err != nil {
		return row, err
	}
	result := activity.Build(records, from, to)
	row.DistractionMinutes = activity.DistractionMinutes(result, r.labels)
	row.Cells = activity.DayCells(result, r.labels, from, dayEnd, to, activityDayCells)
	summaries := activity.Summarize(result, 10, r.labels)
	r.names.Summaries(summaries)
	for _, summary := range summaries {
		device := activityDayDevice{Device: summary.Device, ActiveMinutes: summary.ActiveMinutes, NoDataMinutes: summary.NoDataMinutes,
			Reported: summary.ActiveMinutes > 0 || summary.NoDataMinutes < row.Minutes-1}
		for _, segment := range result.Segments {
			if segment.Device == summary.Device && activity.LabelFor(r.labels, segment) == activity.Distraction {
				device.DistractionMinutes += segment.Duration().Minutes()
			}
		}
		device.DistractionMinutes = round1(device.DistractionMinutes)
		row.Devices = append(row.Devices, device)
		for _, app := range summary.Apps {
			row.Apps = append(row.Apps, activityDayUsage{Device: summary.Device, Name: app.Name, AppName: app.AppName, Label: app.Label, Minutes: app.Minutes})
		}
		for _, site := range summary.Sites {
			row.Sites = append(row.Sites, activityDayUsage{Device: summary.Device, Name: site.Name, Label: site.Label, Minutes: site.Minutes})
		}
	}
	if r.codingOK {
		minutes := activity.AgentMinutes(r.coding, from, to)
		row.AgentMinutes = &minutes
	}
	// Where the phone was, as the Places map and time outside read it.
	positions := r.between(from, to)
	row.Positions = len(positions)
	if len(positions) == 0 {
		return row, nil
	}
	recent := r.between(from.Add(-12*time.Hour), to)
	// Home as the day's time outside finds it (the two weeks before the day ends) ...
	if home := places.Home(r.between(to.Add(-store.HomeLookback), to), r.location); home != nil {
		var minutes float64
		for _, away := range activity.Outside(activity.DayInput{Fixes: recent, Home: home, From: from, To: to}) {
			minutes += away.End.Sub(away.Start).Minutes()
		}
		minutes = round1(minutes)
		row.OutsideMinutes = &minutes
	}
	// ... and as the day's map finds it (the two weeks before the day starts).
	home := places.Home(r.between(from.Add(-store.HomeLookback), to), r.location)
	stays := []places.Stay{}
	for _, stay := range places.Stays(recent, home) {
		if !stay.End.After(from) {
			continue
		}
		stays = append(stays, stay)
		if stay.Start.Before(from) {
			stay.Start = from
		}
		item := activityDayStay{From: stay.Start, To: stay.End, Home: stay.Home,
			Lat: math.Round(stay.Lat*1e5) / 1e5, Lon: math.Round(stay.Lon*1e5) / 1e5}
		if !stay.Home && r.withMap {
			nearby, err := r.repository.NearbyPlaces(ctx, stay.Lat, stay.Lon, 100, 1)
			if err != nil {
				return row, err
			}
			if len(nearby) > 0 {
				item.Name = nearby[0].Name
			}
		}
		row.Stays = append(row.Stays, item)
	}
	distance := math.Round(places.Moved(positions, stays, from, to)/10) / 100
	row.DistanceKM = &distance
	return row, nil
}

func round1(minutes float64) float64 { return math.Round(minutes*10) / 10 }
