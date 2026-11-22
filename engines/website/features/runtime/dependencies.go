package runtime

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrDependency = errors.New("invalid feature dependency graph")
	ErrCycle      = errors.New("feature dependency cycle")
	ErrLock       = errors.New("feature dependency lock mismatch")
)

// Dependency is one manifest-declared provider requirement. The range is a
// bounded SemVer expression; clients cannot pin a package digest directly.
type Dependency struct {
	FeatureID string
	Range     string
}

// Candidate is one catalog entry. Digest and public API version are immutable.
type Candidate struct {
	FeatureID  string
	Version    string
	Digest     string
	APIVersion string
	Depends    []Dependency
}

// LockEntry fixes the exact provider used by a ready snapshot.
type LockEntry struct {
	FeatureID  string `json:"featureId"`
	Version    string `json:"version"`
	Digest     string `json:"digest"`
	APIVersion string `json:"apiVersion"`
}

// Resolver resolves manifest dependency ranges against a catalog and an
// existing immutable lock. Resolution is deterministic and cycle-free.
type Resolver struct {
	catalog map[string][]Candidate
}

func NewResolver(catalog []Candidate) (*Resolver, error) {
	index := make(map[string][]Candidate, len(catalog))
	for _, candidate := range catalog {
		if candidate.FeatureID == "" || !semverPattern.MatchString(candidate.Version) ||
			candidate.Digest == "" || candidate.APIVersion == "" {
			return nil, fmt.Errorf("%w: catalog candidate", ErrDependency)
		}
		index[candidate.FeatureID] = append(index[candidate.FeatureID], candidate)
	}
	for featureID, candidates := range index {
		seenDigests := make(map[string]string, len(candidates))
		seenVersions := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			if previous, exists := seenDigests[candidate.Digest]; exists && previous != candidate.Version {
				return nil, fmt.Errorf("%w: digest reused by %s", ErrDependency, featureID)
			}
			if _, exists := seenVersions[candidate.Version]; exists {
				return nil, fmt.Errorf("%w: ambiguous version %s@%s", ErrDependency, featureID, candidate.Version)
			}
			seenDigests[candidate.Digest] = candidate.Version
			seenVersions[candidate.Version] = struct{}{}
		}
	}
	return &Resolver{catalog: index}, nil
}

// Resolve returns a stable lock entries slice. When a previous lock entry still
// satisfies all current constraints it is preserved; otherwise the highest
// compatible catalog version is selected.
func (r *Resolver) Resolve(requirements []Dependency, previous []LockEntry) ([]LockEntry, error) {
	constraints := make(map[string][]semverRange)
	for _, requirement := range requirements {
		if requirement.FeatureID == "" {
			return nil, fmt.Errorf("%w: empty feature id", ErrDependency)
		}
		parsed, err := parseSemverRange(requirement.Range)
		if err != nil {
			return nil, err
		}
		constraints[requirement.FeatureID] = append(constraints[requirement.FeatureID], parsed)
	}
	locked := make(map[string]LockEntry, len(previous))
	for _, entry := range previous {
		if entry.FeatureID == "" || !semverPattern.MatchString(entry.Version) || entry.Digest == "" || entry.APIVersion == "" {
			return nil, fmt.Errorf("%w: lock entry", ErrLock)
		}
		if _, exists := locked[entry.FeatureID]; exists {
			return nil, fmt.Errorf("%w: duplicate lock entry", ErrLock)
		}
		available := r.catalog[entry.FeatureID]
		stillPinned := false
		for _, candidate := range available {
			if candidate.Version == entry.Version && candidate.Digest == entry.Digest && candidate.APIVersion == entry.APIVersion {
				stillPinned = true
				break
			}
		}
		if !stillPinned {
			return nil, fmt.Errorf("%w: imported dependency %s changed after lock", ErrLock, entry.FeatureID)
		}
		locked[entry.FeatureID] = entry
	}

	resolved := make(map[string]LockEntry)
	visiting := make(map[string]bool)
	var visit func(Dependency) error
	visit = func(requirement Dependency) error {
		if visiting[requirement.FeatureID] {
			return fmt.Errorf("%w: %s", ErrCycle, requirement.FeatureID)
		}
		if _, done := resolved[requirement.FeatureID]; done {
			return nil
		}
		ranges := constraints[requirement.FeatureID]
		if len(ranges) == 0 {
			parsed, err := parseSemverRange(requirement.Range)
			if err != nil {
				return err
			}
			ranges = []semverRange{parsed}
		}
		candidates := r.catalog[requirement.FeatureID]
		if len(candidates) == 0 {
			return fmt.Errorf("%w: unknown provider %s", ErrDependency, requirement.FeatureID)
		}
		chosen, err := chooseCandidate(candidates, ranges, locked[requirement.FeatureID])
		if err != nil {
			return err
		}
		visiting[requirement.FeatureID] = true
		for _, nested := range chosen.Depends {
			parsed, err := parseSemverRange(nested.Range)
			if err != nil {
				visiting[requirement.FeatureID] = false
				return err
			}
			constraints[nested.FeatureID] = append(constraints[nested.FeatureID], parsed)
			if err := visit(nested); err != nil {
				visiting[requirement.FeatureID] = false
				return err
			}
		}
		delete(visiting, requirement.FeatureID)
		resolved[requirement.FeatureID] = LockEntry{
			FeatureID: chosen.FeatureID, Version: chosen.Version,
			Digest: chosen.Digest, APIVersion: chosen.APIVersion,
		}
		return nil
	}
	for _, requirement := range requirements {
		if err := visit(requirement); err != nil {
			return nil, err
		}
	}

	entries := make([]LockEntry, 0, len(resolved))
	for _, entry := range resolved {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].FeatureID < entries[j].FeatureID })
	return entries, nil
}

