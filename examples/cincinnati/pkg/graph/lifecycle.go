package graph

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

type LifecyclePhase int

const (
	LifecyclePhaseFullSupport LifecyclePhase = iota
	LifecyclePhaseMaintenance
	LifecyclePhaseEndOfLife LifecyclePhase = math.MaxInt - 2
	LifecyclePhasePreGA     LifecyclePhase = math.MaxInt - 1
	LifeCyclePhaseUnknown   LifecyclePhase = math.MaxInt
)

func (l LifecyclePhase) String() string {
	switch l {
	case LifecyclePhasePreGA:
		return "Pre-GA"
	case LifecyclePhaseFullSupport:
		return "Full Support"
	case LifecyclePhaseMaintenance:
		return "Maintenance"
	case LifecyclePhaseEndOfLife:
		return "End of Life"
	default:
		return fmt.Sprintf("EUS-%d", int(l-1))
	}
}

func (l LifecyclePhase) Compare(other LifecyclePhase) int {
	return cmp.Compare(other, l)
}

func LifecycleExtensionPhase(i int) LifecyclePhase {
	if i < 1 || i >= int(LifecyclePhaseEndOfLife-1) {
		panic(fmt.Sprintf("invalid lifecycle extension phase: %d", i))
	}
	return LifecyclePhase(i + 1)
}

type LifecycleDates struct {
	FullSupport Date   `json:"fullSupport"`
	Maintenance Date   `json:"maintenance"`
	Extensions  []Date `json:"extensions"`
	EndOfLife   Date   `json:"eol"`
}

func (l LifecycleDates) ValidateOrder() error {
	expectedOrder := []Date{l.FullSupport, l.Maintenance}
	expectedOrder = append(expectedOrder, l.Extensions...)
	expectedOrder = append(expectedOrder, l.EndOfLife)

	var v time.Time
	for _, d := range expectedOrder {
		if d.t.Before(v) {
			return fmt.Errorf("invalid: dates out of order: expected order is Full Support, Maintenance, Extensions (in order), End of Life")
		}
		v = d.t
	}
	return nil
}

func (l LifecycleDates) Phase(asOf time.Time) LifecyclePhase {
	if asOf.Before(l.FullSupport.t) {
		return LifecyclePhasePreGA
	}
	if asOf.Before(l.Maintenance.t) {
		return LifecyclePhaseFullSupport
	}
	if asOf.After(l.EndOfLife.t) {
		return LifecyclePhaseEndOfLife
	}
	if len(l.Extensions) == 0 || asOf.Before(l.Extensions[0].t) {
		return LifecyclePhaseMaintenance
	}
	for i := 1; i < len(l.Extensions); i++ {
		if asOf.Before(l.Extensions[i].t) {
			return LifecycleExtensionPhase(i)
		}
	}
	return LifecycleExtensionPhase(len(l.Extensions))
}

type Date struct {
	t time.Time
}

func NewDate(year int, month time.Month, day int) Date {
	return Date{time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

func (d *Date) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return err
	}
	d.t = t
	return nil
}

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.t.Format("2006-01-02"))
}
