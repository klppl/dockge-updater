package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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

type inputCommandRunner interface {
	RunWithInput(ctx context.Context, directory, input, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	return runCommand(ctx, directory, "", name, args...)
}

func (ExecRunner) RunWithInput(ctx context.Context, directory, input, name string, args ...string) ([]byte, error) {
	return runCommand(ctx, directory, input, name, args...)
}

func runCommand(ctx context.Context, directory, input, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	if input != "" {
		command.Stdin = strings.NewReader(input)
	}
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

func (d *Docker) Login(ctx context.Context, registry, username, token string) error {
	username = strings.TrimSpace(username)
	token = strings.TrimSpace(token)
	if username == "" || token == "" {
		return errors.New("registry username and token are both required")
	}
	runner, ok := d.runner.(inputCommandRunner)
	if !ok {
		return errors.New("command runner does not support standard input")
	}
	if _, err := runner.RunWithInput(ctx, ".", token+"\n", "docker", "login", registry, "--username", username, "--password-stdin"); err != nil {
		return fmt.Errorf("login to %s: %w", registry, err)
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

type dockerImageInspect struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
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
		target, inspectErr := d.runner.Run(ctx, directory, "docker", "image", "inspect", image)
		if inspectErr != nil {
			return stack, fmt.Errorf("inspect image %s: %w", image, inspectErr)
		}
		inspected, inspectErr := parseImageInspect(target)
		if inspectErr != nil {
			return stack, fmt.Errorf("decode image metadata for %s: %w", image, inspectErr)
		}
		service.TargetImageID = inspected.ID
		service.SourceURL, service.ChangelogURL, service.ImageVersion, service.ImageRevision = imageMetadata(image, inspected.Config.Labels)
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

func parseImageInspect(output []byte) (dockerImageInspect, error) {
	var images []dockerImageInspect
	if err := json.Unmarshal(output, &images); err != nil {
		return dockerImageInspect{}, err
	}
	if len(images) == 0 || strings.TrimSpace(images[0].ID) == "" {
		return dockerImageInspect{}, errors.New("image inspect returned no image")
	}
	return images[0], nil
}

func imageMetadata(image string, labels map[string]string) (source, changelog, version, revision string) {
	if labels != nil {
		source = safeHTTPURL(labels["org.opencontainers.image.source"])
		if source == "" {
			source = safeHTTPURL(labels["org.label-schema.vcs-url"])
		}
		version = strings.TrimSpace(labels["org.opencontainers.image.version"])
		revision = strings.TrimSpace(labels["org.opencontainers.image.revision"])
	}
	if source == "" {
		source = ghcrSourceURL(image)
	}
	changelog = githubReleasesURL(source)
	return source, changelog, version, revision
}

func safeHTTPURL(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "git+"))
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

func ghcrSourceURL(image string) string {
	reference := strings.TrimSpace(image)
	if at := strings.IndexByte(reference, '@'); at >= 0 {
		reference = reference[:at]
	}
	if colon := strings.LastIndexByte(reference, ':'); colon > strings.LastIndexByte(reference, '/') {
		reference = reference[:colon]
	}
	parts := strings.Split(reference, "/")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "ghcr.io") || parts[1] == "" || parts[2] == "" {
		return ""
	}
	return "https://github.com/" + url.PathEscape(parts[1]) + "/" + url.PathEscape(parts[2])
}

func githubReleasesURL(source string) string {
	parsed, err := url.Parse(source)
	if err != nil || !strings.EqualFold(parsed.Host, "github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	repository := strings.TrimSuffix(parts[1], ".git")
	return "https://github.com/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(repository) + "/releases"
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
