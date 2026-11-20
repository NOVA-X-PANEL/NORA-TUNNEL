package shadow

import "testing"

func TestParsePortMapping(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		local      int
		remote     int
		wantErr    bool
	}{
		{name: "single port", raw: "443", local: 443, remote: 443},
		{name: "equals mapping", raw: "8443=443", local: 8443, remote: 443},
		{name: "colon mapping", raw: "2053:443", local: 2053, remote: 443},
		{name: "invalid local", raw: "0=443", wantErr: true},
		{name: "invalid remote", raw: "8443=70000", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local, remote, err := parsePortMapping(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePortMapping(%q): %v", tt.raw, err)
			}
			if local != tt.local || remote != tt.remote {
				t.Fatalf("got local=%d remote=%d, want local=%d remote=%d", local, remote, tt.local, tt.remote)
			}
		})
	}
}
