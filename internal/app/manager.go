package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

const maxEvents = 80

type Manager struct {
	mu        sync.RWMutex
	stacksDir string
	docker    *Docker
	store     *Store
	state     persistedState
	job       Job
	version   string
	logger    *slog.Logger
}

func NewManager(stacksDir string, docker *Docker, store *Store, version string, logger *slog.Logger) (*Manager, error) {
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	m := &Manager{
		stacksDir: stacksDir,
		docker:    docker,
		store:     store,
		state:     state,
		version:   version,
		logger:    logger,
	}
	if err := m.refreshDiscovery(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) Snapshot(now time.Time) Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stacks := make([]StackState, 0, len(m.state.Stacks))
	for _, stack := range m.state.Stacks {
		stacks = append(stacks, stack)
	}
	sort.Slice(stacks, func(i, j int) bool { return stacks[i].Name < stacks[j].Name })
	events := append([]Event(nil), m.state.Events...)
	return Snapshot{
		Settings:  m.state.Settings,
		Stacks:    stacks,
		Events:    events,
		Job:       m.job,
		LastCheck: m.state.LastCheck,
		NextCheck: nextDaily(now, m.state.Settings.CheckTime),
		Version:   m.version,
	}
}

func (m *Manager) Settings() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Settings
}

func (m *Manager) UpdateSettings(settings Settings) error {
	if err := validateSettings(settings); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Settings = settings
	m.addEventLocked(Event{Type: "settings", Title: "Schedule changed", Detail: scheduleDescription(settings)})
	return m.saveLocked()
}

func (m *Manager) CheckAllAsync() error {
	if !m.startJob("check", "", "Preparing image check") {
		return fmt.Errorf("another job is already running")
	}
	go func() {
		defer m.finishJob()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		_ = m.checkAll(ctx)
	}()
	return nil
}

func (m *Manager) UpdateStackAsync(id string) error {
	m.mu.RLock()
	_, exists := m.state.Stacks[id]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("stack not found")
	}
	if !m.startJob("update", id, "Preparing stack update") {
		return fmt.Errorf("another job is already running")
	}
	go func() {
		defer m.finishJob()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		m.updateOne(ctx, id, false)
	}()
	return nil
}

func (m *Manager) RunScheduledCheck() bool {
	if !m.startJob("scheduled_check", "", "Starting scheduled image check") {
		return false
	}
	go func() {
		defer m.finishJob()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		_ = m.checkAll(ctx)
	}()
	return true
}

func (m *Manager) RunScheduledUpdate() bool {
	if !m.startJob("scheduled_update", "", "Checking before scheduled update") {
		return false
	}
	go func() {
		defer m.finishJob()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()
		if !m.checkAll(ctx) {
			return
		}

		m.mu.RLock()
		ids := make([]string, 0)
		for id, stack := range m.state.Stacks {
			if stack.Status == "update_available" && stack.UpdatesAvailable > 0 {
				ids = append(ids, id)
			}
		}
		m.mu.RUnlock()
		sort.Strings(ids)
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			m.updateOne(ctx, id, true)
		}
	}()
	return true
}

func (m *Manager) checkAll(ctx context.Context) bool {
	if err := m.refreshDiscovery(); err != nil {
		m.recordGlobalError("Stack discovery failed", err)
		return false
	}

	m.mu.RLock()
	stacks := make([]StackState, 0, len(m.state.Stacks))
	for _, stack := range m.state.Stacks {
		stacks = append(stacks, stack)
	}
	m.mu.RUnlock()
	sort.Slice(stacks, func(i, j int) bool { return stacks[i].Name < stacks[j].Name })

	m.logger.Info("starting image check for all stacks", "total_stacks", len(stacks))

	for index, stack := range stacks {
		if ctx.Err() != nil {
			m.recordGlobalError("Image check stopped", ctx.Err())
			return false
		}

		progressPrefix := fmt.Sprintf("[%d/%d] %s", index+1, len(stacks), stack.Name)
		m.setJobMessage(progressPrefix)
		m.logger.Info("checking stack", "stack", stack.Name, "index", index+1, "total", len(stacks))

		// Mark this stack as checking in memory so UI reflects it immediately
		m.mu.Lock()
		if s, ok := m.state.Stacks[stack.ID]; ok {
			s.Status = "checking"
			m.state.Stacks[stack.ID] = s
		}
		m.mu.Unlock()

		stackStartTime := time.Now()
		previousUpdates := stack.UpdatesAvailable

		checked, err := m.docker.CheckStack(ctx, stack, func(detail string) {
			liveMsg := fmt.Sprintf("[%d/%d] %s", index+1, len(stacks), detail)
			m.setJobMessage(liveMsg)
			m.logger.Debug("image check progress", "message", liveMsg)
		})

		checked.LastChecked = time.Now()
		if err != nil {
			checked.Status = "error"
			checked.Error = err.Error()
			m.logger.Warn("stack check completed with error", "stack", stack.Name, "error", err, "duration", time.Since(stackStartTime))
		} else {
			m.logger.Info("stack check completed", "stack", stack.Name, "status", checked.Status, "updates", checked.UpdatesAvailable, "duration", time.Since(stackStartTime))
		}

		m.mu.Lock()
		m.state.Stacks[stack.ID] = checked
		if err != nil {
			m.addEventLocked(Event{Type: "error", Title: stack.Name + " check failed", Detail: err.Error(), StackID: stack.ID})
		} else if checked.UpdatesAvailable > 0 && previousUpdates == 0 {
			m.addEventLocked(Event{Type: "update", Title: stack.Name + " has updates", Detail: pluralServices(checked.UpdatesAvailable), StackID: stack.ID})
		}
		_ = m.saveLocked()
		m.mu.Unlock()
	}

	m.mu.Lock()
	m.state.LastCheck = time.Now()
	available := 0
	for _, stack := range m.state.Stacks {
		available += stack.UpdatesAvailable
	}
	detail := "All deployed images match their latest pulled versions"
	if available > 0 {
		detail = fmt.Sprintf("%d service image update(s) available", available)
	}
	m.addEventLocked(Event{Type: "check", Title: "Image check complete", Detail: detail})
	_ = m.saveLocked()
	m.mu.Unlock()
	m.logger.Info("completed image check for all stacks", "updates_available", available)
	return true
}

