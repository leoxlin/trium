package manifest

import (
	"context"
	nativeplugin "github.com/leoxlin/skillets/internal/plugin"
	"github.com/leoxlin/skillets/internal/resource"
)

func loadPlugin(ctx context.Context, manifestDir string, declaration bundleDecl, targets []Target, field string) (resource.Plugin, error) {
	source, files, err := loadBundle(ctx, manifestDir, declaration, field)
	if err != nil {
		return resource.Plugin{}, err
	}
	for _, target := range targets {
		if err := nativeplugin.Validate(declaration.Name, target.Agent, files); err != nil {
			return resource.Plugin{}, invalid(field+".path", err.Error())
		}
	}
	return resource.NewPlugin(declaration.Name, source, files), nil
}
