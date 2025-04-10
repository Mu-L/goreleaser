// Package linkedin announces releases on LinkedIn.
package linkedin

import (
	"fmt"

	"github.com/caarlos0/env/v11"
	"github.com/caarlos0/log"
	"github.com/goreleaser/goreleaser/v2/internal/tmpl"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
)

const defaultMessageTemplate = `{{ .ProjectName }} {{ .Tag }} is out! Check it out at {{ .ReleaseURL }}`

type Pipe struct{}

func (Pipe) String() string { return "linkedin" }
func (Pipe) Skip(ctx *context.Context) (bool, error) {
	enable, err := tmpl.New(ctx).Bool(ctx.Config.Announce.LinkedIn.Enabled)
	return !enable, err
}

type Config struct {
	AccessToken string `env:"LINKEDIN_ACCESS_TOKEN,notEmpty"`
}

func (Pipe) Default(ctx *context.Context) error {
	if ctx.Config.Announce.LinkedIn.MessageTemplate == "" {
		ctx.Config.Announce.LinkedIn.MessageTemplate = defaultMessageTemplate
	}

	return nil
}

func (Pipe) Announce(ctx *context.Context) error {
	message, err := tmpl.New(ctx).Apply(ctx.Config.Announce.LinkedIn.MessageTemplate)
	if err != nil {
		return fmt.Errorf("linkedin: %w", err)
	}

	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return fmt.Errorf("linkedin: %w", err)
	}

	c, err := createLinkedInClient(oauthClientConfig{
		Context:     ctx,
		AccessToken: cfg.AccessToken,
	})
	if err != nil {
		return fmt.Errorf("linkedin: %w", err)
	}

	url, err := c.Share(ctx, message)
	if err != nil {
		return fmt.Errorf("linkedin: %w", err)
	}

	log.Infof("The text post is available at: %s\n", url)

	return nil
}
