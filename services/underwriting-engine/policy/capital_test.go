package policy

import "testing"

func TestDescribeCapital(t *testing.T) {
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name                                           string
		total, funded, reserved, bps, floor, available int64
		known                                          bool
	}{
		{"abundant", 100000000, 0, 0, 2000, 20000000, 80000000, true},
		{"ceiling", 101, 0, 0, 1, 1, 100, true},
		{"floor", 10000, 8000, 0, 2000, 2000, 0, true},
		{"overcommitted", 10000, 9000, 9000, 2000, 2000, 0, true},
		{"invalid", -1, 0, 0, 2000, 0, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := FacilitySnapshot{Currency: "USD", LedgerComplete: true, FacilityTotalCents: tt.total, FundedPrincipalCents: tt.funded, ReservedPrincipalCents: tt.reserved, ReserveFloorBPS: tt.bps}
			got := DescribeCapital(in, c)
			if got.Known != tt.known {
				t.Fatalf("DescribeCapital(%+v).Known = %v, want %v", in, got.Known, tt.known)
			}
			if !tt.known {
				if got.Regime != nil || got.AvailableCapitalCents != nil {
					t.Fatal("unknown description contains amounts")
				}
				return
			}
			if *got.ReserveFloorCents != tt.floor || *got.AvailableCapitalCents != tt.available {
				t.Fatalf("DescribeCapital = %+v, want floor %d available %d", got, tt.floor, tt.available)
			}
			want, ok := facilityAvailable(in)
			if !ok || *want != *got.AvailableCapitalCents {
				t.Fatal("description differs from evaluator availability")
			}
			regime, _ := deriveRegime(*want, in.FacilityTotalCents, c)
			if *got.Regime != regime {
				t.Fatal("description differs from evaluator regime")
			}
		})
	}
	if DescribeCapital(FacilitySnapshot{}, Config{}).Known {
		t.Fatal("empty config is known")
	}
}
