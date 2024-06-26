package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestConfigFlagNotSetButExists(t *testing.T) {
	for _, name := range []string{
		".config/goreleaser.yml",
		".config/goreleaser.yaml",
		".goreleaser.yml",
		".goreleaser.yaml",
		"goreleaser.yml",
		"goreleaser.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			folder := setup(t)
			require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o755))
			require.NoError(t, os.Rename(
				filepath.Join(folder, "goreleaser.yml"),
				filepath.Join(folder, name),
			))
			proj, err := loadConfig("")
			require.NoError(t, err)
			require.NotEqual(t, config.Project{}, proj)
		})
	}
}

func TestConfigFileDoesntExist(t *testing.T) {
	folder := setup(t)
	err := os.Remove(filepath.Join(folder, "goreleaser.yml"))
	require.NoError(t, err)
	proj, err := loadConfig("")
	require.NoError(t, err)
	require.Equal(t, config.Project{}, proj)
}

func TestConfigFileFromStdin(t *testing.T) {
	folder := setup(t)
	err := os.Remove(filepath.Join(folder, "goreleaser.yml"))
	require.NoError(t, err)
	proj, err := loadConfig("-")
	require.NoError(t, err)
	require.Equal(t, config.Project{}, proj)
}
