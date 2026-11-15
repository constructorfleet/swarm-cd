package swarmcd

import (
	"fmt"
	"time"
	// embed the timezone database so CRON_TZ works in minimal images
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

// updateWindow decides when a stack or service may be updated, based on a
// standard 5-field cron expression (descriptors such as @daily and a
// CRON_TZ=<zone> prefix are also accepted).
//
// The window is open when the schedule fired at least once since the previous
// check. A range such as "* 2-4 * * *" therefore keeps the window open for the
// whole range, while a single point in time such as "0 3 * * *" opens it for
// the first update cycle after 03:00.
type updateWindow struct {
	expr     string
	schedule cron.Schedule
}

func parseUpdateWindow(expr string) (*updateWindow, error) {
	if expr == "" {
		return nil, nil
	}
	schedule, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid update window %q: %w", expr, err)
	}
	return &updateWindow{expr: expr, schedule: schedule}, nil
}

// isOpen reports whether the schedule fired in (lastCheck, now]. A nil window
// is always open.
func (window *updateWindow) isOpen(lastCheck time.Time, now time.Time) bool {
	if window == nil {
		return true
	}
	return !window.schedule.Next(lastCheck).After(now)
}

// nextOpen returns the next time the window opens after now.
func (window *updateWindow) nextOpen(now time.Time) time.Time {
	return window.schedule.Next(now)
}
