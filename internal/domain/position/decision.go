package position

import sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"

type Action string

const (
	ActionNoOp              Action = "noop"
	ActionOpenLong          Action = "open_long"
	ActionOpenShort         Action = "open_short"
	ActionClose             Action = "close"
	ActionCloseAndOpenLong  Action = "close_and_open_long"
	ActionCloseAndOpenShort Action = "close_and_open_short"
)

// Decide returns the action to take given current position (nil = flat) and incoming signal.
// Implements the 8x decision table in spec §7 Step 5.
func Decide(current *VirtualPosition, sig sigpkg.Kind) Action {
	if current == nil || !current.Status.IsActive() {
		switch sig {
		case sigpkg.KindLong:
			return ActionOpenLong
		case sigpkg.KindShort:
			return ActionOpenShort
		}
		return ActionNoOp
	}
	switch current.Side {
	case SideLong:
		switch sig {
		case sigpkg.KindExitLong:
			return ActionClose
		case sigpkg.KindShort:
			return ActionCloseAndOpenShort
		}
	case SideShort:
		switch sig {
		case sigpkg.KindExitShort:
			return ActionClose
		case sigpkg.KindLong:
			return ActionCloseAndOpenLong
		}
	}
	return ActionNoOp
}
