package graph_test

import (
	"fmt"
	"testing"

	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/graph"
	"github.com/stretchr/testify/assert"
)

func TestLifecyclePhase_Compare(t *testing.T) {
	// Create extension phases for testing
	extension1 := graph.LifecycleExtensionPhase(1)
	extension2 := graph.LifecycleExtensionPhase(2)
	extension3 := graph.LifecycleExtensionPhase(3)

	tests := []struct {
		name     string
		a        graph.LifecyclePhase
		b        graph.LifecyclePhase
		expected int
	}{
		// Same phase comparisons
		{
			name:     "FullSupport == FullSupport",
			a:        graph.LifecyclePhaseFullSupport,
			b:        graph.LifecyclePhaseFullSupport,
			expected: 0,
		},
		{
			name:     "Maintenance == Maintenance",
			a:        graph.LifecyclePhaseMaintenance,
			b:        graph.LifecyclePhaseMaintenance,
			expected: 0,
		},
		{
			name:     "EndOfLife == EndOfLife",
			a:        graph.LifecyclePhaseEndOfLife,
			b:        graph.LifecyclePhaseEndOfLife,
			expected: 0,
		},
		{
			name:     "PreGA == PreGA",
			a:        graph.LifecyclePhasePreGA,
			b:        graph.LifecyclePhasePreGA,
			expected: 0,
		},
		{
			name:     "Extension1 == Extension1",
			a:        extension1,
			b:        extension1,
			expected: 0,
		},

		// FullSupport comparisons (FullSupport is the "best" phase, so it should be > others)
		{
			name:     "FullSupport > Maintenance",
			a:        graph.LifecyclePhaseFullSupport,
			b:        graph.LifecyclePhaseMaintenance,
			expected: 1,
		},
		{
			name:     "FullSupport > Extension1",
			a:        graph.LifecyclePhaseFullSupport,
			b:        extension1,
			expected: 1,
		},
		{
			name:     "FullSupport > Extension2",
			a:        graph.LifecyclePhaseFullSupport,
			b:        extension2,
			expected: 1,
		},
		{
			name:     "FullSupport > EndOfLife",
			a:        graph.LifecyclePhaseFullSupport,
			b:        graph.LifecyclePhaseEndOfLife,
			expected: 1,
		},
		{
			name:     "FullSupport > PreGA",
			a:        graph.LifecyclePhaseFullSupport,
			b:        graph.LifecyclePhasePreGA,
			expected: 1,
		},

		// Maintenance comparisons
		{
			name:     "Maintenance < FullSupport",
			a:        graph.LifecyclePhaseMaintenance,
			b:        graph.LifecyclePhaseFullSupport,
			expected: -1,
		},
		{
			name:     "Maintenance > Extension1",
			a:        graph.LifecyclePhaseMaintenance,
			b:        extension1,
			expected: 1,
		},
		{
			name:     "Maintenance > Extension2",
			a:        graph.LifecyclePhaseMaintenance,
			b:        extension2,
			expected: 1,
		},
		{
			name:     "Maintenance > EndOfLife",
			a:        graph.LifecyclePhaseMaintenance,
			b:        graph.LifecyclePhaseEndOfLife,
			expected: 1,
		},
		{
			name:     "Maintenance > PreGA",
			a:        graph.LifecyclePhaseMaintenance,
			b:        graph.LifecyclePhasePreGA,
			expected: 1,
		},

		// Extension1 comparisons
		{
			name:     "Extension1 < FullSupport",
			a:        extension1,
			b:        graph.LifecyclePhaseFullSupport,
			expected: -1,
		},
		{
			name:     "Extension1 < Maintenance",
			a:        extension1,
			b:        graph.LifecyclePhaseMaintenance,
			expected: -1,
		},
		{
			name:     "Extension1 > Extension2",
			a:        extension1,
			b:        extension2,
			expected: 1,
		},
		{
			name:     "Extension1 > Extension3",
			a:        extension1,
			b:        extension3,
			expected: 1,
		},
		{
			name:     "Extension1 > EndOfLife",
			a:        extension1,
			b:        graph.LifecyclePhaseEndOfLife,
			expected: 1,
		},
		{
			name:     "Extension1 > PreGA",
			a:        extension1,
			b:        graph.LifecyclePhasePreGA,
			expected: 1,
		},

		// Extension2 comparisons
		{
			name:     "Extension2 < FullSupport",
			a:        extension2,
			b:        graph.LifecyclePhaseFullSupport,
			expected: -1,
		},
		{
			name:     "Extension2 < Maintenance",
			a:        extension2,
			b:        graph.LifecyclePhaseMaintenance,
			expected: -1,
		},
		{
			name:     "Extension2 < Extension1",
			a:        extension2,
			b:        extension1,
			expected: -1,
		},
		{
			name:     "Extension2 > Extension3",
			a:        extension2,
			b:        extension3,
			expected: 1,
		},
		{
			name:     "Extension2 > EndOfLife",
			a:        extension2,
			b:        graph.LifecyclePhaseEndOfLife,
			expected: 1,
		},
		{
			name:     "Extension2 > PreGA",
			a:        extension2,
			b:        graph.LifecyclePhasePreGA,
			expected: 1,
		},

		// Extension3 comparisons
		{
			name:     "Extension3 < FullSupport",
			a:        extension3,
			b:        graph.LifecyclePhaseFullSupport,
			expected: -1,
		},
		{
			name:     "Extension3 < Maintenance",
			a:        extension3,
			b:        graph.LifecyclePhaseMaintenance,
			expected: -1,
		},
		{
			name:     "Extension3 < Extension1",
			a:        extension3,
			b:        extension1,
			expected: -1,
		},
		{
			name:     "Extension3 < Extension2",
			a:        extension3,
			b:        extension2,
			expected: -1,
		},
		{
			name:     "Extension3 > EndOfLife",
			a:        extension3,
			b:        graph.LifecyclePhaseEndOfLife,
			expected: 1,
		},
		{
			name:     "Extension3 > PreGA",
			a:        extension3,
			b:        graph.LifecyclePhasePreGA,
			expected: 1,
		},

		// EndOfLife comparisons (EndOfLife is worse than most phases)
		{
			name:     "EndOfLife < FullSupport",
			a:        graph.LifecyclePhaseEndOfLife,
			b:        graph.LifecyclePhaseFullSupport,
			expected: -1,
		},
		{
			name:     "EndOfLife < Maintenance",
			a:        graph.LifecyclePhaseEndOfLife,
			b:        graph.LifecyclePhaseMaintenance,
			expected: -1,
		},
		{
			name:     "EndOfLife < Extension1",
			a:        graph.LifecyclePhaseEndOfLife,
			b:        extension1,
			expected: -1,
		},
		{
			name:     "EndOfLife < Extension2",
			a:        graph.LifecyclePhaseEndOfLife,
			b:        extension2,
			expected: -1,
		},
		{
			name:     "EndOfLife > PreGA",
			a:        graph.LifecyclePhaseEndOfLife,
			b:        graph.LifecyclePhasePreGA,
			expected: 1,
		},

		// PreGA comparisons (PreGA is the "worst" phase)
		{
			name:     "PreGA < FullSupport",
			a:        graph.LifecyclePhasePreGA,
			b:        graph.LifecyclePhaseFullSupport,
			expected: -1,
		},
		{
			name:     "PreGA < Maintenance",
			a:        graph.LifecyclePhasePreGA,
			b:        graph.LifecyclePhaseMaintenance,
			expected: -1,
		},
		{
			name:     "PreGA < Extension1",
			a:        graph.LifecyclePhasePreGA,
			b:        extension1,
			expected: -1,
		},
		{
			name:     "PreGA < Extension2",
			a:        graph.LifecyclePhasePreGA,
			b:        extension2,
			expected: -1,
		},
		{
			name:     "PreGA < EndOfLife",
			a:        graph.LifecyclePhasePreGA,
			b:        graph.LifecyclePhaseEndOfLife,
			expected: -1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := test.a.Compare(test.b)
			assert.Equal(t, test.expected, actual, "Compare(%v, %v)", test.a, test.b)
		})
	}
}

