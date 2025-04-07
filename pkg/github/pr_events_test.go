package github

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v69/github"
	"github.com/mark3labs/mcp-go/server"
	"github.com/migueleliasweb/go-github-mock/src/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_WaitForPRChecks(t *testing.T) {
	// Verify tool definition once
	mockClient := github.NewClient(nil)
	mockServer := server.NewMCPServer("test", "1.0.0")
	tool, _ := waitForPRChecks(mockServer, mockClient, translations.NullTranslationHelper)

	assert.Equal(t, "wait_for_pr_checks", tool.Name)
	assert.NotEmpty(t, tool.Description)
	assert.Contains(t, tool.InputSchema.Properties, "owner")
	assert.Contains(t, tool.InputSchema.Properties, "repo")
	assert.Contains(t, tool.InputSchema.Properties, "pullNumber")
	assert.Contains(t, tool.InputSchema.Properties, "timeout_seconds")
	assert.ElementsMatch(t, tool.InputSchema.Required, []string{"owner", "repo", "pullNumber"})

	// Setup mock PR for successful PR fetch
	mockPR := &github.PullRequest{
		Number:  github.Ptr(42),
		Title:   github.Ptr("Test PR"),
		HTMLURL: github.Ptr("https://github.com/owner/repo/pull/42"),
		Head: &github.PullRequestBranch{
			SHA: github.Ptr("abcd1234"),
			Ref: github.Ptr("feature-branch"),
		},
	}

	// Setup mock status for success case
	mockSuccessStatus := &github.CombinedStatus{
		State:      github.Ptr("success"),
		TotalCount: github.Ptr(3),
		Statuses: []*github.RepoStatus{
			{
				State:       github.Ptr("success"),
				Context:     github.Ptr("continuous-integration/travis-ci"),
				Description: github.Ptr("Build succeeded"),
				TargetURL:   github.Ptr("https://travis-ci.org/owner/repo/builds/123"),
			},
			{
				State:       github.Ptr("success"),
				Context:     github.Ptr("codecov/patch"),
				Description: github.Ptr("Coverage increased"),
				TargetURL:   github.Ptr("https://codecov.io/gh/owner/repo/pull/42"),
			},
		},
	}

	// Setup mock status for pending case
	mockPendingStatus := &github.CombinedStatus{
		State:      github.Ptr("pending"),
		TotalCount: github.Ptr(3),
		Statuses: []*github.RepoStatus{
			{
				State:       github.Ptr("success"),
				Context:     github.Ptr("continuous-integration/travis-ci"),
				Description: github.Ptr("Build succeeded"),
				TargetURL:   github.Ptr("https://travis-ci.org/owner/repo/builds/123"),
			},
			{
				State:       github.Ptr("pending"),
				Context:     github.Ptr("codecov/patch"),
				Description: github.Ptr("Coverage check in progress"),
				TargetURL:   github.Ptr("https://codecov.io/gh/owner/repo/pull/42"),
			},
		},
	}

	tests := []struct {
		name           string
		mockedClient   *http.Client
		requestArgs    map[string]interface{}
		expectError    bool
		expectProgress bool
		expectedStatus *github.CombinedStatus
		expectedErrMsg string
	}{
		{
			name: "checks completed successfully",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsByOwnerByRepoByPullNumber,
					mockPR,
				),
				mock.WithRequestMatch(
					mock.GetReposCommitsStatusByOwnerByRepoByRef,
					mockSuccessStatus,
				),
			),
			requestArgs: map[string]interface{}{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(42),
			},
			expectError:    false,
			expectProgress: false,
			expectedStatus: mockSuccessStatus,
		},
		{
			name: "checks still pending",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsByOwnerByRepoByPullNumber,
					mockPR,
				),
				mock.WithRequestMatch(
					mock.GetReposCommitsStatusByOwnerByRepoByRef,
					mockPendingStatus,
				),
			),
			requestArgs: map[string]interface{}{
				"owner":           "owner",
				"repo":            "repo",
				"pullNumber":      float64(42),
				"timeout_seconds": float64(0.1), // Very short timeout to force timeout
			},
			expectError:    true,
			expectedErrMsg: "context deadline exceeded",
		},

		{
			name: "PR fetch fails",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatchHandler(
					mock.GetReposPullsByOwnerByRepoByPullNumber,
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"message": "Not Found"}`))
					}),
				),
			),
			requestArgs: map[string]interface{}{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(999),
			},
			expectError:    true,
			expectedErrMsg: "failed to get pull request",
		},
		{
			name: "status fetch fails",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsByOwnerByRepoByPullNumber,
					mockPR,
				),
				mock.WithRequestMatchHandler(
					mock.GetReposCommitsStatusByOwnerByRepoByRef,
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"message": "Not Found"}`))
					}),
				),
			),
			requestArgs: map[string]interface{}{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(42),
			},
			expectError:    true,
			expectedErrMsg: "failed to get combined status",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup client with mock
			client := github.NewClient(tc.mockedClient)
			mockServer := server.NewMCPServer("test", "1.0.0")
			_, handler := waitForPRChecks(mockServer, client, translations.NullTranslationHelper)

			// Create call request
			request := createMCPRequest(tc.requestArgs)

			// Create a context with timeout to prevent tests from running too long
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			// Call handler
			result, err := handler(ctx, request)

			// Verify results
			if tc.expectError {
				if err != nil {
					assert.Contains(t, err.Error(), tc.expectedErrMsg)
					return
				}
				// Error might be in the result instead
				textContent := getTextResult(t, result)
				assert.Contains(t, textContent.Text, tc.expectedErrMsg)
				return
			}

			require.NoError(t, err)
			textContent := getTextResult(t, result)

			// For completed responses, unmarshal and verify the status
			var returnedStatus github.CombinedStatus
			err = json.Unmarshal([]byte(textContent.Text), &returnedStatus)
			require.NoError(t, err)
			assert.Equal(t, *tc.expectedStatus.State, *returnedStatus.State)
			assert.Equal(t, *tc.expectedStatus.TotalCount, *returnedStatus.TotalCount)
			assert.Len(t, returnedStatus.Statuses, len(tc.expectedStatus.Statuses))
		})
	}
}

