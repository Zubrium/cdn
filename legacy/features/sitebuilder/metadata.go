package sitebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	publicruntime "websiteengine/pkg/runtime"
)

type SiteMetadata struct {
	CanonicalDomain string                          `json:"canonicalDomain,omitempty"`
	Language        string                          `json:"language,omitempty"`
	NoIndex         bool                            `json:"noIndex,omitempty"`
	FormatVersion   int                             `json:"formatVersion"`
	SiteName        string                          `json:"siteName"`
	FaviconMediaID  string                          `json:"faviconMediaId,omitempty"`
	Description     string                          `json:"description,omitempty"`
	OGTitle         string                          `json:"ogTitle,omitempty"`
	OGDescription   string                          `json:"ogDescription,omitempty"`
	OGImageMediaID  string                          `json:"ogImageMediaId,omitempty"`
	Pages           map[string]SiteMetadataOverride `json:"pages,omitempty"`
}

type SiteMetadataOverride struct {
	NoIndex        *bool   `json:"noIndex,omitempty"`
	SiteName       *string `json:"siteName,omitempty"`
	FaviconMediaID *string `json:"faviconMediaId,omitempty"`
	Description    *string `json:"description,omitempty"`
	OGTitle        *string `json:"ogTitle,omitempty"`
	OGDescription  *string `json:"ogDescription,omitempty"`
	OGImageMediaID *string `json:"ogImageMediaId,omitempty"`
}

func DefaultSiteMetadata() SiteMetadata {
	return SiteMetadata{FormatVersion: 1, SiteName: "Website", Pages: map[string]SiteMetadataOverride{}}
}

func ParseSiteMetadata(raw []byte) (SiteMetadata, error) {
	var metadata SiteMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return SiteMetadata{}, fmt.Errorf("decode site metadata: %w", err)
	}
	if err := metadata.Validate(); err != nil {
		return SiteMetadata{}, err
	}
	if metadata.Pages == nil {
		metadata.Pages = map[string]SiteMetadataOverride{}
	}
	return metadata, nil
}

func (metadata SiteMetadata) Validate() error {
	if err := validateCanonicalDomain(metadata.CanonicalDomain); err != nil {
		return err
	}
	if metadata.Language != "" && !regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`).MatchString(metadata.Language) {
		return errors.New("language must be a BCP 47 language tag")
	}
	if metadata.FormatVersion != 1 {
		return errors.New("metadata formatVersion must be 1")
	}
	if len(strings.TrimSpace(metadata.SiteName)) == 0 || len([]rune(metadata.SiteName)) > 200 {
		return errors.New("siteName must contain between 1 and 200 characters")
	}
	for label, value := range map[string]string{
		"description": metadata.Description,
		"ogTitle":     metadata.OGTitle, "ogDescription": metadata.OGDescription,
	} {
		if len([]rune(value)) > 2048 {
			return fmt.Errorf("%s is too long", label)
		}
	}
	if err := validateMetadataMediaID("faviconMediaId", metadata.FaviconMediaID); err != nil {
		return err
	}
	if err := validateMetadataMediaID("ogImageMediaId", metadata.OGImageMediaID); err != nil {
		return err
	}
	for pageID, override := range metadata.Pages {
		if strings.TrimSpace(pageID) == "" || len(pageID) > 100 {
			return errors.New("page metadata key is invalid")
		}
		for label, value := range map[string]*string{
			"siteName":    override.SiteName,
			"description": override.Description, "ogTitle": override.OGTitle,
			"ogDescription": override.OGDescription,
		} {
			if value != nil && len([]rune(*value)) > 2048 {
				return fmt.Errorf("page %q %s is too long", pageID, label)
			}
		}
		if override.SiteName != nil && (strings.TrimSpace(*override.SiteName) == "" || len([]rune(*override.SiteName)) > 200) {
			return fmt.Errorf("page %q siteName must contain between 1 and 200 characters", pageID)
		}
		if override.FaviconMediaID != nil {
			if err := validateMetadataMediaID("faviconMediaId", *override.FaviconMediaID); err != nil {
				return fmt.Errorf("page %q: %w", pageID, err)
			}
		}
		if override.OGImageMediaID != nil {
			if err := validateMetadataMediaID("ogImageMediaId", *override.OGImageMediaID); err != nil {
				return fmt.Errorf("page %q: %w", pageID, err)
			}
		}
	}
	return nil
}

func validateMetadataMediaID(name, raw string) error {
	if raw == "" {
		return nil
	}
	if _, err := uuid.Parse(raw); err != nil {
		return fmt.Errorf("%s must reference a media object", name)
	}
	return nil
}

func (s *SnapshotService) validateMetadataMedia(ctx context.Context, queryer metadataQueryer, metadata SiteMetadata) error {
	ids := []string{metadata.FaviconMediaID, metadata.OGImageMediaID}
	for _, override := range metadata.Pages {
		if override.FaviconMediaID != nil {
			ids = append(ids, *override.FaviconMediaID)
		}
		if override.OGImageMediaID != nil {
			ids = append(ids, *override.OGImageMediaID)
		}
	}
	seen := map[string]bool{}
	for _, raw := range ids {
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		id, err := uuid.Parse(raw)
		if err != nil {
			return err
		}
		var ready bool
		if err := queryer.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_objects WHERE id=$1 AND status='ready' AND visibility='public' AND content_type LIKE 'image/%')`, id).Scan(&ready); err != nil {
			return err
		}
		if !ready {
			return fmt.Errorf("metadata image %s must be ready, public and an image", raw)
		}
	}
	return nil
}

