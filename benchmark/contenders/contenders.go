// Package contenders holds the Contender definitions; each upstream Dockerfile's FROM line is its
// only version pin, so a Dependabot bump of it is what the tables show.
package contenders

import (
	"embed"
	"fmt"
	"path"
	"strings"
)

//go:embed caddy/Dockerfile nginx/Dockerfile traefik/Dockerfile
var upstreamContenderDockerfiles embed.FS

// UpstreamImage is the proxy image a Contender definition is built on.
type UpstreamImage struct {
	Reference string
	// Version is the reference's tag, the proxy's release.
	Version string
}

func PinnedUpstreamImage(definition string) (UpstreamImage, error) {
	dockerfile, err := upstreamContenderDockerfiles.ReadFile(path.Join(definition, "Dockerfile"))
	if err != nil {
		return UpstreamImage{}, err
	}
	pinned, err := upstreamImageOf(string(dockerfile))
	if err != nil {
		return UpstreamImage{}, fmt.Errorf("contenders/%s/Dockerfile: %w", definition, err)
	}
	return pinned, nil
}

func upstreamImageOf(dockerfile string) (UpstreamImage, error) {
	for _, line := range strings.Split(dockerfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		reference := ""
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "--") {
				reference = field
				break
			}
		}
		return pinnedVersionOf(reference)
	}
	return UpstreamImage{}, fmt.Errorf("no FROM line")
}

func pinnedVersionOf(reference string) (UpstreamImage, error) {
	withoutDigest, _, _ := strings.Cut(reference, "@")
	lastPathElement := withoutDigest[strings.LastIndex(withoutDigest, "/")+1:]
	_, tag, tagged := strings.Cut(lastPathElement, ":")
	if !tagged || tag == "" || tag == "latest" {
		return UpstreamImage{}, fmt.Errorf("image %q is not pinned to an exact version tag", reference)
	}
	return UpstreamImage{Reference: reference, Version: tag}, nil
}
