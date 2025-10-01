package graph

import (
	"errors"
	"fmt"
)

type Template struct {
	Schema         string               `json:"schema"`
	Name           string               `json:"name"`
	VersionStreams []VersionStream      `json:"versionStreams"`
	Images         []CanonicalReference `json:"images"`
}

const SchemaCincinnati = `olm.cincinnati`

func (t *Template) Validate() error {
	var errs []error
	if t.Schema != SchemaCincinnati {
		errs = append(errs, fmt.Errorf("schema must be %q", SchemaCincinnati))
	}
	if t.Name == "" {
		errs = append(errs, fmt.Errorf("name must be set"))
	}
	if len(t.VersionStreams) == 0 {
		errs = append(errs, errors.New("no streams found in template"))
	}
	if len(t.Images) == 0 {
		errs = append(errs, errors.New("no images found in template"))
	}
	for _, version := range t.VersionStreams {
		if err := version.LifecycleDates.ValidateOrder(); err != nil {
			errs = append(errs, fmt.Errorf("version %q invalid: %v", version.Version, err))
		}
	}
	return errors.Join(errs...)
}
