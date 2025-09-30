package graph

import (
	"cmp"
	"fmt"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/util"
	"go.podman.io/image/v5/docker/reference"
	"k8s.io/apimachinery/pkg/util/sets"
)

type Node struct {
	Name           string
	Version        semver.Version
	Release        *string
	ReleaseDate    time.Time
	ImageReference reference.Canonical

	LifecyclePhase            LifecyclePhase
	SupportedPlatformVersions sets.Set[MajorMinor]

	id     int64
	idOnce sync.Once
}

func (n *Node) ID() int64 {
	n.idOnce.Do(func() {
		n.id = int64(util.HashString(n.NVR()))
	})
	return n.id
}

func (n *Node) NVR() string {
	return fmt.Sprintf("%s.v%s", n.Name, n.VR())
}

func (n *Node) VR() string {
	rel := ""
	if n.Release != nil && *n.Release != "" {
		rel = fmt.Sprintf("_%s", *n.Release)
	}
	return fmt.Sprintf("%s%s", n.Version, rel)
}

func (n *Node) Compare(other *Node) int {
	if v := n.Version.Compare(other.Version); v != 0 {
		return v
	}
	return cmp.Compare(n.Name, other.Name)
}
