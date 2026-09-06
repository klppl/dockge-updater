package app

import "time"

const stateVersion = 1

type Settings struct {
	CheckTime        string `json:"checkTime"`
	AutoUpdatePolicy string `json:"autoUpdatePolicy"`
	UpdateTime       string `json:"updateTime"`
	UpdateWeekday    string `json:"updateWeekday"`
}

func DefaultSettings() Settings {
	return Settings{
		CheckTime:        "03:00",
		AutoUpdatePolicy: "off",
		UpdateTime:       "04:00",
		UpdateWeekday:    "Sunday",
	}
}

type ServiceState struct {
	Name            string `json:"name"`
	Image           string `json:"image"`
	ContainerID     string `json:"containerId,omitempty"`
	ContainerState  string `json:"containerState,omitempty"`
	CurrentImageID  string `json:"currentImageId,omitempty"`
	TargetImageID   string `json:"targetImageId,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

type StackState struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Path             string         `json:"path"`
	ComposeFile      string         `json:"composeFile"`
	Status           string         `json:"status"`
	Services         []ServiceState `json:"services"`
	UpdatesAvailable int            `json:"updatesAvailable"`
	LastChecked      time.Time      `json:"lastChecked,omitempty"`
	LastUpdated      time.Time      `json:"lastUpdated,omitempty"`
	Error            string         `json:"error,omitempty"`
}

type Event struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Title   string    `json:"title"`
	Detail  string    `json:"detail,omitempty"`
	StackID string    `json:"stackId,omitempty"`
}

type Job struct {
	Running   bool      `json:"running"`
	Kind      string    `json:"kind,omitempty"`
	StackID   string    `json:"stackId,omitempty"`
	Message   string    `json:"message,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
}

type persistedState struct {
	Version             int                   `json:"version"`
	Settings            Settings              `json:"settings"`
	Stacks              map[string]StackState `json:"stacks"`
	Events              []Event               `json:"events"`
	LastCheck           time.Time             `json:"lastCheck,omitempty"`
	LastScheduledCheck  string                `json:"lastScheduledCheck,omitempty"`
	LastScheduledUpdate string                `json:"lastScheduledUpdate,omitempty"`
}

type Snapshot struct {
	Settings  Settings     `json:"settings"`
	Stacks    []StackState `json:"stacks"`
	Events    []Event      `json:"events"`
	Job       Job          `json:"job"`
	LastCheck time.Time    `json:"lastCheck,omitempty"`
	NextCheck time.Time    `json:"nextCheck"`
	Version   string       `json:"version"`
}
