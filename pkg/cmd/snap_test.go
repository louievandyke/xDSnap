package cmd

import (
	"testing"
)

func TestBuildTaskOrder(t *testing.T) {
	tests := []struct {
		name        string
		sidecar     string
		taskName    string
		extraLogs   []string
		want        []string
	}{
		{
			name:     "sidecar first then task",
			sidecar:  "connect-proxy-web",
			taskName: "web",
			want:     []string{"connect-proxy-web", "web"},
		},
		{
			name:      "deduplicates sidecar in extraLogs",
			sidecar:   "connect-proxy-web",
			taskName:  "web",
			extraLogs: []string{"connect-proxy-web"},
			want:      []string{"connect-proxy-web", "web"},
		},
		{
			name:      "deduplicates task in extraLogs",
			sidecar:   "connect-proxy-web",
			taskName:  "web",
			extraLogs: []string{"web", "redis"},
			want:      []string{"connect-proxy-web", "web", "redis"},
		},
		{
			name:     "sidecar same as task",
			sidecar:  "connect-proxy-web",
			taskName: "connect-proxy-web",
			want:     []string{"connect-proxy-web"},
		},
		{
			name:      "empty sidecar",
			sidecar:   "",
			taskName:  "web",
			extraLogs: []string{"redis"},
			want:      []string{"web", "redis"},
		},
		{
			name:      "empty task",
			sidecar:   "connect-proxy-web",
			taskName:  "",
			extraLogs: []string{"web"},
			want:      []string{"connect-proxy-web", "web"},
		},
		{
			name:      "all empty strings filtered",
			sidecar:   "",
			taskName:  "",
			extraLogs: []string{"", ""},
			want:      []string{},
		},
		{
			name:      "three unique tasks",
			sidecar:   "connect-proxy-web",
			taskName:  "web",
			extraLogs: []string{"redis", "postgres"},
			want:      []string{"connect-proxy-web", "web", "redis", "postgres"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTaskOrder(tt.sidecar, tt.taskName, tt.extraLogs)

			// Handle nil vs empty slice
			if len(tt.want) == 0 {
				if len(got) != 0 {
					t.Errorf("buildTaskOrder() = %v, want empty", got)
				}
				return
			}

			if len(got) != len(tt.want) {
				t.Fatalf("buildTaskOrder() len = %d, want %d\ngot:  %v\nwant: %v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("buildTaskOrder()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
