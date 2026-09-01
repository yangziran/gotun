package router

import (
	"context"
	"testing"

	"github.com/yangziran/gotun/internal/config"
)

func TestRouter_ShouldProxy_Global(t *testing.T) {
	cfg := config.RoutingConfig{
		Mode:      "global",
		BypassLAN: true,
	}
	r := NewRouter(context.Background(), cfg)

	tests := []struct {
		host     string
		expected bool
	}{
		{"8.8.8.8", true},
		{"google.com", true},
		{"127.0.0.1", false},
		{"192.168.1.100", false},
		{"10.0.0.5", false},
		{"::1", false}, // IPv6 loopback
	}

	for _, tt := range tests {
		result := r.ShouldProxy(tt.host)
		if result != tt.expected {
			t.Errorf("ShouldProxy(%s) under global mode: expected %v, got %v", tt.host, tt.expected, result)
		}
	}
}

func TestRouter_ShouldProxy_Direct(t *testing.T) {
	cfg := config.RoutingConfig{
		Mode: "direct",
	}
	r := NewRouter(context.Background(), cfg)

	tests := []struct {
		host     string
		expected bool
	}{
		{"8.8.8.8", false},
		{"google.com", false},
		{"192.168.1.100", false},
	}

	for _, tt := range tests {
		result := r.ShouldProxy(tt.host)
		if result != tt.expected {
			t.Errorf("ShouldProxy(%s) under direct mode: expected %v, got %v", tt.host, tt.expected, result)
		}
	}
}
