package contenders

import "testing"

func TestTheVersionIsThePinnedTagOfTheDockerfilesUpstreamImage(t *testing.T) {
	tests := []struct {
		name       string
		dockerfile string
		want       UpstreamImage
	}{
		{"official image", "FROM nginx:1.31.8\n", UpstreamImage{Reference: "nginx:1.31.8", Version: "1.31.8"}},
		{"after comments", "# a comment\n\nfrom haproxy:3.4.7\nCOPY haproxy.cfg /usr/local/etc/haproxy/haproxy.cfg\n",
			UpstreamImage{Reference: "haproxy:3.4.7", Version: "3.4.7"}},
		{"a platform flag", "FROM --platform=linux/amd64 traefik:v3.7.17\n", UpstreamImage{Reference: "traefik:v3.7.17", Version: "v3.7.17"}},
		{"a digest next to the tag", "FROM traefik:v3.7.17@sha256:68ae5fe5\n",
			UpstreamImage{Reference: "traefik:v3.7.17@sha256:68ae5fe5", Version: "v3.7.17"}},
		{"a registry with a port", "FROM registry.example:5000/proxies/nginx:1.30.6\n",
			UpstreamImage{Reference: "registry.example:5000/proxies/nginx:1.30.6", Version: "1.30.6"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := upstreamImageOf(test.dockerfile)
			if err != nil {
				t.Fatalf("upstreamImageOf: %v", err)
			}
			if got != test.want {
				t.Errorf("upstream image = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestAnUpstreamImageWithoutAnExactVersionIsRefused(t *testing.T) {
	for _, dockerfile := range []string{
		"FROM nginx\n",
		"FROM nginx:latest\n",
		"FROM registry.example:5000/nginx\n",
		"COPY nginx.conf /etc/nginx/nginx.conf\n",
	} {
		if got, err := upstreamImageOf(dockerfile); err == nil {
			t.Errorf("upstreamImageOf(%q) = %+v, want an error", dockerfile, got)
		}
	}
}

func TestEveryUpstreamContenderPinsAnExactVersion(t *testing.T) {
	for _, definition := range []string{"haproxy", "nginx", "traefik"} {
		pinned, err := PinnedUpstreamImage(definition)
		if err != nil {
			t.Errorf("%s: %v", definition, err)
			continue
		}
		if pinned.Version == "" {
			t.Errorf("%s: pinned image %+v has no version", definition, pinned)
		}
	}
}
