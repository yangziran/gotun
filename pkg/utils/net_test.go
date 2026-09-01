package utils

import (
	"reflect"
	"testing"
)

func TestGetDualStackAddrs(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected []string
	}{
		{
			name:     "Localhost dual stack",
			addr:     "127.0.0.1:1080",
			expected: []string{"127.0.0.1:1080", "[::1]:1080"},
		},
		{
			name:     "Any address dual stack",
			addr:     "0.0.0.0:53",
			expected: []string{"0.0.0.0:53", "[::]:53"},
		},
		{
			name:     "Specific IPv4",
			addr:     "192.168.1.100:8000",
			expected: []string{"192.168.1.100:8000"},
		},
		{
			name:     "Specific IPv6",
			addr:     "[2001:db8::1]:3306",
			expected: []string{"[2001:db8::1]:3306"},
		},
		{
			name:     "Invalid port",
			addr:     "127.0.0.1",
			expected: []string{"127.0.0.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetDualStackAddrs(tt.addr)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("GetDualStackAddrs(%q) = %v; want %v", tt.addr, result, tt.expected)
			}
		})
	}
}
