package developerapi

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// The OpenAPI spec is what a third party reads before writing a line of code,
// and it is hand-written, so nothing was checking that it even parses. It
// shipped for a while with a duplicated `description` under servers, which a
// strict parser rejects outright: a generator or a docs viewer handed that
// file would refuse the whole thing, not just the one key.
func TestOpenAPISpecParses(t *testing.T) {
	raw, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("reading the spec: %v", err)
	}

	var spec map[string]interface{}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("the spec is not valid YAML: %v", err)
	}

	for _, key := range []string{"openapi", "info", "servers", "paths", "components"} {
		if _, ok := spec[key]; !ok {
			t.Errorf("the spec has no %q section", key)
		}
	}
}