func chooseCandidate(candidates []Candidate, ranges []semverRange, locked LockEntry) (Candidate, error) {
	satisfiesAll := func(candidate Candidate) bool {
		version, err := parseVersion(candidate.Version)
		if err != nil {
			return false
		}
		for _, constraint := range ranges {
			if !constraint.allows(version) {
				return false
			}
		}
		return true
	}
	if locked.Version != "" {
		for _, candidate := range candidates {
			if candidate.Version == locked.Version && candidate.Digest == locked.Digest &&
				candidate.APIVersion == locked.APIVersion && satisfiesAll(candidate) {
				return candidate, nil
			}
		}
	}
	var best *Candidate
	for i := range candidates {
		candidate := candidates[i]
		if !satisfiesAll(candidate) {
			continue
		}
		if best == nil || compareVersion(candidate.Version, best.Version) > 0 {
			best = &candidate
		}
	}
	if best == nil {
		return Candidate{}, fmt.Errorf("%w: no compatible version", ErrDependency)
	}
	return *best, nil
}

type semverRange struct {
	min    [3]int
	max    [3]int
	hasMin bool
	hasMax bool
	maxInc bool
}

func (value semverRange) allows(version [3]int) bool {
	if value.hasMin && compareParts(version, value.min) < 0 {
		return false
	}
	if value.hasMax {
		cmp := compareParts(version, value.max)
		if cmp > 0 || (cmp == 0 && !value.maxInc) {
			return false
		}
	}
	return true
}

func parseSemverRange(expression string) (semverRange, error) {
	value := strings.TrimSpace(expression)
	if value == "" {
		return semverRange{}, fmt.Errorf("%w: empty range", ErrDependency)
	}
	parse := func(text string) ([3]int, error) {
		parts := strings.Split(text, ".")
		if len(parts) != 3 {
			return [3]int{}, fmt.Errorf("%w: version", ErrDependency)
		}
		var result [3]int
		for i, part := range parts {
			number, err := strconv.Atoi(part)
			if err != nil || number < 0 || (i > 0 && part != strconv.Itoa(number)) {
				return [3]int{}, fmt.Errorf("%w: version", ErrDependency)
			}
			result[i] = number
		}
		return result, nil
	}
	switch {
	case strings.HasPrefix(value, "^"):
		min, err := parse(strings.TrimPrefix(value, "^"))
		if err != nil || min[0] == 0 && (min[1] != 0 || min[2] != 0) {
			return semverRange{}, fmt.Errorf("%w: caret range", ErrDependency)
		}
		max := min
		if min[0] > 0 {
			max = [3]int{min[0] + 1, 0, 0}
		} else if min[1] > 0 {
			max = [3]int{0, min[1] + 1, 0}
		} else {
			max = [3]int{0, 0, min[2] + 1}
		}
		return semverRange{min: min, max: max, hasMin: true, hasMax: true}, nil
	case strings.HasPrefix(value, "~"):
		min, err := parse(strings.TrimPrefix(value, "~"))
		if err != nil {
			return semverRange{}, err
		}
		return semverRange{min: min, max: [3]int{min[0], min[1] + 1, 0}, hasMin: true, hasMax: true}, nil
	case strings.HasPrefix(value, ">="):
		min, err := parse(strings.TrimPrefix(value, ">="))
		return semverRange{min: min, hasMin: true}, err
	case strings.HasPrefix(value, "<"):
		max, err := parse(strings.TrimPrefix(value, "<"))
		return semverRange{max: max, hasMax: true}, err
	default:
		min, err := parse(value)
		return semverRange{min: min, max: min, hasMin: true, hasMax: true, maxInc: true}, err
	}
}

func parseVersion(value string) ([3]int, error) {
	base := strings.SplitN(value, "-", 2)[0]
	base = strings.SplitN(base, "+", 2)[0]
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("%w: version", ErrDependency)
	}
	var result [3]int
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return [3]int{}, fmt.Errorf("%w: version", ErrDependency)
		}
		result[i] = number
	}
	return result, nil
}

func compareVersion(left, right string) int {
	a, _ := parseVersion(left)
	b, _ := parseVersion(right)
	return compareParts(a, b)
}

func compareParts(left, right [3]int) int {
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}
