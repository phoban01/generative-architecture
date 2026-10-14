package ir

import "fmt"

// Source locates a fact in a file. SHA256 is the hash of the file (or of the
// declaration the fact was taken from) so receipts can detect drift.
type Source struct {
	File   string `yaml:"file,omitempty"`
	Line   int    `yaml:"line,omitempty"`
	SHA256 string `yaml:"sha256,omitempty"`
}

// Provenance records where a fact came from: the module and revision it was
// read from, the source position, the generator that produced it and the
// bounds under which the generator ran.
type Provenance struct {
	Module           string            `yaml:"module,omitempty"`
	ModuleVersion    string            `yaml:"moduleVersion,omitempty"`
	Revision         string            `yaml:"revision,omitempty"`
	Source           Source            `yaml:"source,omitempty"`
	Generator        string            `yaml:"generator"`
	GeneratorVersion string            `yaml:"generatorVersion,omitempty"`
	Bounds           map[string]string `yaml:"bounds,omitempty"`
}

// Validate requires a generator and something that pins the input: either a
// source hash or a module version.
func (p Provenance) Validate() error {
	if p.Generator == "" {
		return fmt.Errorf("provenance: generator is required")
	}
	if p.Source.SHA256 == "" && p.ModuleVersion == "" {
		return fmt.Errorf("provenance from %s: either source.sha256 or moduleVersion is required", p.Generator)
	}
	if p.Source.Line < 0 {
		return fmt.Errorf("provenance from %s: negative source line", p.Generator)
	}
	return nil
}
