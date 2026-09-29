//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoRecordingGitHubClient records which repository the update service asks for.
type repoRecordingGitHubClient struct {
	updateServiceGitHubClientStub
	repos []string
}

func (c *repoRecordingGitHubClient) FetchLatestRelease(ctx context.Context, repo string) (*GitHubRelease, error) {
	c.repos = append(c.repos, repo)
	return c.updateServiceGitHubClientStub.FetchLatestRelease(ctx, repo)
}

func (c *repoRecordingGitHubClient) FetchRecentReleases(ctx context.Context, repo string, perPage int) ([]*GitHubRelease, error) {
	c.repos = append(c.repos, repo)
	return c.updateServiceGitHubClientStub.FetchRecentReleases(ctx, repo, perPage)
}

// This fork publishes its own releases, so checking, updating and rolling back must all read them rather than upstream's, whose binaries lack the fork's changes.
func TestUpdateServiceReadsTheForksReleases(t *testing.T) {
	client := &repoRecordingGitHubClient{
		updateServiceGitHubClientStub: updateServiceGitHubClientStub{
			release:        &GitHubRelease{TagName: "v0.2.10", Name: "v0.2.10"},
			recentReleases: []*GitHubRelease{{TagName: "v0.2.9"}},
		},
	}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.2.10", "release")

	_, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	_, err = svc.ListRollbackVersions(context.Background())
	require.NoError(t, err)

	require.Equal(t, []string{"rbonweb/sub2api", "rbonweb/sub2api"}, client.repos)
}
