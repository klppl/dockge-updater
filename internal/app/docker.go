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
	"time"
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
	command.Env = append(os.Environ(), "CI=1", "COMPOSE_INTERACTIVE_NO_CLI=1", "DOCKER_CLI_HINTS=false")
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
		return nil, fmt.Errorf("read stacks directory: %w", err)
	}

	stacks := make([]StackState, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
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

func (d *Docker) CheckStack(ctx context.Context, stack StackState, onProgress ...func(string)) (StackState, error) {
	var progress func(string)
	if len(onProgress) > 0 && onProgress[0] != nil {
		progress = onProgress[0]
	} else {
		progress = func(string) {}
	}

	stack.Status = "checking"
	stack.Error = ""
	directory := filepath.Dir(stack.ComposeFile)

	progress(fmt.Sprintf("%s: reading compose configuration", stack.Name))
	configCtx, cancelConfig := context.WithTimeout(ctx, 45*time.Second)
	defer cancelConfig()
	configOutput, err := d.runner.Run(configCtx, directory, "docker", "compose", "-f", stack.ComposeFile, "config", "--format", "json")
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

	pullErrors := make(map[string]error)
	for sIdx, name := range serviceNames {
		image := config.Services[name].Image
		if len(serviceNames) > 1 {
			progress(fmt.Sprintf("%s (%d/%d): pulling %s", stack.Name, sIdx+1, len(serviceNames), image))
		} else {
			progress(fmt.Sprintf("%s: pulling %s", stack.Name, image))
		}

		pullCtx, cancelPull := context.WithTimeout(ctx, 90*time.Second)
		pullArgs := []string{"compose", "-f", stack.ComposeFile, "pull", "--quiet", name}
		if _, err := d.runner.Run(pullCtx, directory, "docker", pullArgs...); err != nil {
			pullErrors[name] = err
		}
		cancelPull()
	}

	progress(fmt.Sprintf("%s: inspecting containers", stack.Name))
	psCtx, cancelPs := context.WithTimeout(ctx, 30*time.Second)
	defer cancelPs()
	containers := make(map[string]composeContainer)
	psOutput, err := d.runner.Run(psCtx, directory, "docker", "compose", "-f", stack.ComposeFile, "ps", "--all", "--format", "json")
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
		progress(fmt.Sprintf("%s: inspecting %s", stack.Name, image))
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
			if pErr, ok := pullErrors[name]; ok {
				service.Error = fmt.Sprintf("pull failed: %v", pErr)
			} else {
				service.Error = fmt.Sprintf("inspect image: %v", inspectErr)
			}
			services = append(services, service)
			continue
		}
		inspected, inspectErr := parseImageInspect(target)
		if inspectErr != nil {
			service.Error = fmt.Sprintf("decode image metadata: %v", inspectErr)
			services = append(services, service)
			continue
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
	if len(pullErrors) > 0 {
		stack.Status = "error"
		var errMsgs []string
		for svc, pErr := range pullErrors {
			errMsgs = append(errMsgs, fmt.Sprintf("%s: %v", svc, pErr))
		}
		stack.Error = fmt.Sprintf("failed to pull image for %s", strings.Join(errMsgs, "; "))
	} else if updates > 0 {
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