func Test_WaitForPRReview(t *testing.T) {
	// Verify tool definition once
	mockClient := github.NewClient(nil)
	mockServer := server.NewMCPServer("test", "1.0.0")
	tool, _ := waitForPRReview(mockServer, mockClient, translations.NullTranslationHelper)

	assert.Equal(t, "wait_for_pr_review", tool.Name)
	assert.NotEmpty(t, tool.Description)
	assert.Contains(t, tool.InputSchema.Properties, "owner")
	assert.Contains(t, tool.InputSchema.Properties, "repo")
	assert.Contains(t, tool.InputSchema.Properties, "pullNumber")
	assert.Contains(t, tool.InputSchema.Properties, "last_review_id")
	assert.Contains(t, tool.InputSchema.Properties, "timeout_seconds")
	assert.ElementsMatch(t, tool.InputSchema.Required, []string{"owner", "repo", "pullNumber"})

	// Setup mock PR reviews for success case
	mockReviews := []*github.PullRequestReview{
		{
			ID:      github.Ptr(int64(201)),
			State:   github.Ptr("APPROVED"),
			Body:    github.Ptr("LGTM"),
			HTMLURL: github.Ptr("https://github.com/owner/repo/pull/42#pullrequestreview-201"),
			User: &github.User{
				Login: github.Ptr("approver"),
			},
			CommitID:    github.Ptr("abcdef123456"),
			SubmittedAt: &github.Timestamp{Time: time.Now().Add(-24 * time.Hour)},
		},
		{
			ID:      github.Ptr(int64(202)),
			State:   github.Ptr("CHANGES_REQUESTED"),
			Body:    github.Ptr("Please address the following issues"),
			HTMLURL: github.Ptr("https://github.com/owner/repo/pull/42#pullrequestreview-202"),
			User: &github.User{
				Login: github.Ptr("reviewer"),
			},
			CommitID:    github.Ptr("abcdef123456"),
			SubmittedAt: &github.Timestamp{Time: time.Now().Add(-12 * time.Hour)},
		},
		{
			ID:      github.Ptr(int64(203)),
			State:   github.Ptr("APPROVED"),
			Body:    github.Ptr("Now it looks good!"),
			HTMLURL: github.Ptr("https://github.com/owner/repo/pull/42#pullrequestreview-203"),
			User: &github.User{
				Login: github.Ptr("reviewer"),
			},
			CommitID:    github.Ptr("abcdef789012"),
			SubmittedAt: &github.Timestamp{Time: time.Now().Add(-1 * time.Hour)},
		},
	}

	tests := []struct {
		name           string
		mockedClient   *http.Client
		requestArgs    map[string]interface{}
		expectError    bool
		expectProgress bool
		expectedReview *github.PullRequestReview
		expectedErrMsg string
	}{
		{
			name: "new review found",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsReviewsByOwnerByRepoByPullNumber,
					mockReviews,
				),
			),
			requestArgs: map[string]interface{}{
				"owner":          "owner",
				"repo":           "repo",
				"pullNumber":     float64(42),
				"last_review_id": float64(202),
			},
			expectError:    false,
			expectProgress: false,
			expectedReview: mockReviews[2], // The newest review (ID 203)
		},
		{
			name: "no new reviews",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsReviewsByOwnerByRepoByPullNumber,
					mockReviews,
				),
			),
			requestArgs: map[string]interface{}{
				"owner":           "owner",
				"repo":            "repo",
				"pullNumber":      float64(42),
				"last_review_id":  float64(203), // Already have the latest review
				"timeout_seconds": float64(0.1), // Very short timeout to force timeout
			},
			expectError:    true,
			expectedErrMsg: "context deadline exceeded",
		},

		{
			name: "reviews fetch fails",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatchHandler(
					mock.GetReposPullsReviewsByOwnerByRepoByPullNumber,
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"message": "Not Found"}`))
					}),
				),
			),
			requestArgs: map[string]interface{}{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(999),
			},
			expectError:    true,
			expectedErrMsg: "failed to get pull request reviews",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup client with mock
			client := github.NewClient(tc.mockedClient)
			mockServer := server.NewMCPServer("test", "1.0.0")
			_, handler := waitForPRReview(mockServer, client, translations.NullTranslationHelper)

			// Create call request
			request := createMCPRequest(tc.requestArgs)

			// Create a context with timeout to prevent tests from running too long
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			// Call handler
			result, err := handler(ctx, request)

			// Verify results
			if tc.expectError {
				if err != nil {
					assert.Contains(t, err.Error(), tc.expectedErrMsg)
					return
				}
				// Error might be in the result instead
				textContent := getTextResult(t, result)
				assert.Contains(t, textContent.Text, tc.expectedErrMsg)
				return
			}

			require.NoError(t, err)
			textContent := getTextResult(t, result)

			// For completed responses, unmarshal and verify the review
			var returnedReview github.PullRequestReview
			err = json.Unmarshal([]byte(textContent.Text), &returnedReview)
			require.NoError(t, err)
			assert.Equal(t, *tc.expectedReview.ID, *returnedReview.ID)
			assert.Equal(t, *tc.expectedReview.State, *returnedReview.State)
			assert.Equal(t, *tc.expectedReview.Body, *returnedReview.Body)
			assert.Equal(t, *tc.expectedReview.User.Login, *returnedReview.User.Login)
		})
	}
}
