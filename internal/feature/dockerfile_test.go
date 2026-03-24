package feature

import (
	"strings"
	"testing"
)

func TestGenerateDockerfileSingle(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "my-feature",
			Config: &FeatureConfig{
				ID: "my-feature",
			},
		},
	}

	content, prefix := GenerateDockerfile(features, "vscode", "vscode", nil, nil)

	// Prefix checks.
	if !strings.Contains(prefix, "# syntax=docker.io/docker/dockerfile:1.4") {
		t.Error("prefix missing syntax directive")
	}
	if !strings.Contains(prefix, "ARG _DEV_CONTAINERS_BASE_IMAGE=placeholder") {
		t.Error("prefix missing base image ARG")
	}

	// Content checks.
	if !strings.Contains(content, "FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_base_stage") {
		t.Error("content missing FROM with base stage")
	}
	if !strings.Contains(content, "FROM dev_containers_base_stage AS dev_containers_target_stage") {
		t.Error("content missing FROM with target stage")
	}
	if !strings.Contains(content, "USER root") {
		t.Error("content missing USER root")
	}
	if !strings.Contains(content, "COPY .crib-features/ /tmp/build-features/") {
		t.Error("content missing COPY features")
	}
	if !strings.Contains(content, "devcontainer-features-install.sh") {
		t.Error("content missing install script reference")
	}
	if !strings.Contains(content, "ARG _DEV_CONTAINERS_IMAGE_USER=vscode") {
		t.Error("content missing user restore ARG")
	}
	if !strings.Contains(content, "USER $_DEV_CONTAINERS_IMAGE_USER") {
		t.Error("content missing user restore")
	}
}

func TestGenerateDockerfileMultiple(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "feature-a",
			Config:   &FeatureConfig{ID: "feature-a"},
		},
		{
			ConfigID: "feature-b",
			Config:   &FeatureConfig{ID: "feature-b"},
		},
	}

	content, _ := GenerateDockerfile(features, "root", "root", nil, nil)

	// Both features should have numbered install scripts.
	if !strings.Contains(content, "/tmp/build-features/0/devcontainer-features-install.sh") {
		t.Error("missing feature 0 install script")
	}
	if !strings.Contains(content, "/tmp/build-features/1/devcontainer-features-install.sh") {
		t.Error("missing feature 1 install script")
	}
}

func TestGenerateDockerfileFeatureContainerEnv(t *testing.T) {
	// Simulates the node/nvm feature: containerEnv declares PATH with ${PATH}
	// reference. Feature ENV must use unescaped dollars so Docker expands
	// ${PATH} at build time against the image's existing PATH.
	features := []*FeatureSet{
		{
			ConfigID: "node",
			Config: &FeatureConfig{
				ID: "node",
				ContainerEnv: map[string]string{
					"NVM_DIR":              "/usr/local/share/nvm",
					"NVM_SYMLINK_CURRENT":  "true",
					"PATH":                 "/usr/local/share/nvm/current/bin:${PATH}",
				},
			},
		},
	}

	content, _ := GenerateDockerfile(features, "root", "root", nil, nil)

	// Feature PATH should use unescaped ${PATH} — Docker's ENV expands it
	// at build time so the image ships with the fully resolved PATH.
	if !strings.Contains(content, `ENV PATH="/usr/local/share/nvm/current/bin:${PATH}"`) {
		t.Errorf("feature PATH ENV should have unescaped ${PATH}, got:\n%s", content)
	}
	if !strings.Contains(content, `ENV NVM_DIR="/usr/local/share/nvm"`) {
		t.Errorf("missing NVM_DIR ENV instruction in:\n%s", content)
	}

	// Feature ENV should appear BEFORE the USER restore (within the feature
	// install layer), not after it.
	envIdx := strings.Index(content, `ENV PATH=`)
	userIdx := strings.Index(content, "USER $_DEV_CONTAINERS_IMAGE_USER")
	if envIdx < 0 || userIdx < 0 || envIdx > userIdx {
		t.Errorf("feature ENV should appear before USER restore")
	}
}

func TestGenerateDockerfilePrefix(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "test",
			Config:   &FeatureConfig{ID: "test"},
		},
	}

	_, prefix := GenerateDockerfile(features, "", "", nil, nil)

	lines := strings.Split(strings.TrimSpace(prefix), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 prefix lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "# syntax=docker.io/docker/dockerfile:1.4" {
		t.Errorf("line 0 = %q", lines[0])
	}
	if lines[1] != "ARG _DEV_CONTAINERS_BASE_IMAGE=placeholder" {
		t.Errorf("line 1 = %q", lines[1])
	}
}

