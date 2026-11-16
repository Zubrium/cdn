package runtime

import (
	"fmt"
)

// PlacementRoot is the public, non-content portion of a placement required
// before its existing feature ABI is mounted by the browser runtime.
type PlacementRoot struct {
	ID         string
	Slot       string
	FeatureKey string
}

type placementRootGroups struct {
	PageKind        string
	SnapshotID      string
	ContentRevision int64
	Background      []PlacementRoot
	Header          []PlacementRoot
	Main            []PlacementRoot
	Footer          []PlacementRoot
}

func groupPlacementRoots(pageKind, snapshotID string, revision int64, placements []PlacementRoot) (placementRootGroups, error) {
	groups := placementRootGroups{PageKind: pageKind, SnapshotID: snapshotID, ContentRevision: revision}
	for _, placement := range placements {
		switch placement.Slot {
		case "background":
			groups.Background = append(groups.Background, placement)
		case "header":
			groups.Header = append(groups.Header, placement)
		case "main":
			groups.Main = append(groups.Main, placement)
		case "footer":
			groups.Footer = append(groups.Footer, placement)
		default:
			return placementRootGroups{}, fmt.Errorf("unknown public placement slot %q", placement.Slot)
		}
	}
	return groups, nil
}
