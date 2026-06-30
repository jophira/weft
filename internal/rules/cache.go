package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CacheFileName is the conventional name of the generated resolution cache,
// stored alongside the rules tree. It is YAML, so loadTree (which reads only
// *.md) and Fingerprint (likewise) ignore it — the cache never affects its own
// fingerprint.
const CacheFileName = "signals.yaml"

// cacheGeneratorVersion is bumped when the cache schema changes; a mismatch
// forces a rebuild rather than trusting an incompatible cache.
const cacheGeneratorVersion = 1

// cacheEntry is one label's pre-resolved metadata in the cache. The body is not
// stored — only its hash, for audit — so the cache stays small and bodies are
// read on demand from File for the loaded subset.
type cacheEntry struct {
	Detect   string   `yaml:"detect,omitempty"`
	Extends  []string `yaml:"extends,omitempty"`
	Priority int      `yaml:"priority,omitempty"`
	File     string   `yaml:"file"`
	BodyHash string   `yaml:"body_hash"`
}

// cacheMeta is the cache header carrying the staleness fingerprint.
type cacheMeta struct {
	GeneratorVersion  int       `yaml:"generator_version"`
	SourceFingerprint string    `yaml:"source_fingerprint"`
	BuiltAt           time.Time `yaml:"built_at"`
}

// Cache is the generated, never-hand-edited resolution index for a rules tree.
type Cache struct {
	Meta   cacheMeta             `yaml:"meta"`
	Labels map[string]cacheEntry `yaml:"labels"`
}

// cacheHeader is the comment written atop a generated cache file.
const cacheHeader = "# GENERATED — do not edit. Source of truth: front-matter in **/*.md\n"

// DefaultCachePath returns the conventional cache location for a rules tree.
func DefaultCachePath(rulesRoot string) string {
	return filepath.Join(rulesRoot, CacheFileName)
}

// Fingerprint hashes the identity of every *.md file under rulesRoot using only
// stat metadata — relative path, modification time and size — so it is cheap
// (no file reads) and safe to compute on the resolve hot path. Any rule edit,
// addition or removal changes the result; unrelated repo churn does not. mtime
// can change without content changing (e.g. a git checkout), which merely
// triggers a harmless rebuild — it never serves stale rules.
func Fingerprint(rulesRoot string) (string, error) {
	type entry struct {
		rel  string
		mod  int64
		size int64
	}
	var entries []entry
	err := filepath.WalkDir(rulesRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ruleFileExt {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		rel, relErr := filepath.Rel(rulesRoot, path)
		if relErr != nil {
			return relErr
		}
		entries = append(entries, entry{rel: filepath.ToSlash(rel), mod: info.ModTime().UnixNano(), size: info.Size()})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", e.rel, e.mod, e.size)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// BuildCache walks rulesRoot, parses front-matter, and returns the cache plus
// any rules skipped during the walk (e.g. duplicate labels).
func BuildCache(rulesRoot string, now time.Time) (Cache, []SkippedRule, error) {
	rules, skipped, err := loadTree(rulesRoot)
	if err != nil {
		return Cache{}, nil, err
	}
	fp, err := Fingerprint(rulesRoot)
	if err != nil {
		return Cache{}, nil, err
	}
	return newCacheFrom(rules, rulesRoot, fp, now), skipped, nil
}

// newCacheFrom assembles a Cache from already-parsed rules.
func newCacheFrom(rules []Rule, rulesRoot, fingerprint string, now time.Time) Cache {
	c := Cache{
		Meta: cacheMeta{
			GeneratorVersion:  cacheGeneratorVersion,
			SourceFingerprint: fingerprint,
			BuiltAt:           now,
		},
		Labels: make(map[string]cacheEntry, len(rules)),
	}
	for _, r := range rules {
		rel, err := filepath.Rel(rulesRoot, r.Path)
		if err != nil {
			rel = r.Path
		}
		c.Labels[r.Label] = cacheEntry{
			Detect:   r.Detect,
			Extends:  r.Extends,
			Priority: r.Priority,
			File:     filepath.ToSlash(rel),
			BodyHash: hashString(r.Body),
		}
	}
	return c
}

// LoadCache reads and parses a cache file.
func LoadCache(path string) (Cache, error) {
	data, err := os.ReadFile(path) //nolint:gosec // cache path derived from rules root
	if err != nil {
		return Cache{}, err
	}
	var c Cache
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Cache{}, err
	}
	return c, nil
}

// Save writes the cache to path with the generated-file header.
func (c Cache) Save(path string) error {
	body, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(cacheHeader), body...), 0o644) //nolint:gosec // non-secret generated index
}

