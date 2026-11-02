package tailsplit

import _ "embed"

// ExampleYAML is also embedded in the binary for tailsplit init.
//
//go:embed config.example.yaml
var ExampleYAML []byte
