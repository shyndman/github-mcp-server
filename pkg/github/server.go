package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v69/github"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
)

// NewServer creates a new GitHub MCP server with the specified GH client and logger.
func NewServer(gh *github.Client, gql *githubv4.Client, readOnly bool, logger *logrus.Logger, t translations.TranslationHelperFunc) *server.MCPServer {
	// Create a new MCP server
	s := server.NewMCPServer(
		"github-mcp-server",
		"0.0.1",
		server.WithResourceCapabilities(true, true),
		server.WithLogging())

	// Add GitHub Resources
	s.AddResourceTemplate(getRepositoryResourceContent(gh, t))
	s.AddResourceTemplate(getRepositoryResourceBranchContent(gh, t))
	s.AddResourceTemplate(getRepositoryResourceCommitContent(gh, t))
	s.AddResourceTemplate(getRepositoryResourceTagContent(gh, t))
	s.AddResourceTemplate(getRepositoryResourcePrContent(gh, t))

	// Add GitHub tools - Issues
	s.AddTool(getIssue(gh, t))
	s.AddTool(searchIssues(gh, t))
	s.AddTool(listIssues(gh, t))
	if !readOnly {
		s.AddTool(createIssue(gh, t))
		s.AddTool(addIssueComment(gh, t))
		s.AddTool(createIssue(gh, t))
		s.AddTool(updateIssue(gh, t))
	}

	// Add GitHub tools - Pull Requests
	s.AddTool(getPullRequest(gh, t))
	s.AddTool(listPullRequests(gh, t))
	s.AddTool(getPullRequestFiles(gh, t))
	s.AddTool(getPullRequestStatus(gh, t))
	s.AddTool(getPullRequestComments(gh, t))
	s.AddTool(getPullRequestReviews(gh, t))
	s.AddTool(waitForPullRequestChecks(s, gh, t))
	s.AddTool(waitForPullRequestReview(s, gh, gql, logger, t))

	if !readOnly {
		s.AddTool(mergePullRequest(gh, t))
		s.AddTool(updatePullRequestBranch(gh, t))
		s.AddTool(createPullRequestReview(gh, t))
		s.AddTool(replyToReviewComment(gh, t))
		s.AddTool(createPullRequest(gh, t))
	}

	// Add GitHub tools - Repositories
	s.AddTool(searchRepositories(gh, t))
	s.AddTool(getFileContents(gh, t))
	s.AddTool(listCommits(gh, t))
	if !readOnly {
		s.AddTool(createOrUpdateFile(gh, t))
		s.AddTool(createRepository(gh, t))
		s.AddTool(forkRepository(gh, t))
		s.AddTool(createBranch(gh, t))
		s.AddTool(pushFiles(gh, t))
	}

	// Add GitHub tools - Search
	s.AddTool(searchCode(gh, t))
	s.AddTool(searchUsers(gh, t))

	// Add GitHub tools - Users
	s.AddTool(getMe(gh, t))

	// Add GitHub tools - Code Scanning
	s.AddTool(getCodeScanningAlert(gh, t))
	s.AddTool(listCodeScanningAlerts(gh, t))
	return s
}

// getMe creates a tool to get details of the authenticated user.
func getMe(client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_me",
			mcp.WithDescription(t("TOOL_GET_ME_DESCRIPTION", "Get details of the authenticated GitHub user. Use this when a request include \"me\", \"my\"...")),
			mcp.WithString("reason",
				mcp.Description("Optional: reason the session was created"),
			),
		),
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			user, resp, err := client.Users.Get(ctx, "")
			if err != nil {
				return nil, fmt.Errorf("failed to get user: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get user: %s", string(body))), nil
			}

			r, err := json.Marshal(user)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal user: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// isAcceptedError checks if the error is an accepted error.
func isAcceptedError(err error) bool {
	var acceptedError *github.AcceptedError
	return errors.As(err, &acceptedError)
}

// requiredParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request.
// 2. Checks if the parameter is of the expected type.
// 3. Checks if the parameter is not empty, i.e: non-zero value
func requiredParam[T comparable](r mcp.CallToolRequest, p string) (T, error) {
	var zero T

	// Check if the parameter is present in the request
	if _, ok := r.Params.Arguments[p]; !ok {
		return zero, fmt.Errorf("missing required parameter: %s", p)
	}

	// Check if the parameter is of the expected type
	if _, ok := r.Params.Arguments[p].(T); !ok {
		return zero, fmt.Errorf("parameter %s is not of type %T", p, zero)
	}

	if r.Params.Arguments[p].(T) == zero {
		return zero, fmt.Errorf("missing required parameter: %s", p)

	}

	return r.Params.Arguments[p].(T), nil
}

// requiredInt is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request.
// 2. Checks if the parameter is of the expected type.
// 3. Checks if the parameter is not empty, i.e: non-zero value
func requiredInt(r mcp.CallToolRequest, p string) (int, error) {
	v, err := requiredParam[float64](r, p)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

// optionalParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request, if not, it returns its zero-value
// 2. If it is present, it checks if the parameter is of the expected type and returns it
func optionalParam[T any](r mcp.CallToolRequest, p string) (T, error) {
	var zero T

	// Check if the parameter is present in the request
	if _, ok := r.Params.Arguments[p]; !ok {
		return zero, nil
	}

	// Check if the parameter is of the expected type
	if _, ok := r.Params.Arguments[p].(T); !ok {
		return zero, fmt.Errorf("parameter %s is not of type %T, is %T", p, zero, r.Params.Arguments[p])
	}

	return r.Params.Arguments[p].(T), nil
}

// optionalIntParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request, if not, it returns its zero-value
// 2. If it is present, it checks if the parameter is of the expected type and returns it
func optionalIntParam(r mcp.CallToolRequest, p string) (int, error) {
	v, err := optionalParam[float64](r, p)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

// optionalIntParamWithDefault is a helper function that can be used to fetch a requested parameter from the request
// similar to optionalIntParam, but it also takes a default value.
func optionalIntParamWithDefault(r mcp.CallToolRequest, p string, d int) (int, error) {
	v, err := optionalIntParam(r, p)
	if err != nil {
		return 0, err
	}
	if v == 0 {
		return d, nil
	}
	return v, nil
}

// optionalStringArrayParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request, if not, it returns its zero-value
// 2. If it is present, iterates the elements and checks each is a string
func optionalStringArrayParam(r mcp.CallToolRequest, p string) ([]string, error) {
	// Check if the parameter is present in the request
	if _, ok := r.Params.Arguments[p]; !ok {
		return []string{}, nil
	}

	switch v := r.Params.Arguments[p].(type) {
	case []string:
		return v, nil
	case []any:
		strSlice := make([]string, len(v))
		for i, v := range v {
			s, ok := v.(string)
			if !ok {
				return []string{}, fmt.Errorf("parameter %s is not of type string, is %T", p, v)
			}
			strSlice[i] = s
		}
		return strSlice, nil
	default:
		return []string{}, fmt.Errorf("parameter %s could not be coerced to []string, is %T", p, r.Params.Arguments[p])
	}
}
