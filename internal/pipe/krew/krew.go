// Package krew implements Piper and Publisher, providing krew plugin manifest
// creation and upload to a repository (aka krew plugin index).
//
//nolint:tagliatelle
package krew

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/caarlos0/log"
	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/client"
	"github.com/goreleaser/goreleaser/v2/internal/commitauthor"
	"github.com/goreleaser/goreleaser/v2/internal/pipe"
	"github.com/goreleaser/goreleaser/v2/internal/tmpl"
	"github.com/goreleaser/goreleaser/v2/internal/yaml"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
)

const (
	krewConfigExtra = "KrewConfig"
	manifestsFolder = "plugins"
	kind            = "Plugin"
	apiVersion      = "krew.googlecontainertools.github.com/v1alpha2"
)

var ErrNoArchivesFound = errors.New("no archives found")

// Pipe for krew manifest deployment.
type Pipe struct{}

func (Pipe) String() string                 { return "krew plugin manifest" }
func (Pipe) ContinueOnError() bool          { return true }
func (Pipe) Skip(ctx *context.Context) bool { return len(ctx.Config.Krews) == 0 }

func (Pipe) Default(ctx *context.Context) error {
	for i := range ctx.Config.Krews {
		krew := &ctx.Config.Krews[i]

		krew.CommitAuthor = commitauthor.Default(krew.CommitAuthor)
		if krew.CommitMessageTemplate == "" {
			krew.CommitMessageTemplate = "Krew manifest update for {{ .ProjectName }} version {{ .Tag }}"
		}
		if krew.Name == "" {
			krew.Name = ctx.Config.ProjectName
		}
		if krew.Goamd64 == "" {
			krew.Goamd64 = "v1"
		}
	}

	return nil
}

func (Pipe) Run(ctx *context.Context) error {
	cli, err := client.NewReleaseClient(ctx)
	if err != nil {
		return err
	}

	return runAll(ctx, cli)
}

func runAll(ctx *context.Context, cli client.ReleaseURLTemplater) error {
	for _, krew := range ctx.Config.Krews {
		err := doRun(ctx, krew, cli)
		if err != nil {
			return err
		}
	}
	return nil
}

func doRun(ctx *context.Context, krew config.Krew, cl client.ReleaseURLTemplater) error {
	if krew.Name == "" {
		return pipe.Skip("krew: manifest name is not set")
	}
	if krew.Description == "" {
		return errors.New("krew: manifest description is not set")
	}
	if krew.ShortDescription == "" {
		return errors.New("krew: manifest short description is not set")
	}

	filters := []artifact.Filter{
		artifact.Or(
			artifact.ByGoos("darwin"),
			artifact.ByGoos("linux"),
			artifact.ByGoos("windows"),
		),
		artifact.Or(
			artifact.And(
				artifact.ByGoarch("amd64"),
				artifact.ByGoamd64(krew.Goamd64),
			),
			artifact.ByGoarch("arm64"),
			artifact.ByGoarch("all"),
			artifact.And(
				artifact.ByGoarch("arm"),
				artifact.ByGoarm(krew.Goarm),
			),
		),
		artifact.ByType(artifact.UploadableArchive),
		artifact.OnlyReplacingUnibins,
	}
	if len(krew.IDs) > 0 {
		filters = append(filters, artifact.ByIDs(krew.IDs...))
	}

	archives := ctx.Artifacts.Filter(artifact.And(filters...)).List()
	if len(archives) == 0 {
		return ErrNoArchivesFound
	}

	krew, err := templateFields(ctx, krew)
	if err != nil {
		return err
	}

	content, err := buildmanifest(ctx, krew, cl, archives)
	if err != nil {
		return err
	}

	filename := krew.Name + ".yaml"
	yamlPath := filepath.Join(ctx.Config.Dist, "krew", filename)
	if err := os.MkdirAll(filepath.Dir(yamlPath), 0o755); err != nil {
		return err
	}
	log.WithField("manifest", yamlPath).Info("writing")
	if err := os.WriteFile(yamlPath, []byte("# This file was generated by GoReleaser. DO NOT EDIT.\n"+content), 0o644); err != nil { //nolint:gosec
		return fmt.Errorf("failed to write krew manifest: %w", err)
	}

	ctx.Artifacts.Add(&artifact.Artifact{
		Name: filename,
		Path: yamlPath,
		Type: artifact.KrewPluginManifest,
		Extra: map[string]any{
			krewConfigExtra: krew,
		},
	})

	return nil
}

