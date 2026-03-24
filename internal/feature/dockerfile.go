package feature

import (
	"fmt"
	"slices"
	"strings"
)

const (
	dockerfileSyntax = "# syntax=docker.io/docker/dockerfile:1.4"
	baseImageArg     = "ARG _DEV_CONTAINERS_BASE_IMAGE=placeholder"
	baseStageName    = "dev_containers_base_stage"
	targetStageName  = "dev_containers_target_stage"
	builtinEnvFile   = "devcontainer-features.builtin.env"
	featureEnvFile   = "devcontainer-features.env"
)

// GenerateDockerfile produces the Dockerfile content and prefix for installing
// features into the container image.
//
// content is the main Dockerfile body (FROM, COPY, RUN layers).
// prefix is the syntax directive and base image ARG that must appear at the
// top of the final Dockerfile.
//
// cacheMounts are BuildKit cache mount targets (e.g. "/var/cache/apt") to
// attach to each feature install RUN instruction. Pass nil to disable.
//
// configContainerEnv is the containerEnv from devcontainer.json (not from
// features). These are baked into the image as ENV instructions with dollar
// signs escaped, matching the official devcontainer CLI behavior. This keeps
// values like ${PATH} stored literally in the image so Docker does not
// interpolate them at build time. Pass nil if not applicable.
func GenerateDockerfile(features []*FeatureSet, containerUser, remoteUser string, cacheMounts []string, configContainerEnv map[string]string) (content, prefix string) {
	prefix = dockerfileSyntax + "\n" + baseImageArg + "\n"

	var b strings.Builder

	// Alias the base image as a named stage so the RUN --mount below can
	// reference it without a self-referential dependency on targetStageName.
	fmt.Fprintf(&b, "FROM $_DEV_CONTAINERS_BASE_IMAGE AS %s\n", baseStageName)
	b.WriteString("\n")

	// Feature installation stage builds on top of the base.
	fmt.Fprintf(&b, "FROM %s AS %s\n", baseStageName, targetStageName)
	b.WriteString("\n")

	// Switch to root for feature installation.
	b.WriteString("USER root\n")
	b.WriteString("\n")

	// Copy all feature files into the build context.
	fmt.Fprintf(&b, "COPY %s/ /tmp/build-features/\n", ContextFeatureFolder)
	b.WriteString("\n")

	// Source the builtin env file.
	fmt.Fprintf(&b, "RUN cat /tmp/build-features/%s >> /etc/environment 2>/dev/null || true\n", builtinEnvFile)
	b.WriteString("\n")

	// When apt caching is enabled, disable the docker-clean hook that wipes
	// /var/cache/apt/archives after every install. Without this, the BuildKit
	// cache mount is emptied by apt itself during each RUN.
	if hasAptCache(cacheMounts) {
		b.WriteString("RUN rm -f /etc/apt/apt.conf.d/docker-clean 2>/dev/null || true\n\n")
	}

	// Per-feature ENV and RUN layers.
	for i, f := range features {
		// ContainerEnv as ENV instructions.
		for k, v := range f.Config.ContainerEnv {
			fmt.Fprintf(&b, "ENV %s=%q\n", k, v)
		}

		// RUN the feature installation wrapper script.
		// Mount from baseStageName (not the current stage) to avoid a
		// self-referential dependency that Podman and older BuildKit reject.
		fmt.Fprintf(&b, "RUN --mount=type=bind,from=%s,source=/,target=/build-context ", baseStageName)
		for _, target := range cacheMounts {
			fmt.Fprintf(&b, "--mount=type=cache,target=%s ", target)
		}
		fmt.Fprintf(&b, "chmod +x /tmp/build-features/%d/devcontainer-features-install.sh ", i)
		fmt.Fprintf(&b, "&& /tmp/build-features/%d/devcontainer-features-install.sh\n", i)
		b.WriteString("\n")
	}

	// Feature entrypoints: chain them so each feature's daemon starts
	// before the container's main command. Each entrypoint script follows
	// the convention of `exec "$@"` at the end, so chaining works by
	// passing the next entrypoint as an argument.
	var entrypoints []string
	for _, f := range features {
		if f.Config.Entrypoint != "" {
			entrypoints = append(entrypoints, f.Config.Entrypoint)
		}
	}
	if len(entrypoints) == 1 {
		fmt.Fprintf(&b, "ENTRYPOINT [%q]\n", entrypoints[0])
	} else if len(entrypoints) > 1 {
		// Later features wrap earlier ones (outermost runs first).
		// Generate: exec /last.sh /prev.sh ... /first.sh "$@"
		var chain strings.Builder
		for i := len(entrypoints) - 1; i >= 0; i-- {
			if chain.Len() > 0 {
				chain.WriteByte(' ')
			}
			chain.WriteString(entrypoints[i])
		}
		script := fmt.Sprintf("#!/bin/sh\\nexec %s \"$@\"\\n", chain.String())
		fmt.Fprintf(&b, "RUN printf '%s' > /usr/local/share/crib-entrypoint.sh && chmod +x /usr/local/share/crib-entrypoint.sh\n", script)
		b.WriteString("ENTRYPOINT [\"/usr/local/share/crib-entrypoint.sh\"]\n")
	}
	b.WriteString("\n")

	// Restore the original user. The default must match the base image user
	// so the prebuild hash changes when the user changes.
	imageUser := containerUser
	if imageUser == "" {
		imageUser = "root"
	}
	fmt.Fprintf(&b, "ARG _DEV_CONTAINERS_IMAGE_USER=%s\n", imageUser)
	b.WriteString("USER $_DEV_CONTAINERS_IMAGE_USER\n")

	// devcontainer.json containerEnv as ENV instructions with escaped dollar
	// signs. Placed after feature layers and user restore, matching the
	// official devcontainer CLI's #{containerEnvMetadata} placement.
	// Dollar signs are escaped so Docker stores them literally; the shell
	// expands them at runtime (e.g. \${PATH} becomes ${PATH} in the image,
	// then the shell resolves it when a session starts).
	if len(configContainerEnv) > 0 {
		b.WriteString("\n")

		// Sort keys for deterministic Dockerfile output, since content is part
		// of the prebuild hash and map iteration order is random.
		keys := make([]string, 0, len(configContainerEnv))
		for k := range configContainerEnv {
			keys = append(keys, k)
		}
		slices.Sort(keys)

		for _, k := range keys {
			v := configContainerEnv[k]
			fmt.Fprintf(&b, "ENV %s=%s\n", k, escapeEnvValue(v))
		}
	}

	content = b.String()
	return content, prefix
}