func TestGenerateDockerfileUserVariables(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "test",
			Config:   &FeatureConfig{ID: "test"},
		},
	}

	content, _ := GenerateDockerfile(features, "vscode", "vscode", nil, nil)

	if !strings.Contains(content, "USER root") {
		t.Error("missing USER root for feature installation")
	}
	if !strings.Contains(content, "USER $_DEV_CONTAINERS_IMAGE_USER") {
		t.Error("missing user restore at end")
	}
}

func TestGenerateDockerfileCacheMounts(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "test",
			Config:   &FeatureConfig{ID: "test"},
		},
	}

	mounts := []string{"/var/cache/apt", "/var/lib/apt/lists", "/root/.npm"}
	content, _ := GenerateDockerfile(features, "root", "root", mounts, nil)

	// Each cache mount should appear on the RUN line.
	for _, m := range mounts {
		want := "--mount=type=cache,target=" + m
		if !strings.Contains(content, want) {
			t.Errorf("missing cache mount %q in:\n%s", want, content)
		}
	}

	// The bind mount should still be there.
	if !strings.Contains(content, "--mount=type=bind,from=dev_containers_base_stage") {
		t.Error("missing bind mount")
	}
}

func TestGenerateDockerfileCacheMountsAptDisablesDockerClean(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "test",
			Config:   &FeatureConfig{ID: "test"},
		},
	}

	// With apt cache: should disable docker-clean.
	content, _ := GenerateDockerfile(features, "root", "root", []string{"/var/cache/apt"}, nil)
	if !strings.Contains(content, "rm -f /etc/apt/apt.conf.d/docker-clean") {
		t.Error("expected docker-clean removal with apt cache")
	}

	// Without apt: no docker-clean removal.
	content, _ = GenerateDockerfile(features, "root", "root", []string{"/root/.npm"}, nil)
	if strings.Contains(content, "docker-clean") {
		t.Error("unexpected docker-clean removal without apt cache")
	}

	// No cache mounts: no docker-clean removal.
	content, _ = GenerateDockerfile(features, "root", "root", nil, nil)
	if strings.Contains(content, "docker-clean") {
		t.Error("unexpected docker-clean removal with nil cache mounts")
	}
}

func TestGenerateDockerfileSingleEntrypoint(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "docker-in-docker",
			Config: &FeatureConfig{
				ID:         "docker-in-docker",
				Entrypoint: "/usr/local/share/docker-init.sh",
			},
		},
	}

	content, _ := GenerateDockerfile(features, "root", "root", nil, nil)

	if !strings.Contains(content, `ENTRYPOINT ["/usr/local/share/docker-init.sh"]`) {
		t.Errorf("missing single ENTRYPOINT instruction in:\n%s", content)
	}
}

func TestGenerateDockerfileMultipleEntrypoints(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "feature-a",
			Config: &FeatureConfig{
				ID:         "feature-a",
				Entrypoint: "/entry-a.sh",
			},
		},
		{
			ConfigID: "feature-b",
			Config: &FeatureConfig{
				ID:         "feature-b",
				Entrypoint: "/entry-b.sh",
			},
		},
	}

	content, _ := GenerateDockerfile(features, "root", "root", nil, nil)

	// Later features wrap earlier ones (outermost runs first).
	if !strings.Contains(content, "crib-entrypoint.sh") {
		t.Errorf("missing wrapper entrypoint script in:\n%s", content)
	}
	// The wrapper should chain: exec /entry-b.sh /entry-a.sh "$@"
	if !strings.Contains(content, "/entry-b.sh /entry-a.sh") {
		t.Errorf("expected /entry-b.sh to wrap /entry-a.sh in:\n%s", content)
	}
	if !strings.Contains(content, `ENTRYPOINT ["/usr/local/share/crib-entrypoint.sh"]`) {
		t.Errorf("missing wrapper ENTRYPOINT in:\n%s", content)
	}
}

func TestGenerateDockerfileNoEntrypoint(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "plain",
			Config:   &FeatureConfig{ID: "plain"},
		},
	}

	content, _ := GenerateDockerfile(features, "root", "root", nil, nil)

	if strings.Contains(content, "ENTRYPOINT") {
		t.Errorf("unexpected ENTRYPOINT for features without entrypoints:\n%s", content)
	}
}

func TestGenerateDockerfileNoCacheMountsWithoutProviders(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "test",
			Config:   &FeatureConfig{ID: "test"},
		},
	}

	content, _ := GenerateDockerfile(features, "root", "root", nil, nil)

	// Should not have any cache mounts.
	if strings.Contains(content, "type=cache") {
		t.Errorf("unexpected cache mount in:\n%s", content)
	}
}

