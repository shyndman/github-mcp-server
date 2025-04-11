package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v69/github"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/shurcooL/githubv4"
)

// PullRequestEventContext holds common state for pull request event handlers
type PullRequestEventContext struct {
	MCPServer     *server.MCPServer
	Client        *github.Client
	Ctx           context.Context
	Request       mcp.CallToolRequest
	Owner         string
	Repo          string
	PullNumber    int
	StartTime     time.Time
	ProgressToken any
	PollInterval  time.Duration
}

// waitForPullRequestReview creates a tool to wait for a new review to be added to a pull request.
func waitForPullRequestReview(mcpServer *server.MCPServer, gh *github.Client, gql *githubv4.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("wait_for_pullrequest_review",
			mcp.WithDescription(t("TOOL_WAIT_FOR_PULLREQUEST_REVIEW_DESCRIPTION", "Wait for a pull request to be approved, or for additional feedback to be added")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("Repository owner"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithNumber("pullNumber",
				mcp.Required(),
				mcp.Description("Pull request number"),
			),
			mcp.WithNumber("last_review_id",
				mcp.Description("ID of most recent review (wait for newer reviews)"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Parse common parameters and set up context
			eventCtx, result, cancel, err := parsePullRequestEventParams(ctx, mcpServer, gh, request)
			if result != nil || err != nil {
				return result, err
			}
			defer cancel()

			// Get optional last_review_id parameter
			lastReviewID, err := optionalIntParam(request, "last_review_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Run the polling loop with a check function for pull request reviews
			return pollForPullRequestEvent(eventCtx, func() (*mcp.CallToolResult, error) {
				// Get the current reviews
				reviews, resp, err := gh.PullRequests.ListReviews(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, eventCtx.PullNumber, nil)
				if err != nil {
					return nil, fmt.Errorf("failed to get pull request reviews: %w", err)
				}

				// Handle the response
				if err := handleResponse(resp, "failed to get pull request reviews"); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}

				// Check if there are any new reviews
				var latestReview *github.PullRequestReview
				for _, review := range reviews {
					if review.ID == nil {
						continue
					}

					reviewID := int(*review.ID)
					if reviewID > lastReviewID {
						if latestReview == nil || reviewID > int(*latestReview.ID) {
							latestReview = review
						}
					}
				}

				// If we found a new review, return it
				if latestReview != nil {
					r, err := json.Marshal(latestReview)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal response: %w", err)
					}
					return mcp.NewToolResultText(string(r)), nil
				}

				// Return nil to continue polling
				return nil, nil
			})
		}
}

// waitForPullRequestChecks creates a tool to wait for all status checks to complete on a pull request.
func waitForPullRequestChecks(mcpServer *server.MCPServer, client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("wait_for_pullrequest_checks",
			mcp.WithDescription(t("TOOL_WAIT_FOR_PULLREQUEST_CHECKS_DESCRIPTION", "Wait for all status checks to complete on a pull request")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("Repository owner"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithNumber("pullNumber",
				mcp.Required(),
				mcp.Description("Pull request number"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Parse common parameters and set up context
			eventCtx, result, cancel, err := parsePullRequestEventParams(ctx, mcpServer, client, request)
			if result != nil || err != nil {
				return result, err
			}
			defer cancel()

			// Run the polling loop with a check function for pull request checks
			return pollForPullRequestEvent(eventCtx, func() (*mcp.CallToolResult, error) {
				// First get the pull request to find the head SHA
				pr, resp, err := client.PullRequests.Get(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, eventCtx.PullNumber)
				if err != nil {
					return nil, fmt.Errorf("failed to get pull request: %w", err)
				}

				// Handle the response
				if err := handleResponse(resp, "failed to get pull request"); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}

				if pr.Head == nil || pr.Head.SHA == nil {
					return mcp.NewToolResultError("Pull request head SHA is missing"), nil
				}

				// Get check runs for the head SHA
				checkRuns, resp, err := client.Checks.ListCheckRunsForRef(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, *pr.Head.SHA, nil)
				if err != nil {
					return nil, fmt.Errorf("failed to get check runs: %w", err)
				}

				// Handle the response
				if err := handleResponse(resp, "failed to get check runs"); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}

				// Check if there are any check runs
				if checkRuns.GetTotal() == 0 {
					// If there are no check runs, we should consider the checks complete
					// Otherwise, we'd poll indefinitely for repositories without checks
					r, err := json.Marshal(checkRuns)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal response: %w", err)
					}
					return mcp.NewToolResultText(string(r)), nil
				}

				// Check if all checks are complete
				allComplete := true
				for _, checkRun := range checkRuns.CheckRuns {
					if checkRun.GetStatus() != "completed" {
						allComplete = false
						break
					}
				}

				if allComplete {
					// All checks are complete, return the check runs
					r, err := json.Marshal(checkRuns)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal response: %w", err)
					}
					return mcp.NewToolResultText(string(r)), nil
				}

				// Return nil to continue polling
				return nil, nil
			})
		}
}

// pollForPullRequestEvent runs a polling loop for pull request events with proper context handling
func pollForPullRequestEvent(eventCtx *PullRequestEventContext, checkFn func() (*mcp.CallToolResult, error)) (*mcp.CallToolResult, error) {
	// Use a defer to ensure we send a final progress update if needed
	defer func() {
		if eventCtx.ProgressToken != nil {
			sendProgressNotification(eventCtx.Ctx, eventCtx)
		}
	}()

	// Enter polling loop
	for {
		// Check if context is done (canceled or deadline exceeded)
		select {
		case <-eventCtx.Ctx.Done():
			if eventCtx.Ctx.Err() == context.DeadlineExceeded {
				// Customize the timeout message based on the tool name
				var operation string
				switch {
				case strings.Contains(eventCtx.Request.Method, "wait_for_pullrequest_checks"):
					operation = "pull request checks to complete"
				case strings.Contains(eventCtx.Request.Method, "wait_for_pullrequest_review"):
					operation = "pull request review"
				default:
					operation = "operation"
				}
				return mcp.NewToolResultError(fmt.Sprintf("Timeout waiting for %s", operation)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("Operation canceled: %v", eventCtx.Ctx.Err())), nil
		default:
			// Continue with current logic
		}

		// Call the check function
		result, err := checkFn()
		// nil will be returned for result AND nil when we have not yet completed
		// our check
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}

		// Send progress notification
		sendProgressNotification(eventCtx.Ctx, eventCtx)

		// Sleep before next poll
		time.Sleep(eventCtx.PollInterval)
	}
}

// sendProgressNotification sends a progress notification to the client
func sendProgressNotification(ctx context.Context, eventCtx *PullRequestEventContext) {
	if eventCtx.ProgressToken == nil {
		return
	}

	// Calculate elapsed time
	elapsed := time.Since(eventCtx.StartTime)

	// Calculate progress value - increment progress endlessly with no total
	progress := elapsed.Seconds()
	var total *float64

	// Create and send a progress notification with the client's token
	n := mcp.NewProgressNotification(eventCtx.ProgressToken, progress, total)
	params := map[string]any{"progressToken": n.Params.ProgressToken, "progress": n.Params.Progress, "total": n.Params.Total}

	if err := eventCtx.MCPServer.SendNotificationToClient(ctx, "notifications/progress", params); err != nil {
		// Log the error but continue - notification failures shouldn't stop the process
		fmt.Printf("Failed to send progress notification: %v\n", err)
	}
}

// parsePullRequestEventParams parses common parameters for pull request event handlers and sets up the context
func parsePullRequestEventParams(ctx context.Context, mcpServer *server.MCPServer, client *github.Client, request mcp.CallToolRequest) (*PullRequestEventContext, *mcp.CallToolResult, context.CancelFunc, error) {
	eventCtx := &PullRequestEventContext{
		MCPServer:    mcpServer,
		Client:       client,
		Ctx:          ctx,
		Request:      request,
		PollInterval: 5 * time.Second,
		StartTime:    time.Now(),
	}

	// Get required parameters
	owner, err := requiredParam[string](request, "owner")
	if err != nil {
		return nil, mcp.NewToolResultError(err.Error()), nil, nil
	}
	eventCtx.Owner = owner

	repo, err := requiredParam[string](request, "repo")
	if err != nil {
		return nil, mcp.NewToolResultError(err.Error()), nil, nil
	}
	eventCtx.Repo = repo

	pullNumber, err := requiredInt(request, "pullNumber")
	if err != nil {
		return nil, mcp.NewToolResultError(err.Error()), nil, nil
	}
	eventCtx.PullNumber = pullNumber

	// Create a no-op cancel function
	var cancel context.CancelFunc = func() {} // No-op cancel function

	// Extract the client's progress token (if any)
	if request.Params.Meta != nil {
		eventCtx.ProgressToken = request.Params.Meta.ProgressToken
	}

	return eventCtx, nil, cancel, nil
}

// handleResponse is a helper function to handle GitHub API responses and properly close the body
func handleResponse(resp *github.Response, errorPrefix string) error {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}
		return fmt.Errorf("%s: %s", errorPrefix, string(body))
	}
	return nil
}
