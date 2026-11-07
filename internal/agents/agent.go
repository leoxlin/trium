package agents

import (
	"context"

	"github.com/leoxlin/skillets/internal/resource"
)

type Resources struct {
	Skills resource.Skills
}

type Agent interface {
	Name() string
	Resources() Resources
	Lock(context.Context) (unlock func() error, err error)
}
