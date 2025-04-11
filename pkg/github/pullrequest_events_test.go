package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v69/github"
	"github.com/mark3labs/mcp-go/server"
	"github.com/migueleliasweb/go-github-mock/src/mock"
	"github.com/shurcooL/githubv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_WaitForPullRequestChecks(t *testing.T) {
	// Verify tool definition once
	mockClient := github.NewClient(nil)
	mockServer := server.NewMCPServer("test", "1.0.0")
	tool, _ := waitForPullRequestChecks(mockServer, mockClient, translations.NullTranslationHelper)

	assert.Equal(t, "wait_for_pullrequest_checks", tool.Name)
	assert.NotEmpty(t, tool.Description)
	assert.Contains(t, tool.InputSchema.Properties, "owner")
	assert.Contains(t, tool.InputSchema.Properties, "repo")
	assert.Contains(t, tool.InputSchema.Properties, "pullNumber")
	assert.ElementsMatch(t, tool.InputSchema.Required, []string{"owner", "repo", "pullNumber"})
	// timeout_seconds parameter has been removed

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

	// Setup mock check runs for completed case
	mockCompletedCheckRuns := &github.ListCheckRunsResults{
		Total: github.Ptr(2),
		CheckRuns: []*github.CheckRun{
			{
				ID:         github.Ptr(int64(1)),
				Name:       github.Ptr("travis-ci"),
				Status:     github.Ptr("completed"),
				Conclusion: github.Ptr("success"),
				HTMLURL:    github.Ptr("https://travis-ci.org/owner/repo/builds/123"),
				Output: &github.CheckRunOutput{
					Title:   github.Ptr("Build succeeded"),
					Summary: github.Ptr("All tests passed"),
				},
			},
			{
				ID:         github.Ptr(int64(2)),
				Name:       github.Ptr("codecov"),
				Status:     github.Ptr("completed"),
				Conclusion: github.Ptr("success"),
				HTMLURL:    github.Ptr("https://codecov.io/gh/owner/repo/pull/42"),
				Output: &github.CheckRunOutput{
					Title:   github.Ptr("Coverage increased"),
					Summary: github.Ptr("Coverage is now at 85%"),
				},
			},
		},
	}

	// Setup mock check runs for in-progress case
	mockInProgressCheckRuns := &github.ListCheckRunsResults{
		Total: github.Ptr(2),
		CheckRuns: []*github.CheckRun{
			{
				ID:         github.Ptr(int64(1)),
				Name:       github.Ptr("travis-ci"),
				Status:     github.Ptr("completed"),
				Conclusion: github.Ptr("success"),
				HTMLURL:    github.Ptr("https://travis-ci.org/owner/repo/builds/123"),
				Output: &github.CheckRunOutput{
					Title:   github.Ptr("Build succeeded"),
					Summary: github.Ptr("All tests passed"),
				},
			},
			{
				ID:      github.Ptr(int64(2)),
				Name:    github.Ptr("codecov"),
				Status:  github.Ptr("in_progress"),
				HTMLURL: github.Ptr("https://codecov.io/gh/owner/repo/pull/42"),
				Output: &github.CheckRunOutput{
					Title:   github.Ptr("Coverage in progress"),
					Summary: github.Ptr("Calculating coverage"),
				},
			},
		},
	}

	// Setup mock empty check runs
	mockEmptyCheckRuns := &github.ListCheckRunsResults{
		Total:     github.Ptr(0),
		CheckRuns: []*github.CheckRun{},
	}

	tests := []struct {
		name           string
		mockedClient   *http.Client
		requestArgs    map[string]any
		expectError    bool
		expectProgress bool
		expectedStatus *github.ListCheckRunsResults
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
					mock.GetReposCommitsCheckRunsByOwnerByRepoByRef,
					mockCompletedCheckRuns,
				),
			),
			requestArgs: map[string]any{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(42),
			},
			expectError:    false,
			expectProgress: false,
			expectedStatus: mockCompletedCheckRuns,
		},
		{
			name: "checks still pending",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsByOwnerByRepoByPullNumber,
					mockPR,
				),
				mock.WithRequestMatch(
					mock.GetReposCommitsCheckRunsByOwnerByRepoByRef,
					mockInProgressCheckRuns,
				),
			),
			requestArgs: map[string]any{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(42),
			},
			expectError:    true,
			expectedErrMsg: "Timeout waiting for",
		},
		{
			name: "no check runs configured",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsByOwnerByRepoByPullNumber,
					mockPR,
				),
				mock.WithRequestMatch(
					mock.GetReposCommitsCheckRunsByOwnerByRepoByRef,
					mockEmptyCheckRuns,
				),
			),
			requestArgs: map[string]any{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(42),
			},
			expectError:    false,
			expectProgress: false,
			expectedStatus: mockEmptyCheckRuns,
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
			requestArgs: map[string]any{
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
			requestArgs: map[string]any{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(42),
			},
			expectError:    true,
			expectedErrMsg: "failed to get check runs",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup client with mock
			client := github.NewClient(tc.mockedClient)
			mockServer := server.NewMCPServer("test", "1.0.0")
			_, handler := waitForPullRequestChecks(mockServer, client, translations.NullTranslationHelper)

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

			// For completed responses, unmarshal and verify the check runs
			var returnedCheckRuns github.ListCheckRunsResults
			err = json.Unmarshal([]byte(textContent.Text), &returnedCheckRuns)
			require.NoError(t, err)
			assert.Equal(t, *tc.expectedStatus.Total, *returnedCheckRuns.Total)
			assert.Len(t, returnedCheckRuns.CheckRuns, len(tc.expectedStatus.CheckRuns))
			// Verify the first check run
			if len(returnedCheckRuns.CheckRuns) > 0 && len(tc.expectedStatus.CheckRuns) > 0 {
				assert.Equal(t, *tc.expectedStatus.CheckRuns[0].Name, *returnedCheckRuns.CheckRuns[0].Name)
				assert.Equal(t, *tc.expectedStatus.CheckRuns[0].Status, *returnedCheckRuns.CheckRuns[0].Status)
			}
		})
	}
}

