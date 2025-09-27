package graph

import (
	"encoding/json"
	"fmt"

	"go.podman.io/image/v5/docker/reference"
)

type CanonicalReference struct {
	reference.Canonical
}

func (r CanonicalReference) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.String())
}

func (r *CanonicalReference) UnmarshalJSON(data []byte) error {
	var ref string
	if err := json.Unmarshal(data, &ref); err != nil {
		return err
	}
	namedRef, err := reference.ParseNamed(ref)
	if err != nil {
		return err
	}
	canonicalRef, ok := namedRef.(reference.Canonical)
	if !ok {
		return fmt.Errorf("%s is not a canonical reference", ref)
	}
	r.Canonical = canonicalRef
	return nil
}
