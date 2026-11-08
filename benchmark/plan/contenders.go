package plan

import "github.com/aaydin-tr/divisor/benchmark/contenders"

// CheckoutDivisorImage is the local tag the checkout is built into, in the
// release image shape: a static linux/amd64 binary in the release Dockerfile.
const CheckoutDivisorImage = "divisor-benchmark/divisor:checkout"

const (
	divisorContenderID = "divisor"
	divisorName        = "divisor"
	divisorDefinition  = "divisor"
	// divisorImageBuildArg is the image contenders/divisor builds FROM.
	divisorImageBuildArg = "DIVISOR_IMAGE"
)

type Contender struct {
	// ID names the Contender's containers, raw result files and JSON entries.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Version is what the tables show next to Name.
	Version string `json:"version"`
	// Image is the proxy image under test, before its definition adds the config.
	Image             string `json:"image"`
	ImageFromCheckout bool   `json:"image_from_checkout,omitempty"`
	// Definition is the directory under contenders/ holding the Dockerfile
	// and config that put Image in the Equivalent setup.
	Definition string            `json:"definition"`
	BuildArgs  map[string]string `json:"build_args,omitempty"`
}

// upstreamContenders run after divisor in every round, in this order, which a
// ranked table also keeps for rows with equal numbers.
var upstreamContenders = []struct{ id, name string }{
	{"caddy", "Caddy"},
	{"nginx", "nginx"},
	{"traefik", "Traefik"},
}

func contendersFor(inputs *Inputs) ([]Contender, error) {
	decidedContenders := []Contender{divisorUnderTest(inputs)}
	for _, upstream := range upstreamContenders {
		pinned, err := contenders.PinnedUpstreamImage(upstream.id)
		if err != nil {
			return nil, err
		}
		decidedContenders = append(decidedContenders, Contender{
			ID:         upstream.id,
			Name:       upstream.name,
			Version:    pinned.Version,
			Image:      pinned.Reference,
			Definition: upstream.id,
		})
	}
	return decidedContenders, nil
}

func divisorContender(version, image string) Contender {
	return Contender{
		ID:         divisorContenderID,
		Name:       divisorName,
		Version:    version,
		Image:      image,
		Definition: divisorDefinition,
		BuildArgs:  map[string]string{divisorImageBuildArg: image},
	}
}

func divisorFromCheckout(inputs *Inputs) Contender {
	divisor := divisorContender(checkoutVersion(inputs), CheckoutDivisorImage)
	divisor.ImageFromCheckout = true
	return divisor
}

func checkoutVersion(inputs *Inputs) string {
	version := shortCommit(inputs.Commit)
	if inputs.Ref != "" {
		version = inputs.Ref + "@" + version
	}
	if inputs.CheckoutHasUncommittedChanges {
		version += "-dirty"
	}
	return version
}