func TestLifecycleExtensionPhase(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected graph.LifecyclePhase
	}{
		{
			name:     "Extension phase 1",
			input:    1,
			expected: graph.LifecyclePhase(2), // i + 1
		},
		{
			name:     "Extension phase 2",
			input:    2,
			expected: graph.LifecyclePhase(3), // i + 1
		},
		{
			name:     "Extension phase 10",
			input:    10,
			expected: graph.LifecyclePhase(11), // i + 1
		},
		{
			name:     "Extension phase 100",
			input:    100,
			expected: graph.LifecyclePhase(101), // i + 1
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := graph.LifecycleExtensionPhase(test.input)
			assert.Equal(t, test.expected, actual)

			// Verify the string representation follows the expected format
			expectedStr := fmt.Sprintf("EUS-%d", test.input)
			assert.Equal(t, expectedStr, actual.String())
		})
	}
}

func TestLifecycleExtensionPhase_Panics(t *testing.T) {
	panicTests := []struct {
		name  string
		input int
	}{
		{
			name:  "Input 0 should panic",
			input: 0,
		},
		{
			name:  "Negative input should panic",
			input: -1,
		},
		{
			name:  "Input at EndOfLife-1 boundary should panic",
			input: int(graph.LifecyclePhaseEndOfLife - 1),
		},
		{
			name:  "Input above EndOfLife-1 boundary should panic",
			input: int(graph.LifecyclePhaseEndOfLife),
		},
	}

	for _, test := range panicTests {
		t.Run(test.name, func(t *testing.T) {
			assert.Panics(t, func() {
				graph.LifecycleExtensionPhase(test.input)
			}, "Expected panic for input %d", test.input)
		})
	}
}

func TestLifecyclePhase_String(t *testing.T) {
	tests := []struct {
		name     string
		phase    graph.LifecyclePhase
		expected string
	}{
		{
			name:     "PreGA string representation",
			phase:    graph.LifecyclePhasePreGA,
			expected: "Pre-GA",
		},
		{
			name:     "FullSupport string representation",
			phase:    graph.LifecyclePhaseFullSupport,
			expected: "Full Support",
		},
		{
			name:     "Maintenance string representation",
			phase:    graph.LifecyclePhaseMaintenance,
			expected: "Maintenance",
		},
		{
			name:     "EndOfLife string representation",
			phase:    graph.LifecyclePhaseEndOfLife,
			expected: "End of Life",
		},
		{
			name:     "Extension phase 1 string representation",
			phase:    graph.LifecycleExtensionPhase(1),
			expected: "EUS-1",
		},
		{
			name:     "Extension phase 2 string representation",
			phase:    graph.LifecycleExtensionPhase(2),
			expected: "EUS-2",
		},
		{
			name:     "Extension phase 5 string representation",
			phase:    graph.LifecycleExtensionPhase(5),
			expected: "EUS-5",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := test.phase.String()
			assert.Equal(t, test.expected, actual)
		})
	}
}
