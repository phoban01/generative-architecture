package krm

import (
	"bytes"
	"fmt"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
)

func DecodeYAML(b []byte, out any) error {
	var tree yaml.Node
	if err := yaml.Unmarshal(b, &tree); err != nil {
		return err
	}
	var inspect func(*yaml.Node) error
	inspect = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode {
			return fmt.Errorf("YAML aliases are not supported")
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Tag != "!!str" || seen[k.Value] {
					return fmt.Errorf("duplicate or non-string YAML key %q", k.Value)
				}
				seen[k.Value] = true
			}
		}
		for _, child := range n.Content {
			if err := inspect(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := inspect(&tree); err != nil {
		return err
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one YAML document")
	}
	return nil
}
func WriteYAML(path string, value any) error {
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(value); err != nil {
		return err
	}
	if err := e.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0644)
}