func templateFields(ctx *context.Context, krew config.Krew) (config.Krew, error) {
	t := tmpl.New(ctx)

	if err := t.ApplyAll(
		&krew.Name,
		&krew.Homepage,
		&krew.Description,
		&krew.Caveats,
		&krew.ShortDescription,
	); err != nil {
		return config.Krew{}, err
	}

	return krew, nil
}

func buildmanifest(
	ctx *context.Context,
	krew config.Krew,
	client client.ReleaseURLTemplater,
	artifacts []*artifact.Artifact,
) (string, error) {
	data, err := manifestFor(ctx, krew, client, artifacts)
	if err != nil {
		return "", err
	}
	return doBuildManifest(data)
}

func doBuildManifest(data Manifest) (string, error) {
	out, err := yaml.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("krew: failed to marshal yaml: %w", err)
	}
	return string(out), nil
}

func manifestFor(
	ctx *context.Context,
	cfg config.Krew,
	cl client.ReleaseURLTemplater,
	artifacts []*artifact.Artifact,
) (Manifest, error) {
	result := Manifest{
		APIVersion: apiVersion,
		Kind:       kind,
		Metadata: Metadata{
			Name: cfg.Name,
		},
		Spec: Spec{
			Homepage:         cfg.Homepage,
			Version:          "v" + ctx.Version,
			ShortDescription: cfg.ShortDescription,
			Description:      cfg.Description,
			Caveats:          cfg.Caveats,
		},
	}

	for _, art := range artifacts {
		sum, err := art.Checksum("sha256")
		if err != nil {
			return result, err
		}

		if cfg.URLTemplate == "" {
			url, err := cl.ReleaseURLTemplate(ctx)
			if err != nil {
				return result, err
			}
			cfg.URLTemplate = url
		}
		url, err := tmpl.New(ctx).WithArtifact(art).Apply(cfg.URLTemplate)
		if err != nil {
			return result, err
		}

		goarch := []string{art.Goarch}
		if art.Goarch == "all" {
			goarch = []string{"amd64", "arm64"}
		}

		for _, arch := range goarch {
			bins := artifact.MustExtra[[]string](*art, artifact.ExtraBinaries)
			if len(bins) != 1 {
				return result, fmt.Errorf("krew: only one binary per archive allowed, got %d on %q", len(bins), art.Name)
			}
			result.Spec.Platforms = append(result.Spec.Platforms, Platform{
				Bin:    bins[0],
				URI:    url,
				Sha256: sum,
				Selector: Selector{
					MatchLabels: MatchLabels{
						Os:   art.Goos,
						Arch: arch,
					},
				},
			})
		}
	}

	slices.SortFunc(result.Spec.Platforms, func(a, b Platform) int {
		return -cmp.Compare(a.URI, b.URI)
	})

	return result, nil
}

// Publish krew manifest.
func (Pipe) Publish(ctx *context.Context) error {
	cli, err := client.New(ctx)
	if err != nil {
		return err
	}
	return publishAll(ctx, cli)
}

func publishAll(ctx *context.Context, cli client.Client) error {
	skips := pipe.SkipMemento{}
	for _, manifest := range ctx.Artifacts.Filter(artifact.ByType(artifact.KrewPluginManifest)).List() {
		err := doPublish(ctx, manifest, cli)
		if err != nil && pipe.IsSkip(err) {
			skips.Remember(err)
			continue
		}
		if err != nil {
			return err
		}
	}
	return skips.Evaluate()
}

