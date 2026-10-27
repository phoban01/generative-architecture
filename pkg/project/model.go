package project

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// identifier is the form of controller, effect and local names: they become
// model and LikeC4 identifiers.
var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// Controller declares, for one discovered reconciler, the facts its model
// needs that neither the source nor the Abstraction document carries
// (ADOPT-1). arch generate fails when a discovered controller has no
// declaration.
type Controller struct {
	// Receiver is the Go type name of the reconciler, as arch extract
	// records it.
	Receiver string `yaml:"receiver"`
	// Name is the controller's name in the runtime model.
	Name string `yaml:"name"`
	// Workers is the number of concurrent reconciles; RetryTicks is the
	// delay, in model ticks, before a failed key is retried.
	Workers    int `yaml:"workers"`
	RetryTicks int `yaml:"retryTicks"`
	// Key is the abstraction object the reconcile request addresses.
	Key string `yaml:"key"`
	// Effects names the effect of each client method the reconciler calls.
	Effects []ClientEffect `yaml:"effects"`
	// ObjectKeys maps the symbol of a key field (package.Type.Field) to the
	// abstraction object it addresses. The reconcile request's key maps to
	// Key without a declaration.
	ObjectKeys map[string]string `yaml:"objectKeys,omitempty"`
	// Locals keeps a selected field of a Go object local in a declared
	// abstraction local: "<go local>.<json path>" maps to the local's name.
	Locals map[string]string `yaml:"locals,omitempty"`
}

// ClientEffect names the effect of one client method. Call is the method's
// symbol, <package path>.<Receiver>.<Method>. Consistency overrides the
// consistency the runtime profile derives from the receiver.
type ClientEffect struct {
	Call        string `yaml:"call"`
	Effect      string `yaml:"effect"`
	Consistency string `yaml:"consistency,omitempty"`
}

// Symbol splits Call into its package path, receiver type and method.
func (e ClientEffect) Symbol() (pkg, receiver, method string, err error) {
	slash := strings.LastIndex(e.Call, "/")
	parts := strings.Split(e.Call[slash+1:], ".")
	if len(parts) != 3 || parts[0] == "" || !identifier.MatchString(parts[1]) || !identifier.MatchString(parts[2]) {
		return "", "", "", fmt.Errorf("call %q must be <package path>.<Receiver>.<Method>", e.Call)
	}
	return e.Call[:slash+1] + parts[0], parts[1], parts[2], nil
}

// validateModel checks the declarations arch generate consumes.
func (s Spec) validateModel() error {
	if s.Abstraction != "" {
		if err := relativePath("abstraction", s.Abstraction); err != nil {
			return err
		}
	}
	if s.Runtime.MaxTicks < 0 {
		return fmt.Errorf("runtime.maxTicks must be positive, got %d", s.Runtime.MaxTicks)
	}
	if len(s.Controllers) > 0 {
		if s.Abstraction == "" {
			return fmt.Errorf("controllers are declared but no abstraction document is named")
		}
		if s.Runtime.MaxTicks == 0 {
			return fmt.Errorf("controllers are declared but runtime.maxTicks is not set")
		}
	}
	receivers, names := map[string]bool{}, map[string]bool{}
	for i, c := range s.Controllers {
		at := fmt.Sprintf("controllers[%d]", i)
		if !identifier.MatchString(c.Receiver) || !identifier.MatchString(c.Name) {
			return fmt.Errorf("%s: receiver %q and name %q must be identifiers", at, c.Receiver, c.Name)
		}
		if receivers[c.Receiver] || names[c.Name] {
			return fmt.Errorf("%s: receiver %s or name %s declared twice", at, c.Receiver, c.Name)
		}
		receivers[c.Receiver], names[c.Name] = true, true
		if c.Workers <= 0 || c.RetryTicks <= 0 {
			return fmt.Errorf("%s: workers and retryTicks must be positive, got %d and %d", at, c.Workers, c.RetryTicks)
		}
		if c.Key == "" {
			return fmt.Errorf("%s: key must name the abstraction object the reconcile request addresses", at)
		}
		if len(c.Effects) == 0 {
			return fmt.Errorf("%s: at least one client effect is required", at)
		}
		calls := map[string]bool{}
		for _, e := range c.Effects {
			if _, _, _, err := e.Symbol(); err != nil {
				return fmt.Errorf("%s: %w", at, err)
			}
			if calls[e.Call] {
				return fmt.Errorf("%s: call %s declared twice", at, e.Call)
			}
			calls[e.Call] = true
			if !identifier.MatchString(e.Effect) {
				return fmt.Errorf("%s: effect %q of %s must be an identifier", at, e.Effect, e.Call)
			}
			switch e.Consistency {
			case "", "cached", "direct":
			default:
				return fmt.Errorf("%s: consistency %q of %s must be cached or direct", at, e.Consistency, e.Call)
			}
		}
		for symbol, object := range c.ObjectKeys {
			if strings.Count(symbol[strings.LastIndex(symbol, "/")+1:], ".") != 2 || object == "" {
				return fmt.Errorf("%s: object key %q must be <package path>.<Type>.<Field> and name an object", at, symbol)
			}
		}
		for local, name := range c.Locals {
			if !strings.Contains(local, ".") || !identifier.MatchString(name) {
				return fmt.Errorf("%s: local %q must be <go local>.<json path> and map to an identifier, got %q", at, local, name)
			}
		}
	}
	for _, object := range sortedNames(s.Initial) {
		if strings.TrimSpace(s.Initial[object]) == "" {
			return fmt.Errorf("initial value of object %s is empty", object)
		}
	}
	return nil
}

func relativePath(field, rel string) error {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(strings.ReplaceAll(rel, `\`, "/"), "../") {
		return fmt.Errorf("%s: path %q must be relative to the document and inside its directory", field, rel)
	}
	return nil
}

// Controller returns the declaration for a receiver type name.
func (s Spec) Controller(receiver string) (Controller, bool) {
	for _, c := range s.Controllers {
		if c.Receiver == receiver {
			return c, true
		}
	}
	return Controller{}, false
}

// Selects reports whether a file, given relative to the module root with
// slashes, lies in one of the selected packages: in the directory of a
// plain pattern, or under the directory of a pattern ending in /... .
func (s Spec) Selects(file string) bool {
	dir := "."
	if i := strings.LastIndex(file, "/"); i >= 0 {
		dir = file[:i]
	}
	for _, pattern := range s.Packages {
		tree := strings.HasSuffix(pattern, "/...")
		pd := strings.TrimPrefix(strings.TrimSuffix(pattern, "/..."), "./")
		if pd == "." && tree {
			return true
		}
		if dir == pd || tree && strings.HasPrefix(dir, pd+"/") {
			return true
		}
	}
	return false
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
