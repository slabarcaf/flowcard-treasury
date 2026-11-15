package domain

import (
	"errors"
	"time"
)

// DelinquencyStage derives servicing stage from UTC calendar-day delinquency.
type DelinquencyStage string

const (
	StageCurrent    DelinquencyStage = "CURRENT"
	StageWatch      DelinquencyStage = "WATCH"
	StageDelinquent DelinquencyStage = "DELINQUENT"
	StageSerious    DelinquencyStage = "SERIOUS"
	StageDefault    DelinquencyStage = "DEFAULT"
)

// DaysPastDue returns zero for no settled debt or before/during the due date.
func DaysPastDue(asOf, dueOn time.Time, settledDebt Money) (int, error) {
	if settledDebt < 0 {
		return 0, errors.New("debt_negative")
	}
	if settledDebt == 0 {
		return 0, nil
	}
	a, d := utcDate(asOf), utcDate(dueOn)
	if a.Before(d) {
		return 0, nil
	}
	return int(a.Sub(d) / (24 * time.Hour)), nil
}

// StageForDays maps DPD to the pinned 0/1/8/31/61 bands.
func StageForDays(days int) (DelinquencyStage, error) {
	if days < 0 {
		return "", errors.New("days_past_due_invalid")
	}
	switch {
	case days == 0:
		return StageCurrent, nil
	case days <= 7:
		return StageWatch, nil
	case days <= 30:
		return StageDelinquent, nil
	case days <= 60:
		return StageSerious, nil
	default:
		return StageDefault, nil
	}
}

// FirstDefaultOn returns the effective first default date, preserving history.
func FirstDefaultOn(dueOn time.Time, existing *time.Time) *time.Time {
	if existing != nil {
		d := utcDate(*existing)
		return &d
	}
	d := utcDate(dueOn).AddDate(0, 0, 61)
	return &d
}

func utcDate(value time.Time) time.Time {
	v := value.UTC()
	return time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, time.UTC)
}