func TestGenerateDockerfileConfigContainerEnv(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "test",
			Config:   &FeatureConfig{ID: "test"},
		},
	}

	configEnv := map[string]string{
		"MY_VAR":  "hello",
		"MY_PATH": "/custom/bin:${PATH}",
	}
	content, _ := GenerateDockerfile(features, "root", "root", nil, configEnv)

	// Config containerEnv should appear after USER restore.
	if !strings.Contains(content, `ENV MY_VAR="hello"`) {
		t.Errorf("missing MY_VAR ENV instruction in:\n%s", content)
	}
	// Dollar signs should be escaped in config containerEnv.
	if !strings.Contains(content, `ENV MY_PATH="/custom/bin:\${PATH}"`) {
		t.Errorf("missing or improperly escaped MY_PATH ENV instruction in:\n%s", content)
	}

	// Config ENV should come after the USER restore line.
	userIdx := strings.Index(content, "USER $_DEV_CONTAINERS_IMAGE_USER")
	envIdx := strings.Index(content, "ENV MY_VAR=")
	if userIdx < 0 || envIdx < 0 || envIdx < userIdx {
		t.Errorf("config containerEnv should appear after USER restore")
	}
}

func TestGenerateDockerfileConfigContainerEnvNil(t *testing.T) {
	features := []*FeatureSet{
		{
			ConfigID: "test",
			Config:   &FeatureConfig{ID: "test"},
		},
	}

	content, _ := GenerateDockerfile(features, "root", "root", nil, nil)

	// With nil configContainerEnv, no extra ENV lines after USER restore.
	lines := strings.Split(content, "\n")
	var afterUser []string
	found := false
	for _, line := range lines {
		if found && strings.TrimSpace(line) != "" {
			afterUser = append(afterUser, line)
		}
		if strings.Contains(line, "USER $_DEV_CONTAINERS_IMAGE_USER") {
			found = true
		}
	}
	for _, line := range afterUser {
		if strings.HasPrefix(line, "ENV ") {
			t.Errorf("unexpected ENV after USER restore with nil configContainerEnv: %s", line)
		}
	}
}

func TestEscapeEnvValue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", `"hello"`},
		{"${PATH}", `"\${PATH}"`},
		{`say "hi"`, `"say \"hi\""`},
		{`back\slash`, `"back\\slash"`},
		{"/bin:${PATH}:$HOME", `"/bin:\${PATH}:\$HOME"`},
	}
	for _, tt := range tests {
		got := escapeEnvValue(tt.input)
		if got != tt.want {
			t.Errorf("escapeEnvValue(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAppendConfigContainerEnv_AddsEnvInstructions(t *testing.T) {
dockerfileContent := "FROM ubuntu:24.04\nRUN apt-get update"
containerEnv := map[string]string{
"FOO": "bar",
"BAZ": "/path:${PATH}",
}

result := AppendConfigContainerEnv(dockerfileContent, containerEnv)

if !strings.Contains(result, "FROM ubuntu:24.04") {
t.Error("original Dockerfile content should be preserved")
}
if !strings.Contains(result, `ENV FOO="bar"`) {
t.Errorf("expected ENV FOO=\"bar\" in output, got:\n%s", result)
}
// Dollar signs should be escaped so Docker stores them literally.
if !strings.Contains(result, `ENV BAZ="/path:\${PATH}"`) {
t.Errorf("expected escaped ENV BAZ in output, got:\n%s", result)
}
}

func TestAppendConfigContainerEnv_SortedOrder(t *testing.T) {
dockerfileContent := "FROM alpine:3.20"
containerEnv := map[string]string{
"Z_LAST":  "last",
"A_FIRST": "first",
"M_MID":   "mid",
}

result := AppendConfigContainerEnv(dockerfileContent, containerEnv)
lines := strings.Split(result, "\n")

var envLines []string
for _, l := range lines {
if strings.HasPrefix(l, "ENV ") {
envLines = append(envLines, l)
}
}

if len(envLines) != 3 {
t.Fatalf("expected 3 ENV lines, got %d: %v", len(envLines), envLines)
}
if !strings.HasPrefix(envLines[0], "ENV A_FIRST") {
t.Errorf("first ENV should be A_FIRST, got %q", envLines[0])
}
if !strings.HasPrefix(envLines[1], "ENV M_MID") {
t.Errorf("second ENV should be M_MID, got %q", envLines[1])
}
if !strings.HasPrefix(envLines[2], "ENV Z_LAST") {
t.Errorf("third ENV should be Z_LAST, got %q", envLines[2])
}
}

func TestAppendConfigContainerEnv_EmptyMap_ReturnsUnchanged(t *testing.T) {
dockerfileContent := "FROM ubuntu:24.04"
result := AppendConfigContainerEnv(dockerfileContent, nil)
if result != dockerfileContent {
t.Errorf("expected unchanged content for nil containerEnv, got:\n%s", result)
}
result = AppendConfigContainerEnv(dockerfileContent, map[string]string{})
if result != dockerfileContent {
t.Errorf("expected unchanged content for empty containerEnv, got:\n%s", result)
}
}
