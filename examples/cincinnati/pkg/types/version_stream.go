package types

import (
	"github.com/blang/semver/v4"
)

type VersionStream struct {
	Version                   MajorMinor     `json:"version"`
	MinimumUpdateVersion      semver.Version `json:"minimumUpdateVersion"`
	LifecycleDates            LifecycleDates `json:"lifecycleDates"`
	SupportedPlatformVersions []MajorMinor   `json:"supportedPlatformVersions"`
}
