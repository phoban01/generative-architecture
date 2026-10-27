package typed

import "context"

// Job nests a list of structs, a struct and a type that contains itself.
type Job struct {
	Spec JobSpec `json:"spec"`
}

type JobSpec struct {
	Containers []Container `json:"containers,omitempty"`
	Template   Template    `json:"template"`
	Tree       Tree        `json:"tree"`
}

type Container struct {
	Name  string            `json:"name"`
	Image string            `json:"image"`
	Args  []string          `json:"args,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
}

type Template struct {
	Image string `json:"image"`
}

type Tree struct {
	Children []Tree `json:"children,omitempty"`
}

func (r *Reconciler) create(ctx context.Context, job *Job) error { return nil }
func (r *Reconciler) image(ctx context.Context) string           { return "" }

// Literals assigns a slice literal and a struct literal to fields.
func (r *Reconciler) Literals(ctx context.Context, name string) (Result, error) {
	job := &Job{}
	job.Spec.Containers = []Container{{Name: "main", Image: name, Args: []string{"run", name}}}
	job.Spec.Template = Template{Image: name}
	return Result{}, r.create(ctx, job)
}

// CallInLiteral calls a method inside a literal.
func (r *Reconciler) CallInLiteral(ctx context.Context) (Result, error) {
	job := &Job{}
	job.Spec.Containers = []Container{{Name: "main", Image: r.image(ctx)}}
	return Result{}, r.create(ctx, job)
}

// RecursiveLiteral assigns a literal of a type that contains itself.
func (r *Reconciler) RecursiveLiteral(ctx context.Context) (Result, error) {
	job := &Job{}
	job.Spec.Tree = Tree{}
	return Result{}, r.create(ctx, job)
}
