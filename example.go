package tailsplit

import _ "embed"

// DefaultYAML is the shared initial desktop configuration, without device identities.
//
//go:embed config.default.yaml
var DefaultYAML []byte

// ExampleYAML is a configured CLI example with a placeholder exit for tailsplit init.
//
//go:embed config.example.yaml
var ExampleYAML []byte
