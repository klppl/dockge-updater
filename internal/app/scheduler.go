package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var weekdays = map[string]time.Weekday{
	"Sunday": time.Sunday, "Monday": time.Monday, "Tuesday": time.Tuesday,
	"Wednesday": time.Wednesday, "Thursday": time.Thursday, "Friday": time.Friday,
	"Saturday": time.Saturday,
}

func validateSettings(settings Settings) error {
	if _, _, err := parseClock(settings.CheckTime); err != nil {
		return fmt.Errorf("invalid check time: %w", err)
	}
	if _, _, err := parseClock(settings.UpdateTime); err != nil {
		return fmt.Errorf("invalid update time: %w", err)
	}
	if settings.AutoUpdatePolicy != "off" && settings.AutoUpdatePolicy != "daily" && settings.AutoUpdatePolicy != "weekly" {
		return fmt.Errorf("auto-update policy must be off, daily, or weekly")
	}
	if _, ok := weekdays[settings.UpdateWeekday]; !ok {
		return fmt.Errorf("invalid update weekday")
	}
	return nil
}

func parseClock(value string) (int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("use HH:MM")
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("use 24-hour HH:MM")
	}
	return hour, minute, nil
}

func nextDaily(now time.Time, clock string) time.Time {
	hour, minute, err := parseClock(clock)
	if err != nil {
		return time.Time{}
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func scheduleDescription(settings Settings) string {
	if settings.AutoUpdatePolicy == "off" {
		return "Daily checks at " + settings.CheckTime + "; automatic updates off"
	}
	if settings.AutoUpdatePolicy == "daily" {
		return "Daily checks at " + settings.CheckTime + "; daily updates at " + settings.UpdateTime
	}
	return "Daily checks at " + settings.CheckTime + "; " + settings.UpdateWeekday + " updates at " + settings.UpdateTime
}

func (m *Manager) RunScheduler(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	now := time.Now()
	m.mu.RLock()
	lastCheck := m.state.LastCheck
	m.mu.RUnlock()
	if lastCheck.IsZero() || now.Sub(lastCheck) >= 25*time.Hour {
		m.RunScheduledCheck()
	} else {
		m.schedulerTick(now)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.schedulerTick(now)
		}
	}
}

func (m *Manager) schedulerTick(now time.Time) {
	m.mu.RLock()
	settings := m.state.Settings
	lastCheck := m.state.LastScheduledCheck
	lastUpdate := m.state.LastScheduledUpdate
	m.mu.RUnlock()

	minuteKey := now.Format("2006-01-02 15:04")
	updateDue := settings.AutoUpdatePolicy == "daily" ||
		(settings.AutoUpdatePolicy == "weekly" && weekdays[settings.UpdateWeekday] == now.Weekday())
	if updateDue && clockMatches(now, settings.UpdateTime) && lastUpdate != minuteKey {
		if m.RunScheduledUpdate() {
			m.mu.Lock()
			m.state.LastScheduledUpdate = minuteKey
			if settings.CheckTime == settings.UpdateTime {
				m.state.LastScheduledCheck = minuteKey
			}
			_ = m.saveLocked()
			m.mu.Unlock()
			return
		}
	}

	if clockMatches(now, settings.CheckTime) && lastCheck != minuteKey {
		if m.RunScheduledCheck() {
			m.mu.Lock()
			m.state.LastScheduledCheck = minuteKey
			_ = m.saveLocked()
			m.mu.Unlock()
		}
	}
}

func clockMatches(now time.Time, clock string) bool {
	hour, minute, err := parseClock(clock)
	return err == nil && now.Hour() == hour && now.Minute() == minute
}
