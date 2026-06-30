package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// ManifestEntry is one loaded rule as recorded in the audit manifest.
type ManifestEntry struct {
	Label    string `json:"label"`
	Path     string `json:"path"`
	Direct   bool   `json:"direct"`
	Priority int    `json:"priority"`
	BodyHash string `json:"body_hash"`
}

// ManifestSkip is one discovered-but-not-loaded rule in the manifest.
type ManifestSkip struct {
	Label  string `json:"label,omitempty"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// CacheInfo records what the resolution cache did, for the audit trail.
type CacheInfo struct {
	Used         bool   `json:"used"`
	StaleRebuilt bool   `json:"stale_rebuilt"`
	Wrote        bool   `json:"wrote"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	Path         string `json:"path,omitempty"`
}

// Manifest is the audit record of a resolve: what loaded, what was skipped, and
// a ResolutionHash that changes only when the *selection* changes — not when
// unrelated repo content changes. That makes it the natural dedup key for an
// append-only resolve log (hash the resolution, not the repo).
type Manifest struct {
	GeneratedAt    time.Time       `json:"generated_at"`
	RulesRoot      string          `json:"rules_root"`
	RepoRoot       string          `json:"repo_root"`
	ResolutionHash string          `json:"resolution_hash"`
	Cache          *CacheInfo      `json:"cache,omitempty"`
	Loaded         []ManifestEntry `json:"loaded"`
	Skipped        []ManifestSkip  `json:"skipped,omitempty"`
	UnknownExtends []string        `json:"unknown_extends,omitempty"`
}

// WithCache returns m annotated with the cache status from a ResolveWithCache.
func (m Manifest) WithCache(s CacheStatus) Manifest {
	m.Cache = &CacheInfo{
		Used:         s.Used,
		StaleRebuilt: s.StaleRebuilt,
		Wrote:        s.Wrote,
		Fingerprint:  s.Fingerprint,
		Path:         s.Path,
	}
	return m
}

// NewManifest builds the audit manifest for a resolution. now is injected so
// callers (and tests) control the timestamp.
func NewManifest(res Resolution, rulesRoot, repoRoot string, now time.Time) Manifest {
	loaded := make([]ManifestEntry, 0, len(res.Loaded))
	hasher := sha256.New()
	for _, lr := range res.Loaded {
		bodyHash := hashString(lr.Body)
		loaded = append(loaded, ManifestEntry{
			Label:    lr.Label,
			Path:     lr.Path,
			Direct:   lr.Direct,
			Priority: lr.Priority,
			BodyHash: bodyHash,
		})
		// Fold label + body hash, in load order, into the resolution hash.
		hasher.Write([]byte(lr.Label))
		hasher.Write([]byte{0})
		hasher.Write([]byte(bodyHash))
		hasher.Write([]byte{'\n'})
	}

	skipped := make([]ManifestSkip, 0, len(res.Skipped))
	for _, s := range res.Skipped {
		skipped = append(skipped, ManifestSkip(s))
	}

	return Manifest{
		GeneratedAt:    now,
		RulesRoot:      rulesRoot,
		RepoRoot:       repoRoot,
		ResolutionHash: hex.EncodeToString(hasher.Sum(nil)),
		Loaded:         loaded,
		Skipped:        skipped,
		UnknownExtends: res.UnknownExtends,
	}
}

// hashString returns the hex SHA-256 of s.
func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