func (metadata SiteMetadata) ForPage(pageID string) publicruntime.HeadMetadata {
	result := publicruntime.HeadMetadata{
		Language: metadata.Language,
		Title:    metadata.SiteName, Description: metadata.Description,
		FaviconURL: mediaURL(metadata.FaviconMediaID), OGTitle: metadata.OGTitle, OGDescription: metadata.OGDescription,
		OGImageURL: mediaURL(metadata.OGImageMediaID),
	}
	if override, ok := metadata.Pages[pageID]; ok {
		if override.SiteName != nil {
			result.Title = *override.SiteName
		}
		if override.FaviconMediaID != nil {
			result.FaviconURL = mediaURL(*override.FaviconMediaID)
		}
		if override.Description != nil {
			result.Description = *override.Description
		}
		if override.OGTitle != nil {
			result.OGTitle = *override.OGTitle
		}
		if override.OGDescription != nil {
			result.OGDescription = *override.OGDescription
		}
		if override.OGImageMediaID != nil {
			result.OGImageURL = mediaURL(*override.OGImageMediaID)
		}
	}
	if result.OGTitle == "" {
		result.OGTitle = result.Title
	}
	if result.OGDescription == "" {
		result.OGDescription = result.Description
	}
	return result
}

// ForPath never uses the request Host, preventing a proxy/Host header from
// changing canonical URLs. Branch noindex always wins over page settings.
func (metadata SiteMetadata) ForPath(pageID, requestPath string, branchIndexable bool) publicruntime.HeadMetadata {
	result := metadata.ForPage(pageID)
	indexable := branchIndexable && !metadata.NoIndex
	if page, ok := metadata.Pages[pageID]; ok && page.NoIndex != nil && *page.NoIndex {
		indexable = false
	}
	if indexable {
		result.Robots = "index,follow"
	} else {
		result.Robots = "noindex,nofollow"
	}
	if metadata.CanonicalDomain != "" && validateCanonicalDomain(metadata.CanonicalDomain) == nil {
		base, _ := url.Parse(strings.TrimRight(metadata.CanonicalDomain, "/"))
		base.Path = requestPath
		base.RawPath, base.RawQuery, base.Fragment = "", "", ""
		result.CanonicalURL = base.String()
		result.OGImageURL = absoluteMetadataURL(metadata.CanonicalDomain, result.OGImageURL)
	}
	return result
}

func absoluteMetadataURL(canonicalDomain, raw string) string {
	if canonicalDomain == "" || raw == "" {
		return raw
	}
	base, err := url.Parse(strings.TrimRight(canonicalDomain, "/") + "/")
	if err != nil {
		return raw
	}
	reference, err := url.Parse(raw)
	if err != nil || reference.IsAbs() {
		return raw
	}
	return base.ResolveReference(reference).String()
}

func validateCanonicalDomain(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("canonicalDomain must be an absolute http(s) origin without a path, credentials, query or fragment")
	}
	return nil
}

func mediaURL(id string) string {
	if id == "" {
		return ""
	}
	return "/media/" + id
}

type metadataQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
