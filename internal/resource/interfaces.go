package resource

import "context"

type Resource[T Item] interface {
	Plan(context.Context, []T) (ChangeSet[T], error)
	Apply(context.Context, ChangeSet[T]) (ApplyReport, error)
}

type Skills interface {
	Resource[Skill]
}

type Plugins interface{ Resource[Plugin] }