// toMetas converts the cache into the ruleMeta slice the resolver consumes,
// re-absolutising each File against rulesRoot.
func (c Cache) toMetas(rulesRoot string) []ruleMeta {
	metas := make([]ruleMeta, 0, len(c.Labels))
	for label, e := range c.Labels {
		metas = append(metas, ruleMeta{
			Label:    label,
			Detect:   e.Detect,
			Extends:  e.Extends,
			Priority: e.Priority,
			Path:     filepath.Join(rulesRoot, filepath.FromSlash(e.File)),
		})
	}
	return metas
}

// fresh reports whether the cache matches the given fingerprint and schema
// version, i.e. can be trusted without rebuilding.
func (c Cache) fresh(fingerprint string) bool {
	return c.Meta.GeneratorVersion == cacheGeneratorVersion && c.Meta.SourceFingerprint == fingerprint
}

// CacheOptions controls the cache behaviour of ResolveWithCache.
type CacheOptions struct {
	// Path overrides the cache file location; empty uses DefaultCachePath.
	Path string
	// Disabled bypasses the cache entirely (always resolve from the tree).
	Disabled bool
	// ForceRebuild ignores any existing cache and regenerates it.
	ForceRebuild bool
	// Now supplies the build timestamp; defaults to time.Now().UTC().
	Now func() time.Time
}

// CacheStatus reports what the cache did during a resolve, for the audit trail.
type CacheStatus struct {
	// Used is true when the resolve was served from a fresh cache hit.
	Used bool
	// StaleRebuilt is true when an existing cache's fingerprint did not match
	// and was rebuilt in place (the self-healing path).
	StaleRebuilt bool
	// Wrote is true when a cache file was (re)written during this resolve.
	Wrote bool
	// Fingerprint is the source fingerprint computed for this resolve.
	Fingerprint string
	// Path is the cache file location considered.
	Path string
}

// ResolveWithCache resolves rulesRoot against ctx using the signals cache when
// possible, and is the optimization-only entry point:
//
//   - no cache file        → resolve from the tree, write a cache (correct).
//   - fresh cache          → resolve from the cache, no tree parse (fast).
//   - stale cache          → rebuild in place, resolve from the tree, rewrite
//     the cache, and report StaleRebuilt (self-healing).
//
// In every case the assembled selection is identical to a direct Resolve; the
// cache only changes speed, never correctness, so forgetting to run a manual
// build is harmless.
func ResolveWithCache(rulesRoot string, ctx Context, ev Evaluator, opts CacheOptions) (Resolution, CacheStatus, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	cachePath := opts.Path
	if cachePath == "" {
		cachePath = DefaultCachePath(rulesRoot)
	}
	status := CacheStatus{Path: cachePath}

	if opts.Disabled {
		res, err := Resolve(rulesRoot, ctx, ev)
		return res, status, err
	}

	fp, err := Fingerprint(rulesRoot)
	if err != nil {
		return Resolution{}, status, err
	}
	status.Fingerprint = fp

	if !opts.ForceRebuild {
		if cached, loadErr := LoadCache(cachePath); loadErr == nil {
			if cached.fresh(fp) {
				res, resErr := resolveMeta(cached.toMetas(rulesRoot), nil, ctx, ev, readFileBody)
				status.Used = true
				return res, status, resErr
			}
			status.StaleRebuilt = true // a cache existed but no longer matches
		}
	}

	// (Re)build path: walk once, persist the cache, resolve from memory.
	rules, skipped, err := loadTree(rulesRoot)
	if err != nil {
		return Resolution{}, status, err
	}
	cache := newCacheFrom(rules, rulesRoot, fp, now().UTC())
	if saveErr := cache.Save(cachePath); saveErr == nil {
		status.Wrote = true
	}
	res, resErr := resolveRulesInMemory(rules, skipped, ctx, ev)
	return res, status, resErr
}
