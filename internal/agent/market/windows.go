// Package market provides the agent scorer with live K-line statistics
// and high-volatility window detection.
package market

import "time"

// ActiveWindows reports which (if any) named high-volatility windows the
// given UTC time falls inside. The names go directly into the LLM prompt
// so they should be human-readable in Chinese context.
//
// V1 covers three fixed periodic windows. Adding economic-calendar
// integration (CPI, NFP, FOMC by date) is future work; the spec calls
// out introducing a WindowProvider interface when that lands.
func ActiveWindows(now time.Time) []string {
	var out []string
	utc := now.UTC()
	h, m := utc.Hour(), utc.Minute()
	weekday := utc.Weekday()

	// US data release window: UTC 12:30 (DST) / 13:30 (standard), Tue-Fri.
	if weekday >= time.Tuesday && weekday <= time.Friday {
		if (h == 12 && m >= 25 && m <= 35) || (h == 13 && m >= 25 && m <= 35) {
			out = append(out, "us_data_release_window")
		}
	}

	// US market open window: UTC 13:30 ~ 14:30, Mon-Fri.
	if weekday >= time.Monday && weekday <= time.Friday {
		if (h == 13 && m >= 30) || (h == 14 && m <= 30) {
			out = append(out, "us_market_open_window")
		}
	}

	// Weekend gap window: Monday 00:00 ~ 04:00 UTC. Crypto runs 24/7 but
	// liquidity is thin and weekend news can produce gaps in price action
	// when traditional markets reopen.
	if weekday == time.Monday && h < 4 {
		out = append(out, "weekend_gap_window")
	}
	return out
}
