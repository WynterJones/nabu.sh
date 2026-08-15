// Package scheduler calculates durable schedule times. It does no work itself;
// the daemon claims due rows from SQLite before dispatching a target.
package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const maximumSearchMinutes = 366 * 24 * 60

// Next returns the first occurrence strictly after the supplied time. Nabu
// supports fixed intervals, descriptors, @every durations, and conventional
// five-field cron expressions (minute through weekday).
func Next(schedule domain.Schedule, after time.Time) (time.Time, error) {
	if !schedule.Enabled {
		return time.Time{}, errors.New("scheduler: schedule is disabled")
	}
	if schedule.IntervalSeconds > 0 {
		return after.Add(time.Duration(schedule.IntervalSeconds) * time.Second), nil
	}
	expression := strings.TrimSpace(schedule.Expression)
	if expression == "" {
		return time.Time{}, errors.New("scheduler: interval or expression is required")
	}
	if strings.HasPrefix(expression, "@every ") {
		duration, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(expression, "@every ")))
		if err != nil || duration <= 0 {
			return time.Time{}, fmt.Errorf("scheduler: invalid @every duration %q", expression)
		}
		return after.Add(duration), nil
	}
	switch expression {
	case "@hourly":
		expression = "0 * * * *"
	case "@daily":
		expression = "0 0 * * *"
	case "@weekly":
		expression = "0 0 * * 0"
	}

	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("scheduler: expected five cron fields, got %d", len(fields))
	}
	minute, err := parseField(fields[0], 0, 59, false)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: minute: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23, false)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: hour: %w", err)
	}
	day, err := parseField(fields[2], 1, 31, false)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: day: %w", err)
	}
	month, err := parseField(fields[3], 1, 12, false)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: month: %w", err)
	}
	weekday, err := parseField(fields[4], 0, 7, true)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: weekday: %w", err)
	}

	candidate := after.Truncate(time.Minute).Add(time.Minute)
	for range maximumSearchMinutes {
		if minute.matches(candidate.Minute()) && hour.matches(candidate.Hour()) &&
			day.matches(candidate.Day()) && month.matches(int(candidate.Month())) &&
			weekday.matches(int(candidate.Weekday())) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, errors.New("scheduler: expression has no occurrence within one year")
}

type fieldMatcher struct {
	any     bool
	allowed map[int]struct{}
}

func (f fieldMatcher) matches(value int) bool {
	if f.any {
		return true
	}
	_, ok := f.allowed[value]
	return ok
}

func parseField(value string, minimum, maximum int, normalizeSunday bool) (fieldMatcher, error) {
	if value == "*" {
		return fieldMatcher{any: true}, nil
	}
	matcher := fieldMatcher{allowed: make(map[int]struct{})}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fieldMatcher{}, errors.New("empty field item")
		}
		base := part
		step := 1
		if slash := strings.IndexByte(part, '/'); slash >= 0 {
			base = part[:slash]
			parsedStep, err := strconv.Atoi(part[slash+1:])
			if err != nil || parsedStep <= 0 {
				return fieldMatcher{}, fmt.Errorf("invalid step %q", part)
			}
			step = parsedStep
		}
		start, end := minimum, maximum
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			bounds := strings.Split(base, "-")
			if len(bounds) != 2 {
				return fieldMatcher{}, fmt.Errorf("invalid range %q", base)
			}
			var err error
			start, err = strconv.Atoi(bounds[0])
			if err != nil {
				return fieldMatcher{}, fmt.Errorf("invalid range %q", base)
			}
			end, err = strconv.Atoi(bounds[1])
			if err != nil {
				return fieldMatcher{}, fmt.Errorf("invalid range %q", base)
			}
		case step != 1:
			return fieldMatcher{}, fmt.Errorf("step requires * or a range in %q", part)
		default:
			item, err := strconv.Atoi(base)
			if err != nil {
				return fieldMatcher{}, fmt.Errorf("invalid value %q", base)
			}
			start, end = item, item
		}
		if start < minimum || end > maximum || start > end {
			return fieldMatcher{}, fmt.Errorf("range %q is outside %d-%d", base, minimum, maximum)
		}
		for item := start; item <= end; item += step {
			normalized := item
			if normalizeSunday && normalized == 7 {
				normalized = 0
			}
			matcher.allowed[normalized] = struct{}{}
		}
	}
	return matcher, nil
}
