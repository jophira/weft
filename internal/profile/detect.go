package profile

// Detection is one harness's detection outcome, expressed in terms this package
// can hold on its own. Adapters live in internal/harness; as with HarnessSync's
// class names, the harness is carried as a plain string so profile stays
// independent of the harness package.
type Detection struct {
	Name string
	// Found reports whether the harness is installed on this machine.
	Found bool
	// Signal is the evidence the detection matched ("config ~/.claude",
	// "binary ~/.local/bin/codex"), empty when nothing matched.
	Signal string
}

// TargetStatus is one row of the detect report: what was found, and how it
// relates to the profile's target list.
type TargetStatus struct {
	Detection
	// Targeted reports whether the profile already lists this harness.
	Targeted bool
	// New is a detected harness the profile does not list yet — the set --add
	// appends.
	New bool
	// Untraced is a listed harness that no longer detects. It is reported and
	// kept, never dropped: an uninstall on one machine must not silently stop
	// projection on another.
	Untraced bool
}

// TargetReport pairs each detection with the profile's target list, and appends
// a row for every configured target the detections did not cover. Those extra
// rows matter because a profile may name a harness this build knows nothing
// about; dropping it from the report would make it look unconfigured.
func TargetReport(p *Profile, detections []Detection) []TargetStatus {
	targeted := map[string]bool{}
	for _, name := range p.ResolvedTargets() {
		targeted[name] = true
	}

	rows := make([]TargetStatus, 0, len(detections))
	covered := map[string]bool{}
	for _, d := range detections {
		covered[d.Name] = true
		rows = append(rows, TargetStatus{
			Detection: d,
			Targeted:  targeted[d.Name],
			New:       d.Found && !targeted[d.Name],
			Untraced:  !d.Found && targeted[d.Name],
		})
	}
	for _, name := range p.ResolvedTargets() {
		if !covered[name] {
			rows = append(rows, TargetStatus{
				Detection: Detection{Name: name},
				Targeted:  true,
				Untraced:  true,
			})
		}
	}
	return rows
}

// AddDetected appends every newly detected harness to p's target list and
// migrates a legacy active_target into it, keeping the existing value first so
// the harness weft already applied to stays the head of the list. It returns
// the names it added and whether p changed at all.
//
// Nothing is ever removed. changed is true for the migration alone, so a
// profile still on active_target gains an explicit targets list even on a
// machine where no new harness turned up.
func AddDetected(p *Profile, rows []TargetStatus) (added []string, changed bool) {
	targets := append([]string(nil), p.ResolvedTargets()...)
	listed := map[string]bool{}
	for _, name := range targets {
		listed[name] = true
	}
	for _, r := range rows {
		if !r.New || listed[r.Name] {
			continue
		}
		targets = append(targets, r.Name)
		listed[r.Name] = true
		added = append(added, r.Name)
	}

	migrating := p.ActiveTarget != ""
	if len(added) == 0 && !migrating {
		return nil, false
	}
	p.Targets = targets
	p.ActiveTarget = "" // one source of truth: the list now carries it
	return added, true
}
