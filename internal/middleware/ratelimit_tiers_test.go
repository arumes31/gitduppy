package middleware

import "testing"

func TestLimitForCategories(t *testing.T) {
	rl := &RateLimiter{rps: 60, burst: 60}

	cases := []struct {
		path       string
		wantPrefix string
	}{
		{"/api/v1/auth/login", "auth:"},
		{"/api/v1/oauth/github/login", "auth:"},
		{"/api/v1/search", "expensive:"},
		{"/api/v1/dashboard/stats", "dashboard:"},
		{"/api/v1/repositories", "api:"},
		{"/api/v1/health", "api:"},
	}
	for _, c := range cases {
		prefix, rps, burst := rl.limitFor(c.path)
		if prefix != c.wantPrefix {
			t.Errorf("limitFor(%q) prefix = %q, want %q", c.path, prefix, c.wantPrefix)
		}
		// Auth and expensive tiers must be strictly tighter than the default API tier.
		switch c.wantPrefix {
		case "auth:":
			if rps >= rl.rps {
				t.Errorf("auth tier rps %v should be below default %v", rps, rl.rps)
			}
		case "expensive:":
			if rps >= rl.rps {
				t.Errorf("expensive tier rps %v should be below default %v", rps, rl.rps)
			}
		case "dashboard:":
			// The dashboard is isolated on its own key but must stay generous:
			// a single view fans out to several of these endpoints, so it uses
			// the full API budget rather than a throttled fraction of it.
			if rps != rl.rps || burst != rl.burst {
				t.Errorf("dashboard tier should use full API budget, got rps=%v burst=%v", rps, burst)
			}
		case "api:":
			if rps != rl.rps || burst != rl.burst {
				t.Errorf("default tier should use base limits, got rps=%v burst=%v", rps, burst)
			}
		}
		if burst < 1 {
			t.Errorf("burst for %q must be at least 1, got %d", c.path, burst)
		}
	}
}