func (m *Manager) updateOne(ctx context.Context, id string, automatic bool) {
	m.mu.RLock()
	stack, exists := m.state.Stacks[id]
	m.mu.RUnlock()
	if !exists {
		return
	}
	m.setJobMessage("Updating " + stack.Name)
	if err := m.docker.UpdateStack(ctx, stack); err != nil {
		m.mu.Lock()
		stack.Status = "error"
		stack.Error = err.Error()
		m.state.Stacks[id] = stack
		m.addEventLocked(Event{Type: "error", Title: stack.Name + " update failed", Detail: err.Error(), StackID: id})
		_ = m.saveLocked()
		m.mu.Unlock()
		return
	}

	checked, err := m.docker.CheckStack(ctx, stack)
	checked.LastUpdated = time.Now()
	checked.LastChecked = checked.LastUpdated
	if err != nil {
		checked.Status = "error"
		checked.Error = "updated, but verification failed: " + err.Error()
	}
	mode := "Updated manually"
	if automatic {
		mode = "Updated by schedule"
	}
	event := Event{Type: "success", Title: stack.Name + " updated", Detail: mode, StackID: id}
	if err != nil {
		event.Type = "error"
		event.Title = stack.Name + " updated; verification failed"
		event.Detail = err.Error()
	}
	m.mu.Lock()
	m.state.Stacks[id] = checked
	m.addEventLocked(event)
	_ = m.saveLocked()
	m.mu.Unlock()
}

func (m *Manager) refreshDiscovery() error {
	discovered, err := DiscoverStacks(m.stacksDir)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fresh := make(map[string]StackState, len(discovered))
	for _, stack := range discovered {
		if existing, ok := m.state.Stacks[stack.ID]; ok {
			existing.Name = stack.Name
			existing.Path = stack.Path
			existing.ComposeFile = stack.ComposeFile
			fresh[stack.ID] = existing
		} else {
			fresh[stack.ID] = stack
		}
	}
	m.state.Stacks = fresh
	return m.saveLocked()
}

func (m *Manager) startJob(kind, stackID, message string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job.Running {
		return false
	}
	m.job = Job{Running: true, Kind: kind, StackID: stackID, Message: message, StartedAt: time.Now()}
	return true
}

func (m *Manager) finishJob() {
	m.mu.Lock()
	m.job = Job{}
	m.mu.Unlock()
}

func (m *Manager) setJobMessage(message string) {
	m.mu.Lock()
	m.job.Message = message
	m.mu.Unlock()
}

func (m *Manager) recordGlobalError(title string, err error) {
	m.logger.Error(title, "error", err)
	m.mu.Lock()
	m.addEventLocked(Event{Type: "error", Title: title, Detail: err.Error()})
	_ = m.saveLocked()
	m.mu.Unlock()
}

func (m *Manager) addEventLocked(event Event) {
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("%d", event.Time.UnixNano())
	}
	m.state.Events = append([]Event{event}, m.state.Events...)
	if len(m.state.Events) > maxEvents {
		m.state.Events = m.state.Events[:maxEvents]
	}
}

func (m *Manager) saveLocked() error {
	if err := m.store.Save(m.state); err != nil {
		m.logger.Error("save state", "error", err)
		return err
	}
	return nil
}

func pluralServices(count int) string {
	if count == 1 {
		return "1 service can be updated"
	}
	return fmt.Sprintf("%d services can be updated", count)
}
