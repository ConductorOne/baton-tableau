package client

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsFilterSafe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "plain name", value: "Finance Reports", want: true},
		{name: "unicode", value: "Ventas Año Fiscal ✓", want: true},
		{name: "comma", value: "Core Exp, Nav, and Search", want: false},
		{name: "ampersand", value: "Sales & Marketing", want: false},
		{name: "colon", value: "Q1: Planning", want: true},
		{name: "empty", value: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, IsFilterSafe(tt.value))
		})
	}
}
