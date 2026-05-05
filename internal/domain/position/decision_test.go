package position

import (
	"testing"

	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/stretchr/testify/assert"
)

func TestDecide_AllEightCases(t *testing.T) {
	cases := []struct {
		name    string
		current *VirtualPosition // nil = 空仓
		signal  sigpkg.Kind
		want    Action
	}{
		{"empty + Long", nil, sigpkg.KindLong, ActionOpenLong},
		{"empty + Short", nil, sigpkg.KindShort, ActionOpenShort},
		{"empty + ExitLong", nil, sigpkg.KindExitLong, ActionNoOp},
		{"empty + ExitShort", nil, sigpkg.KindExitShort, ActionNoOp},
		{"long + ExitLong", &VirtualPosition{Side: SideLong, Status: StatusOpen}, sigpkg.KindExitLong, ActionClose},
		{"long + Long", &VirtualPosition{Side: SideLong, Status: StatusOpen}, sigpkg.KindLong, ActionNoOp},
		{"long + Short", &VirtualPosition{Side: SideLong, Status: StatusOpen}, sigpkg.KindShort, ActionCloseAndOpenShort},
		{"long + ExitShort", &VirtualPosition{Side: SideLong, Status: StatusOpen}, sigpkg.KindExitShort, ActionNoOp},
		{"short + ExitShort", &VirtualPosition{Side: SideShort, Status: StatusOpen}, sigpkg.KindExitShort, ActionClose},
		{"short + Short", &VirtualPosition{Side: SideShort, Status: StatusOpen}, sigpkg.KindShort, ActionNoOp},
		{"short + Long", &VirtualPosition{Side: SideShort, Status: StatusOpen}, sigpkg.KindLong, ActionCloseAndOpenLong},
		{"short + ExitLong", &VirtualPosition{Side: SideShort, Status: StatusOpen}, sigpkg.KindExitLong, ActionNoOp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.current, tc.signal)
			assert.Equal(t, tc.want, got, "case=%s", tc.name)
		})
	}
}