// mockGraphQLClient is a mock implementation of GraphQLQuerier for testing
type mockGraphQLClient struct {
	QueryFunc func(ctx context.Context, q any, variables map[string]any) error
}

// Query implements the GraphQLQuerier interface
func (m *mockGraphQLClient) Query(ctx context.Context, q any, variables map[string]any) error {
	if m.QueryFunc != nil {
		return m.QueryFunc(ctx, q, variables)
	}
	return nil
}

func Test_WaitForPullRequestReview(t *testing.T) {
	// Verify tool definition once
	mockClient := github.NewClient(nil)
	// Create a mock githubv4.Client
	mockGQLClient := &githubv4.Client{}
	mockServer := server.NewMCPServer("test", "1.0.0")
	tool, _ := waitForPullRequestReview(mockServer, mockClient, mockGQLClient, translations.NullTranslationHelper)

	assert.Equal(t, "wait_for_pullrequest_review", tool.Name)
	assert.NotEmpty(t, tool.Description)
	assert.Contains(t, tool.InputSchema.Properties, "owner")
	assert.Contains(t, tool.InputSchema.Properties, "repo")
	assert.Contains(t, tool.InputSchema.Properties, "pullNumber")
	// last_review_id parameter has been removed
	assert.ElementsMatch(t, tool.InputSchema.Required, []string{"owner", "repo", "pullNumber"})
	// timeout_seconds parameter has been removed

	// Setup mock PR for tests
	mockPullRequest := &github.PullRequest{
		Number: github.Ptr(42),
		Title:  github.Ptr("Test PR"),
		User: &github.User{
			Login: github.Ptr("author"),
		},
	}

	tests := []struct {
		name           string
		mockedClient   *http.Client
		mockedGQLFunc  func(ctx context.Context, q any, variables map[string]any) error
		requestArgs    map[string]any
		expectError    bool
		expectProgress bool
		expectedResult any
		expectedErrMsg string
	}{
		// Test case 1: Reviewer activity more recent than author
		{
			name: "reviewer activity more recent than author",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsByOwnerByRepoByPullNumber,
					mockPullRequest,
				),
			),
			mockedGQLFunc: func(_ context.Context, q any, _ map[string]any) error {
				// Set up the query result with reviewer activity more recent than author
				query, ok := q.(*PullRequestActivityQuery)
				if !ok {
					return fmt.Errorf("unexpected query type")
				}

				// Set author info
				query.Repository.PullRequest.Author.Login = "author"

				// Add author commit (older)
				authorCommit := struct {
					Commit struct {
						Author struct {
							Email githubv4.String
						}
						CommittedDate githubv4.DateTime
					}
				}{}
				authorCommit.Commit.Author.Email = "author@example.com"
				authorCommit.Commit.CommittedDate.Time = time.Now().Add(-2 * time.Hour)
				query.Repository.PullRequest.Commits.Nodes = append(query.Repository.PullRequest.Commits.Nodes, authorCommit)

				// Add reviewer review (more recent)
				// Create a review node with the same structure as in the query
				reviewNode := struct {
					ViewerDidAuthor githubv4.Boolean
					State           githubv4.String
					UpdatedAt       githubv4.DateTime
					Comments        struct {
						TotalCount githubv4.Int
						Nodes      []struct {
							ViewerDidAuthor githubv4.Boolean
							BodyText        githubv4.String
							UpdatedAt       githubv4.DateTime
						}
					} `graphql:"comments(first: 100)"`
				}{}
				reviewNode.ViewerDidAuthor = false
				reviewNode.State = "APPROVED"
				reviewNode.UpdatedAt.Time = time.Now().Add(-1 * time.Hour)
				query.Repository.PullRequest.Reviews.Nodes = append(query.Repository.PullRequest.Reviews.Nodes, reviewNode)

				return nil
			},
			requestArgs: map[string]any{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(42),
			},
			expectError:    false,
			expectProgress: false,
			expectedResult: &ActivityResult{},
		},
		{
			name: "author activity more recent than reviewer",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsByOwnerByRepoByPullNumber,
					mockPullRequest,
				),
			),
			mockedGQLFunc: func(_ context.Context, q any, _ map[string]any) error {
				// Set up the query result with author activity more recent than reviewer
				query, ok := q.(*PullRequestActivityQuery)
				if !ok {
					return fmt.Errorf("unexpected query type")
				}

				// Set author info
				query.Repository.PullRequest.Author.Login = "author"

				// Add reviewer review (older)
				// Create a review node with the same structure as in the query
				reviewNode := struct {
					ViewerDidAuthor githubv4.Boolean
					State           githubv4.String
					UpdatedAt       githubv4.DateTime
					Comments        struct {
						TotalCount githubv4.Int
						Nodes      []struct {
							ViewerDidAuthor githubv4.Boolean
							BodyText        githubv4.String
							UpdatedAt       githubv4.DateTime
						}
					} `graphql:"comments(first: 100)"`
				}{}
				reviewNode.ViewerDidAuthor = false
				reviewNode.State = "APPROVED"
				reviewNode.UpdatedAt.Time = time.Now().Add(-2 * time.Hour)
				query.Repository.PullRequest.Reviews.Nodes = append(query.Repository.PullRequest.Reviews.Nodes, reviewNode)

				// Add author commit (more recent)
				authorCommit := struct {
					Commit struct {
						Author struct {
							Email githubv4.String
						}
						CommittedDate githubv4.DateTime
					}
				}{}
				authorCommit.Commit.Author.Email = "author@example.com"
				authorCommit.Commit.CommittedDate.Time = time.Now().Add(-1 * time.Hour)
				query.Repository.PullRequest.Commits.Nodes = append(query.Repository.PullRequest.Commits.Nodes, authorCommit)

				return nil
			},
			requestArgs: map[string]any{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(42),
			},
			expectError:    true,
			expectedErrMsg: "Timeout waiting for",
		},

		{
			name: "GraphQL query fails",
			mockedClient: mock.NewMockedHTTPClient(
				mock.WithRequestMatch(
					mock.GetReposPullsByOwnerByRepoByPullNumber,
					mockPullRequest,
				),
			),
			mockedGQLFunc: func(_ context.Context, _ any, _ map[string]any) error {
				return fmt.Errorf("GraphQL query failed")
			},
			requestArgs: map[string]any{
				"owner":      "owner",
				"repo":       "repo",
				"pullNumber": float64(42),
			},
			expectError:    true,
			expectedErrMsg: "failed to execute GraphQL query",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup client with mock
			client := github.NewClient(tc.mockedClient)
			// Create a mock githubv4.Client for each test case
			mockGQLClient := &mockGraphQLClient{QueryFunc: tc.mockedGQLFunc}
			mockServer := server.NewMCPServer("test", "1.0.0")
			_, handler := waitForPullRequestReview(mockServer, client, mockGQLClient, translations.NullTranslationHelper)

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

			// For completed responses, unmarshal and verify the result
			if tc.expectedResult != nil {
				var returnedActivity ActivityResult
				err = json.Unmarshal([]byte(textContent.Text), &returnedActivity)
				require.NoError(t, err)

				// Verify the activity result has the expected structure
				assert.NotEmpty(t, returnedActivity.ViewerDates)
				assert.NotEmpty(t, returnedActivity.NonViewerDates)
				assert.False(t, returnedActivity.ViewerMaxDate.IsZero())
				assert.False(t, returnedActivity.NonViewerMaxDate.IsZero())

				// Verify that non-viewer date is more recent than viewer date
				assert.True(t, returnedActivity.NonViewerMaxDate.After(returnedActivity.ViewerMaxDate))
			}
		})
	}
}
