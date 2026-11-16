package sitebuilder

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
)

type featureDefinitionAtVersion struct {
	SnapshotFeatureToken
	Version string
}

func featureDefinitionsAtVersion(builds []PinnedBuild) (map[string]featureDefinitionAtVersion, error) {
	definitions, err := SnapshotFeatureTokens(builds)
	if err != nil {
		return nil, err
	}
	versions := make(map[string]string, len(builds))
	for _, build := range builds {
		versions[build.Key] = build.Version
	}
	result := make(map[string]featureDefinitionAtVersion, len(definitions))
	for _, item := range definitions {
		result[item.FeatureKey+"/"+item.Definition.Key] = featureDefinitionAtVersion{item, versions[item.FeatureKey]}
	}
	return result, nil
}

func validateLegacyFeatureValues(values []LegacyFeatureTokenValue) error {
	for _, item := range values {
		if !featureKeyPattern.MatchString(item.FeatureKey) || !tokenKeyPattern.MatchString(item.TokenKey) || !validTokenName(item.Label) || !validTokenName(item.FeatureName) {
			return fmt.Errorf("invalid retained feature token")
		}
		value := item.Value
		if value.BaseTokenID != "" {
			if _, err := uuid.Parse(value.BaseTokenID); err != nil || value.Number != nil || value.Color != nil || value.Font != "" || value.Shadow != nil || value.Unit != "" {
				return fmt.Errorf("invalid retained base token reference")
			}
			switch item.Kind {
			case "color", "length", "number", "font", "shadow":
				continue
			default:
				return fmt.Errorf("invalid retained feature token kind")
			}
		}
		// Referenced font families may no longer be attached to this snapshot;
		// they are kept inert until a matching font is added again.
		fonts := map[string]bool{}
		if value.Font != "" && familyPattern.MatchString(value.Font) {
			fonts[value.Font] = true
		}
		if _, err := tokenValueCSS(item.Kind, value, fonts); err != nil {
			return fmt.Errorf("invalid retained feature token: %w", err)
		}
	}
	return nil
}

// ReconcileSnapshotDesignSystem moves incompatible overrides to inert retained
// values, preserving them verbatim in a child snapshot without changing its
// parent. Compatible retained values are restored when the corresponding
// feature/type returns and no active override has replaced them.
// Call it only for an inherited theme after the selected builds change; a
// client-supplied theme must instead pass strict NormalizeSnapshotDesignSystem.
func ReconcileSnapshotDesignSystem(value DesignSystem, oldBuilds, newBuilds []PinnedBuild) (DesignSystem, []string, error) {
	if value.FormatVersion != 2 {
		return value, nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return DesignSystem{}, nil, err
	}
	var result DesignSystem
	if err := json.Unmarshal(raw, &result); err != nil {
		return DesignSystem{}, nil, err
	}
	oldDefs, err := featureDefinitionsAtVersion(oldBuilds)
	if err != nil {
		return DesignSystem{}, nil, err
	}
	newDefs, err := featureDefinitionsAtVersion(newBuilds)
	if err != nil {
		return DesignSystem{}, nil, err
	}
	bases := make(map[string]BaseToken, len(result.BaseTokens))
	for _, base := range result.BaseTokens {
		bases[base.ID] = base
	}
	fonts := make(map[string]bool, len(result.Fonts))
	for _, font := range result.Fonts {
		fonts[font.Family] = true
	}
	compatible := func(kind string, value TokenValue) bool {
		if value.BaseTokenID != "" {
			base, found := bases[value.BaseTokenID]
			return found && base.Kind == kind && value.Number == nil && value.Color == nil && value.Font == "" && value.Shadow == nil && value.Unit == ""
		}
		_, err := tokenValueCSS(kind, value, fonts)
		return err == nil
	}
	warnings := []string{}
	reconcile := func(active map[string]TokenValue, legacy []LegacyFeatureTokenValue, context string) (map[string]TokenValue, []LegacyFeatureTokenValue, error) {
		if active == nil {
			active = map[string]TokenValue{}
		}
		keys := make([]string, 0, len(active))
		for key := range active {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			old, known := oldDefs[key]
			if !known {
				return nil, nil, fmt.Errorf("feature token %q is not present in the parent snapshot", key)
			}
			current, present := newDefs[key]
			if present && current.Definition.Kind == old.Definition.Kind && compatible(current.Definition.Kind, active[key]) {
				continue
			}
			legacy = append(legacy, LegacyFeatureTokenValue{FeatureKey: old.FeatureKey, FeatureName: old.FeatureName, TokenKey: old.Definition.Key, Label: old.Definition.Label, Kind: old.Definition.Kind, Version: old.Version, Value: active[key]})
			delete(active, key)
			warnings = append(warnings, fmt.Sprintf("%s: прежняя настройка «%s» сохранена, но не применяется", context, old.Definition.Label))
		}
		// Restore the most recent compatible retained value for a key, only
		// when a current explicit override is not already present.
		removed := make(map[int]bool)
		for index := len(legacy) - 1; index >= 0; index-- {
			item := legacy[index]
			key := item.FeatureKey + "/" + item.TokenKey
			definition, present := newDefs[key]
			if _, exists := active[key]; !exists && present && definition.Definition.Kind == item.Kind && compatible(item.Kind, item.Value) {
				active[key] = item.Value
				removed[index] = true
				warnings = append(warnings, fmt.Sprintf("%s: восстановлена настройка «%s»", context, item.Label))
			}
		}
		kept := make([]LegacyFeatureTokenValue, 0, len(legacy)-len(removed))
		for index, item := range legacy {
			if !removed[index] {
				kept = append(kept, item)
			}
		}
		return active, kept, nil
	}
	result.FeatureValues, result.LegacyFeatureValues, err = reconcile(result.FeatureValues, result.LegacyFeatureValues, "Общие настройки")
	if err != nil {
		return DesignSystem{}, nil, err
	}
	for i := range result.Breakpoints {
		bp := &result.Breakpoints[i]
		bp.FeatureValues, bp.LegacyFeatureValues, err = reconcile(bp.FeatureValues, bp.LegacyFeatureValues, bp.Name)
		if err != nil {
			return DesignSystem{}, nil, err
		}
	}
	result, err = NormalizeSnapshotDesignSystem(result, newBuilds)
	return result, warnings, err
}
