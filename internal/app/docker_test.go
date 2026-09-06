package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	responses map[string][]byte
	errors    map[string]error
	calls     []string
	inputs    []string
}

func (f *fakeRunner) Run(_ context.Context, directory, name string, args ...string) ([]byte, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, directory+" :: "+key)
	if err := f.errors[key]; err != nil {
		return nil, err
	}
	response, ok := f.responses[key]
	if !ok {
		return nil, fmt.Errorf("unexpected command: %s", key)
	}
	return response, nil
}

func (f *fakeRunner) RunWithInput(ctx context.Context, directory, input, name string, args ...string) ([]byte, error) {
	f.inputs = append(f.inputs, input)
	return f.Run(ctx, directory, name, args...)
}

func TestDockerLoginUsesStandardInput(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{
		"docker login ghcr.io --username octocat --password-stdin": []byte("Login Succeeded\n"),
	}, errors: map[string]error{}}

	if err := NewDocker(runner).Login(context.Background(), "ghcr.io", "octocat", "secret-token"); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0] != "secret-token\n" {
		t.Fatalf("token was not passed through standard input: %#v", runner.inputs)
	}
	if strings.Contains(strings.Join(runner.calls, " "), "secret-token") {
		t.Fatal("token must not appear in command arguments")
	}
}

func TestDiscoverStacks(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "alpha", "compose.yaml"),
		filepath.Join(root, "beta", "docker-compose.yml"),
		filepath.Join(root, "ignored", "notes.txt"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("services: {}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	stacks, err := DiscoverStacks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(stacks) != 2 {
		t.Fatalf("expected 2 stacks, got %d", len(stacks))
	}
	if stacks[0].Name != "alpha" || stacks[1].Name != "beta" {
		t.Fatalf("unexpected stack order: %#v", stacks)
	}
	if stacks[0].ID == stacks[1].ID || len(stacks[0].ID) != 12 {
		t.Fatalf("stack IDs should be distinct short hashes: %#v", stacks)
	}
}

func TestParseComposeContainersSupportsArrayAndLines(t *testing.T) {
	array := []byte(`[{"ID":"one","Service":"api","State":"running"},{"ID":"two","Service":"db","State":"exited"}]`)
	lines := []byte("{\"ID\":\"one\",\"Service\":\"api\",\"State\":\"running\"}\n{\"ID\":\"two\",\"Service\":\"db\",\"State\":\"exited\"}\n")
	for name, input := range map[string][]byte{"array": array, "lines": lines} {
		t.Run(name, func(t *testing.T) {
			containers := parseComposeContainers(input)
			if len(containers) != 2 || containers[1].Service != "db" {
				t.Fatalf("unexpected containers: %#v", containers)
			}
		})
	}
}

func TestCheckStackDetectsUpdatedImage(t *testing.T) {
	composeFile := "/opt/stacks/demo/compose.yaml"
	runner := &fakeRunner{responses: map[string][]byte{}, errors: map[string]error{}}
	runner.responses["docker compose -f "+composeFile+" config --format json"] = []byte(`{"services":{"web":{"image":"nginx:latest"}}}`)
	runner.responses["docker compose -f "+composeFile+" pull --quiet web"] = nil
	runner.responses["docker compose -f "+composeFile+" ps --all --format json"] = []byte(`[{"ID":"container123456789","Service":"web","Image":"nginx:latest","State":"running"}]`)
	runner.responses["docker inspect --format={{.Image}} container123456789"] = []byte("sha256:old\n")
	runner.responses["docker image inspect nginx:latest"] = []byte(`[{"Id":"sha256:new","Config":{"Labels":{"org.opencontainers.image.source":"https://github.com/nginx/nginx","org.opencontainers.image.version":"1.29.1","org.opencontainers.image.revision":"abc123"}}}]`)

	checked, err := NewDocker(runner).CheckStack(context.Background(), StackState{
		ID: "demo", Name: "demo", ComposeFile: composeFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status != "update_available" || checked.UpdatesAvailable != 1 {
		t.Fatalf("expected one update, got %#v", checked)
	}
	if len(checked.Services) != 1 || !checked.Services[0].UpdateAvailable {
		t.Fatalf("service should report an update: %#v", checked.Services)
	}
	if checked.Services[0].ContainerID != "container123" {
		t.Fatalf("container ID was not shortened: %q", checked.Services[0].ContainerID)
	}
	if checked.Services[0].SourceURL != "https://github.com/nginx/nginx" {
		t.Fatalf("unexpected source URL: %q", checked.Services[0].SourceURL)
	}
	if checked.Services[0].ChangelogURL != "https://github.com/nginx/nginx/releases" {
		t.Fatalf("unexpected changelog URL: %q", checked.Services[0].ChangelogURL)
	}
	if checked.Services[0].ImageVersion != "1.29.1" || checked.Services[0].ImageRevision != "abc123" {
		t.Fatalf("unexpected image metadata: %#v", checked.Services[0])
	}
}

func TestImageMetadataFallsBackToGHCRRepository(t *testing.T) {
	source, changelog, _, _ := imageMetadata("ghcr.io/example/private-app:latest", nil)
	if source != "https://github.com/example/private-app" {
		t.Fatalf("unexpected source URL: %q", source)
	}
	if changelog != "https://github.com/example/private-app/releases" {
		t.Fatalf("unexpected changelog URL: %q", changelog)
	}
}

func TestImageMetadataRejectsUnsafeSourceURL(t *testing.T) {
	source, changelog, _, _ := imageMetadata("registry.example.com/app:latest", map[string]string{
		"org.opencontainers.image.source": "javascript:alert(1)",
	})
	if source != "" || changelog != "" {
		t.Fatalf("unsafe metadata should not produce links: source=%q changelog=%q", source, changelog)
	}
}
