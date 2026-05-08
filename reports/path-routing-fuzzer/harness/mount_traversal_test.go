package harness

import (
	"net/url"
	"testing"
)

func TestMount_NetURL_PathNormalization(t *testing.T) {
	// Check what net/url.Parse does with traversal paths
	paths := []string{
		"http://example.com/api/v1/../../../admin",
		"http://example.com/api/../../etc",
		"http://example.com/api/v1/../data",
	}
	for _, p := range paths {
		u, err := url.Parse(p)
		if err != nil {
			t.Logf("Parse error %q: %v", p, err)
			continue
		}
		t.Logf("url.Parse(%q): Path=%q", p, u.Path)
	}
	// Also test ParseRequestURI
	for _, p := range paths {
		u, err := url.ParseRequestURI(p)
		if err != nil {
			t.Logf("ParseRequestURI error %q: %v", p, err)
			continue
		}
		t.Logf("url.ParseRequestURI(%q): Path=%q", p, u.Path)
	}
}