// escapeEnvValue formats a value for a Dockerfile ENV instruction with dollar
// signs escaped. This matches the official devcontainer CLI's
// generateContainerEnvs(env, escapeDollar=true) behavior: double quotes,
// backslashes, and dollar signs are all escaped, and the result is wrapped
// in double quotes.
func escapeEnvValue(v string) string {
	// Escape backslashes, double quotes, and dollar signs (in that order).
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, `$`, `\$`)
	return `"` + v + `"`
}

// AppendConfigContainerEnv appends devcontainer.json containerEnv as ENV
// instructions to a Dockerfile content string. Dollar signs are escaped so
// Docker stores values literally (matching the official devcontainer CLI's
// #{containerEnvMetadata} behavior). This is used for the Dockerfile-based
// path without features, where GenerateDockerfile is not called.
// Returns dockerfileContent unchanged if containerEnv is empty.
func AppendConfigContainerEnv(dockerfileContent string, containerEnv map[string]string) string {
	if len(containerEnv) == 0 {
		return dockerfileContent
	}

	keys := make([]string, 0, len(containerEnv))
	for k := range containerEnv {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var b strings.Builder
	b.WriteString(dockerfileContent)
	b.WriteString("\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "ENV %s=%s\n", k, escapeEnvValue(containerEnv[k]))
	}
	return b.String()
}

// hasAptCache reports whether /var/cache/apt is among the cache mount targets.
func hasAptCache(mounts []string) bool {
	return slices.Contains(mounts, "/var/cache/apt")
}
