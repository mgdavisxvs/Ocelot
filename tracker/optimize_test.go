package tracker

import (
	"testing"
)

func TestAdaptiveInterval(t *testing.T) {
	tests := []struct {
		name     string
		seeders  int
		leechers int
		baseInt  int
		expected int32
	}{
		{
			name:     "Very small swarm",
			seeders:  2,
			leechers: 3,
			baseInt:  1800,
			expected: 600, // < 10 peers
		},
		{
			name:     "Small swarm",
			seeders:  5,
			leechers: 5,
			baseInt:  1800,
			expected: 1200, // 10 peers, not < 10, so second tier
		},
		{
			name:     "Medium swarm",
			seeders:  30,
			leechers: 40,
			baseInt:  1800,
			expected: 1200, // 70 peers, < 100
		},
		{
			name:     "Large swarm",
			seeders:  60,
			leechers: 40,
			baseInt:  1800,
			expected: 2400, // 100 peers, not < 100, so third tier
		},
		{
			name:     "Huge swarm",
			seeders:  200,
			leechers: 100,
			baseInt:  1800,
			expected: 2400, // 300 peers, >= 100
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval := AdaptiveInterval(tt.seeders, tt.leechers, tt.baseInt)

			if interval != tt.expected {
				t.Errorf("Expected interval %d, got %d",
					tt.expected, interval)
			}
		})
	}
}
