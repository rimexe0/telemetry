package activity

import (
	"sort"
	"time"
)

// Usage is the time spent on one app, site or window.
type Usage struct {
	Name     string  `json:"name"`
	App      string  `json:"app,omitempty"`
	Minutes  float64 `json:"minutes"`
	Sessions int     `json:"sessions"`
}

type DeviceSummary struct {
	Device        string     `json:"device"`
	ActiveMinutes float64    `json:"active_minutes"`
	Apps          []Usage    `json:"apps"`
	Sites         []Usage    `json:"sites,omitempty"`
	Titles        []Usage    `json:"titles,omitempty"`
	LastRecord    *time.Time `json:"last_record,omitempty"`
	// NoDataMinutes is time in the range with no records from this device.
	NoDataMinutes float64 `json:"no_data_minutes"`
}

// Summarize totals a Result. limit bounds each ranked list.
func Summarize(result Result, limit int) []DeviceSummary {
	summaries := []DeviceSummary{}
	for _, device := range []string{DeviceMac, DevicePhone} {
		summary := DeviceSummary{Device: device}
		if last, ok := result.LastRecord[device]; ok {
			summary.LastRecord = &last
		}
		apps, sites, titles := map[string]*Usage{}, map[string]*Usage{}, map[string]*Usage{}
		for _, segment := range result.Segments {
			if segment.Device != device {
				continue
			}
			minutes := segment.Duration().Minutes()
			summary.ActiveMinutes += minutes
			add(apps, segment.App, "", minutes)
			if segment.Domain != "" {
				add(sites, segment.Domain, segment.App, minutes)
			}
			if segment.Title != "" {
				add(titles, segment.App+"\x00"+segment.Title, segment.App, minutes)
			}
		}
		for _, gap := range result.Gaps {
			if gap.Device == device {
				summary.NoDataMinutes += gap.End.Sub(gap.Start).Minutes()
			}
		}
		summary.Apps = ranked(apps, limit)
		summary.Sites = ranked(sites, limit)
		summary.Titles = ranked(titles, limit)
		for i := range summary.Titles {
			summary.Titles[i].Name = summary.Titles[i].Name[len(summary.Titles[i].App)+1:]
		}
		summary.ActiveMinutes = round(summary.ActiveMinutes)
		summary.NoDataMinutes = round(summary.NoDataMinutes)
		summaries = append(summaries, summary)
	}
	return summaries
}

func add(totals map[string]*Usage, key, app string, minutes float64) {
	usage, ok := totals[key]
	if !ok {
		usage = &Usage{Name: key, App: app}
		totals[key] = usage
	}
	usage.Minutes += minutes
	usage.Sessions++
}

func ranked(totals map[string]*Usage, limit int) []Usage {
	result := make([]Usage, 0, len(totals))
	for _, usage := range totals {
		usage.Minutes = round(usage.Minutes)
		result = append(result, *usage)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Minutes != result[j].Minutes {
			return result[i].Minutes > result[j].Minutes
		}
		return result[i].Name < result[j].Name
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

func round(minutes float64) float64 { return float64(int64(minutes*10+0.5)) / 10 }
