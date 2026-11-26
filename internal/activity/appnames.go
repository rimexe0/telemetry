package activity

// AppNameKind is the phone's observation of the names it shows for the
// apps it has seen: {"labels": {package: name}}, sent when a name is new or
// changed.
const AppNameKind = "android.app_labels"

// AppNames maps an Android package to the name the phone shows for it. The
// names are only for showing: the package stays the identity everywhere,
// and the owner's labels still match packages.
type AppNames map[string]string

// Summaries names the phone's apps.
func (n AppNames) Summaries(summaries []DeviceSummary) {
	for i := range summaries {
		n.Apps(summaries[i].Device, summaries[i].Apps)
	}
}

// Apps names one device's ranked apps when it is the phone.
func (n AppNames) Apps(device string, apps []Usage) {
	if device != DevicePhone {
		return
	}
	for i := range apps {
		apps[i].AppName = n[apps[i].Name]
	}
}

// Review names the phone's apps in every block of a plan review.
func (n AppNames) Review(review *PlanReview) {
	for i := range review.Blocks {
		if review.Blocks[i].Observed != nil {
			n.Summaries(review.Blocks[i].Observed.Devices)
		}
	}
	n.Summaries(review.Outside.Devices)
}

// Segments names the phone's segments.
func (n AppNames) Segments(segments []Segment) {
	for i := range segments {
		if segments[i].Device == DevicePhone {
			segments[i].AppName = n[segments[i].App]
		}
	}
}

// Day names what the phone's chunks were spent on.
func (n AppNames) Day(day *Day) {
	for i := range day.Chunks {
		if day.Chunks[i].Device != DevicePhone {
			continue
		}
		for j := range day.Chunks[i].Items {
			day.Chunks[i].Items[j].AppName = n[day.Chunks[i].Items[j].Name]
		}
		for j := range day.Chunks[i].Segments {
			day.Chunks[i].Segments[j].AppName = n[day.Chunks[i].Segments[j].Name]
		}
	}
}

// Insights gives the names for the identifiers insights mention (Insight.Names).
func (n AppNames) Insights(insights []Insight) map[string]string {
	result := map[string]string{}
	for _, insight := range insights {
		for _, name := range insight.Names {
			if label := n[name]; label != "" {
				result[name] = label
			}
		}
	}
	return result
}
