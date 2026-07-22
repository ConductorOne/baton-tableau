package connector

import (
	"testing"

	"github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/stretchr/testify/require"
)

// TestLicenseResource_Trait asserts the LicenseProfileTrait is attached with the
// right name, entitlement ID, and seat counts. The baton CLI does not decode the
// trait body, so this direct assertion is the reliable verification path.
func TestLicenseResource_Trait(t *testing.T) {
	t.Parallel()

	creatorCap := "5"
	zeroCap := "0"

	tests := []struct {
		name          string
		license       string
		purchased     *string
		consumed      int64
		wantName      string
		wantEntID     string
		wantPurchased int64
		wantConsumed  int64
	}{
		{
			name:          "capped tier reports seats",
			license:       creator,
			purchased:     &creatorCap,
			consumed:      2,
			wantName:      "Creator",
			wantEntID:     "license:creator:member",
			wantPurchased: 5,
			wantConsumed:  2,
		},
		{
			name:      "floor tier without capacity omits seats",
			license:   unlicensed,
			purchased: nil,
			consumed:  0,
			wantName:  "Unlicensed",
			wantEntID: "license:unlicensed:member",
			// no capacity -> WithLicenseSeats not called -> both zero
			wantPurchased: 0,
			wantConsumed:  0,
		},
		{
			name:      "zero capacity omits seats",
			license:   viewer,
			purchased: &zeroCap,
			consumed:  3,
			wantName:  "Viewer",
			wantEntID: "license:viewer:member",
			// "0" capacity -> WithLicenseSeats not called (no "0 of N")
			wantPurchased: 0,
			wantConsumed:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := licenseResource(tt.license, tt.purchased, tt.consumed)
			require.NoError(t, err)

			profile, err := resource.GetLicenseProfileTrait(res)
			require.NoError(t, err, "license resource must carry a LicenseProfileTrait")

			require.Equal(t, tt.wantName, profile.GetLicenseName())
			require.Equal(t, []string{tt.wantEntID}, profile.GetEntitlementIds())
			require.Equal(t, tt.wantPurchased, profile.GetPurchasedSeats())
			require.Equal(t, tt.wantConsumed, profile.GetConsumedSeats())
		})
	}
}

// TestCapacitySeats locks in the parse + positive-guard contract: only a
// present, numeric, positive capacity string yields seats.
func TestCapacitySeats(t *testing.T) {
	t.Parallel()

	str := func(s string) *string { return &s }
	tests := []struct {
		name   string
		in     *string
		wantN  int64
		wantOK bool
	}{
		{name: "nil", in: nil, wantN: 0, wantOK: false},
		{name: "positive", in: str("25"), wantN: 25, wantOK: true},
		{name: "zero", in: str("0"), wantN: 0, wantOK: false},
		{name: "whitespace-padded", in: str("  7 "), wantN: 7, wantOK: true},
		{name: "negative", in: str("-3"), wantN: 0, wantOK: false},
		{name: "non-numeric", in: str("unlimited"), wantN: 0, wantOK: false},
		{name: "empty", in: str(""), wantN: 0, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			n, ok := capacitySeats(tt.in)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantN, n)
		})
	}
}
