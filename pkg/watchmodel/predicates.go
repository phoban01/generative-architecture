package watchmodel

import (
	"fmt"
	"strings"

	"generative-architecture/pkg/ir"
)

// GenerationVar names the generation counter of the object of resource.
func GenerationVar(resource ir.GVK) string { return lowerFirst(resource.Kind) + "Generation" }

// ResourceVersionVar names the resource-version counter of the object of
// resource.
func ResourceVersionVar(resource ir.GVK) string {
	return lowerFirst(resource.Kind) + "ResourceVersion"
}

// seen names the copy of a counter or field that the last delivered event
// left behind: the ObjectOld of the next update.
func seen(v string) string { return v + "Seen" }

// passes returns the condition under which the event of r passes every
// predicate of w. Predicates without a model fail closed.
func passes(r *resource, w ir.Watch) (string, error) {
	var all []string
	for _, p := range w.Predicates {
		source := p.Kind
		switch p.Kind {
		case "and", "or", "not":
			source = p.Args["expression"]
			if source == "" {
				return "", diagnose(w, "predicate", fmt.Sprintf("%s predicate has no expression", p.Kind))
			}
		}
		c, err := parsePredicate(r, source)
		if err != nil {
			return "", diagnose(w, "predicate", err.Error())
		}
		if c != "true" {
			all = append(all, "("+c+")")
		}
	}
	if len(all) == 0 {
		return "true", nil
	}
	return strings.Join(all, " && "), nil
}

// parsePredicate lowers a predicate summary such as
// or(generationChanged, not(resourceVersionChanged)) to a condition.
func parsePredicate(r *resource, source string) (string, error) {
	p := &predicateParser{r: r, rest: strings.TrimSpace(source)}
	c, err := p.parse()
	if err != nil {
		return "", err
	}
	if p.rest != "" {
		return "", fmt.Errorf("unexpected %q in predicate %s", p.rest, source)
	}
	return c, nil
}

type predicateParser struct {
	r    *resource
	rest string
}

func (p *predicateParser) parse() (string, error) {
	i := strings.IndexAny(p.rest, "(),")
	if i < 0 {
		i = len(p.rest)
	}
	name := strings.TrimSpace(p.rest[:i])
	p.rest = strings.TrimSpace(p.rest[i:])
	if !strings.HasPrefix(p.rest, "(") {
		return p.leaf(name)
	}
	if name != "and" && name != "or" && name != "not" {
		return "", fmt.Errorf("unsupported predicate operator %q", name)
	}
	p.rest = strings.TrimSpace(p.rest[1:])
	var args []string
	for {
		c, err := p.parse()
		if err != nil {
			return "", err
		}
		args = append(args, "("+c+")")
		if strings.HasPrefix(p.rest, ",") {
			p.rest = strings.TrimSpace(p.rest[1:])
			continue
		}
		if !strings.HasPrefix(p.rest, ")") {
			return "", fmt.Errorf("unterminated %s predicate", name)
		}
		p.rest = strings.TrimSpace(p.rest[1:])
		break
	}
	switch name {
	case "not":
		if len(args) != 1 {
			return "", fmt.Errorf("not predicate takes one argument")
		}
		return "!" + args[0], nil
	case "and":
		return strings.Join(args, " && "), nil
	}
	return strings.Join(args, " || "), nil
}

// leaf lowers a standard predicate. Both pass create and delete events and
// compare the object with the last delivered copy on update.
func (p *predicateParser) leaf(name string) (string, error) {
	ev := EventVar(p.r.gvk)
	var counter string
	switch name {
	case "generationChanged":
		counter = GenerationVar(p.r.gvk)
	case "resourceVersionChanged":
		counter = ResourceVersionVar(p.r.gvk)
	case "":
		return "", fmt.Errorf("empty predicate")
	default:
		return "", fmt.Errorf("predicate %s is not modelled", name)
	}
	return fmt.Sprintf(`%s != "update" || %s != %s`, ev, counter, seen(counter)), nil
}
