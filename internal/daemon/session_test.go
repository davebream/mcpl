package daemon

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseClientCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		params   string
		wantCaps SessionCapabilities
	}{
		{
			name:     "empty capabilities",
			params:   `{"capabilities":{}}`,
			wantCaps: SessionCapabilities{},
		},
		{
			name:     "roots only",
			params:   `{"capabilities":{"roots":{"listChanged":true}}}`,
			wantCaps: SessionCapabilities{Roots: true},
		},
		{
			name:     "sampling only",
			params:   `{"capabilities":{"sampling":{}}}`,
			wantCaps: SessionCapabilities{Sampling: true},
		},
		{
			name:     "both roots and sampling",
			params:   `{"capabilities":{"roots":{"listChanged":true},"sampling":{}}}`,
			wantCaps: SessionCapabilities{Roots: true, Sampling: true},
		},
		{
			name:     "nil params",
			params:   `{}`,
			wantCaps: SessionCapabilities{},
		},
		{
			name:     "invalid JSON",
			params:   `not-json`,
			wantCaps: SessionCapabilities{},
		},
		{
			name:     "full initialize params with extra fields",
			params:   `{"protocolVersion":"2025-03-26","capabilities":{"roots":{"listChanged":true},"sampling":{}},"clientInfo":{"name":"claude-code","version":"1.0"}}`,
			wantCaps: SessionCapabilities{Roots: true, Sampling: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := ParseClientCapabilities(json.RawMessage(tt.params))
			assert.Equal(t, tt.wantCaps, caps)
		})
	}
}
