package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var composeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

type CommandRunner interface {
	Run(ctx context.Context, directory, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return stdout.Bytes(), nil
}

type Docker struct {
	runner CommandRunner
}

func NewDocker(runner CommandRunner) *Docker {
	return &Docker{runner: runner}
}

func (d *Docker) Validate(ctx context.Context) error {
	if _, err := d.runner.Run(ctx, ".", "docker", "compose", "version", "--short"); err != nil {
		return fmt.Errorf("docker compose is unavailable: %w", err)
	}
	return nil
}

func DiscoverStacks(root string) ([]StackState, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read stacks directory %q: %w", root, err)
	}

	stacks := make([]StackState, 0)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		composeFile := ""
		for _, name := range composeFileNames {
			candidate := filepath.Join(directory, name)
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				composeFile = candidate
				break
			}
		}
		if composeFile == "" {
			continue
		}
		stacks = append(stacks, StackState{
			ID:          stackID(directory),
			Name:        entry.Name(),
			Path:        directory,
			ComposeFile: composeFile,
			Status:      "unknown",
			Services:    make([]ServiceState, 0),
		})
	}
	sort.Slice(stacks, func(i, j int) bool { return stacks[i].Name < stacks[j].Name })
	return stacks, nil
}

func stackID(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return hex.EncodeToString(sum[:6])
}

type composeConfig struct {
	Services map[string]struct {
		Image string `json:"image"`
	} `json:"services"`
}

type composeContainer struct {
	ID      string `json:"ID"`
	Image   string `json:"Image"`
	Service string `json:"Service"`
	State   string `json:"State"`
}

func (d *Docker) CheckStack(ctx context.Context, stack StackState) (StackState, error) {
	stack.Status = "checking"
	stack.Error = ""
	directory := filepath.Dir(stack.ComposeFile)
	configOutput, err := d.runner.Run(ctx, directory, "docker", "compose", "-f", stack.ComposeFile, "config", "--format", "json")
	if err != nil {
		return stack, fmt.Errorf("read compose configuration: %w", err)
	}
	var config composeConfig
	if err := json.Unmarshal(configOutput, &config); err != nil {
		return stack, fmt.Errorf("decode compose configuration: %w", err)
	}

	serviceNames := make([]string, 0, len(config.Services))
	for name, service := range config.Services {
		if service.Image != "" {
			serviceNames = append(serviceNames, name)
		}
	}
	sort.Strings(serviceNames)
	if len(serviceNames) == 0 {
		stack.Services = nil
		stack.Status = "up_to_date"
		return stack, nil
	}

	pullArgs := []string{"compose", "-f", stack.ComposeFile, "pull", "--quiet"}
	pullArgs = append(pullArgs, serviceNames...)
	if _, err := d.runner.Run(ctx, directory, "docker", pullArgs...); err != nil {
		return stack, fmt.Errorf("pull image metadata: %w", err)
	}

	containers := make(map[string]composeContainer)
	psOutput, err := d.runner.Run(ctx, directory, "docker", "compose", "-f", stack.ComposeFile, "ps", "--all", "--format", "json")
	if err != nil {
		return stack, fmt.Errorf("list compose containers: %w", err)
	}
	for _, container := range parseComposeContainers(psOutput) {
		containers[container.Service] = container
	}

	services := make([]ServiceState, 0, len(serviceNames))
	updates := 0
	for _, name := range serviceNames {
		image := config.Services[name].Image
		service := ServiceState{Name: name, Image: image}
		if container, ok := containers[name]; ok {
			service.ContainerID = shortID(container.ID)
			service.ContainerState = strings.ToLower(container.State)
			current, inspectErr := d.runner.Run(ctx, directory, "docker", "inspect", "--format={{.Image}}", container.ID)
			if inspectErr == nil {
				service.CurrentImageID = strings.TrimSpace(string(current))
			}
		}
		target, inspectErr := d.runner.Run(ctx, directory, "docker", "image", "inspect", "--format={{.Id}}", image)
		if inspectErr != nil {
			return stack, fmt.Errorf("inspect image %s: %w", image, inspectErr)
		}
		service.TargetImageID = strings.TrimSpace(string(target))
		service.UpdateAvailable = service.CurrentImageID != "" && service.TargetImageID != "" && service.CurrentImageID != service.TargetImageID
		if service.UpdateAvailable {
			updates++
		}
		services = append(services, service)
	}

	stack.Services = services
	stack.UpdatesAvailable = updates
	if updates > 0 {
		stack.Status = "update_available"
	} else {
		stack.Status = "up_to_date"
	}
	return stack, nil
}

func (d *Docker) UpdateStack(ctx context.Context, stack StackState) error {
	directory := filepath.Dir(stack.ComposeFile)
	_, err := d.runner.Run(ctx, directory, "docker", "compose", "-f", stack.ComposeFile, "up", "-d", "--remove-orphans")
	if err != nil {
		return fmt.Errorf("apply compose update: %w", err)
	}
	return nil
}

func parseComposeContainers(output []byte) []composeContainer {
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return nil
	}
	var list []composeContainer
	if trimmed[0] == '[' {
		_ = json.Unmarshal(trimmed, &list)
		return list
	}
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		var item composeContainer
		if json.Unmarshal(line, &item) == nil {
			list = append(list, item)
		}
	}
	return list
}

func shortID(id string) string {
	id = strings.TrimPrefix(strings.TrimSpace(id), "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