func doPublish(ctx *context.Context, manifest *artifact.Artifact, cl client.Client) error {
	cfg := artifact.MustExtra[config.Krew](*manifest, krewConfigExtra)
	if strings.TrimSpace(cfg.SkipUpload) == "true" {
		return pipe.Skip("krews.skip_upload is set")
	}

	if strings.TrimSpace(cfg.SkipUpload) == "auto" && ctx.Semver.Prerelease != "" {
		return pipe.Skip("prerelease detected with 'auto' upload, skipping krew publish")
	}

	ref, err := client.TemplateRef(tmpl.New(ctx).Apply, cfg.Repository)
	if err != nil {
		return err
	}
	cfg.Repository = ref
	repo := client.RepoFromRef(cfg.Repository)
	gpath := buildManifestPath(manifestsFolder, manifest.Name)

	msg, err := tmpl.New(ctx).Apply(cfg.CommitMessageTemplate)
	if err != nil {
		return err
	}

	author, err := commitauthor.Get(ctx, cfg.CommitAuthor)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(manifest.Path)
	if err != nil {
		return err
	}

	if cfg.Repository.Git.URL != "" {
		return client.NewGitUploadClient(repo.Branch).
			CreateFile(ctx, author, repo, content, gpath, msg)
	}

	cl, err = client.NewIfToken(ctx, cl, cfg.Repository.Token)
	if err != nil {
		return err
	}

	base := client.Repo{
		Name:   cfg.Repository.PullRequest.Base.Name,
		Owner:  cfg.Repository.PullRequest.Base.Owner,
		Branch: cfg.Repository.PullRequest.Base.Branch,
	}

	// try to sync branch
	fscli, ok := cl.(client.ForkSyncer)
	if ok && cfg.Repository.PullRequest.Enabled {
		if err := fscli.SyncFork(ctx, repo, base); err != nil {
			log.WithError(err).Warn("could not sync fork")
		}
	}

	if err := cl.CreateFile(ctx, author, repo, content, gpath, msg); err != nil {
		return err
	}

	if !cfg.Repository.PullRequest.Enabled {
		log.Debug("krews.pull_request disabled")
		return nil
	}

	log.Info("krews.pull_request enabled, creating a PR")
	pcl, ok := cl.(client.PullRequestOpener)
	if !ok {
		return errors.New("client does not support pull requests")
	}

	return pcl.OpenPullRequest(ctx, base, repo, msg, cfg.Repository.PullRequest.Draft)
}

func buildManifestPath(folder, filename string) string {
	return path.Join(folder, filename)
}

type Manifest struct {
	APIVersion string   `yaml:"apiVersion,omitempty"`
	Kind       string   `yaml:"kind,omitempty"`
	Metadata   Metadata `yaml:"metadata,omitempty"`
	Spec       Spec     `yaml:"spec,omitempty"`
}

type Metadata struct {
	Name string `yaml:"name,omitempty"`
}

type MatchLabels struct {
	Os   string `yaml:"os,omitempty"`
	Arch string `yaml:"arch,omitempty"`
}

type Selector struct {
	MatchLabels MatchLabels `yaml:"matchLabels,omitempty"`
}

type Platform struct {
	Bin      string   `yaml:"bin,omitempty"`
	URI      string   `yaml:"uri,omitempty"`
	Sha256   string   `yaml:"sha256,omitempty"`
	Selector Selector `yaml:"selector,omitempty"`
}

type Spec struct {
	Version          string     `yaml:"version,omitempty"`
	Platforms        []Platform `yaml:"platforms,omitempty"`
	ShortDescription string     `yaml:"shortDescription,omitempty"`
	Homepage         string     `yaml:"homepage,omitempty"`
	Caveats          string     `yaml:"caveats,omitempty"`
	Description      string     `yaml:"description,omitempty"`
}
