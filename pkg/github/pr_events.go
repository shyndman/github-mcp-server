package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v69/github"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// waitForPRChecks creates a tool to wait for all status checks to complete on a pull request.
func waitForPRChecks(mcpServer *server.MCPServer, client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("wait_for_pr_checks",
			mcp.WithDescription(t("TOOL_WAIT_FOR_PR_CHECKS_DESCRIPTION", "Wait for all status checks to complete on a pull request")),
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
			mcp.WithNumber("timeout_seconds",
				mcp.Description("How long to wait before giving up (default 600 seconds)"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Get required parameters
			owner, err := requiredParam[string](request, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			repo, err := requiredParam[string](request, "repo")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			pullNumber, err := requiredInt(request, "pullNumber")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Get timeout parameter with default value
			timeoutSecs, err := optionalIntParamWithDefault(request, "timeout_seconds", 600)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Initialize start time for this operation
			startTime := time.Now()

			// Set up polling interval
			pollInterval := 5 * time.Second
			timeoutDuration := time.Duration(timeoutSecs) * time.Second
			endTime := startTime.Add(timeoutDuration)

			// Extract the client's progress token (if any)
			var progressToken interface{}
			if request.Params.Meta != nil {
				progressToken = request.Params.Meta.ProgressToken
			}

			// Enter polling loop
			for time.Now().Before(endTime) {
				// Calculate elapsed time
				elapsed := time.Since(startTime)

				// First get the PR to find the head SHA
				pr, resp, err := client.PullRequests.Get(ctx, owner, repo, pullNumber)
				if err != nil {
					return nil, fmt.Errorf("failed to get pull request: %w", err)
				}
				defer func() { _ = resp.Body.Close() }()

				if resp.StatusCode != http.StatusOK {
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						return nil, fmt.Errorf("failed to read response body: %w", err)
					}
					return mcp.NewToolResultError(fmt.Sprintf("failed to get pull request: %s", string(body))), nil
				}

				// Get combined status for the head SHA
				status, resp, err := client.Repositories.GetCombinedStatus(ctx, owner, repo, *pr.Head.SHA, nil)
				if err != nil {
					return nil, fmt.Errorf("failed to get combined status: %w", err)
				}
				defer func() { _ = resp.Body.Close() }()

				if resp.StatusCode != http.StatusOK {
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						return nil, fmt.Errorf("failed to read response body: %w", err)
					}
					return mcp.NewToolResultError(fmt.Sprintf("failed to get combined status: %s", string(body))), nil
				}

				// Check if all checks are complete
				if status.State != nil && (*status.State == "success" || *status.State == "failure" || *status.State == "error") {
					// All checks are complete, return the status
					r, err := json.Marshal(status)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal response: %w", err)
					}
					return mcp.NewToolResultText(string(r)), nil
				}

				// If the client provided a progress token, send a progress notification
				if progressToken != nil {
					// Calculate progress as percentage of time elapsed
					progress := elapsed.Seconds() / timeoutDuration.Seconds()
					total := 1.0

					// Create and send a progress n with the client's token
					n := mcp.NewProgressNotification(progressToken, progress, &total)
					// In a real implementation, you would send this notification to the client
					// For now, we're just creating it but not sending it
					params := map[string]any{"progressToken": n.Params.ProgressToken, "progress": n.Params.Progress, "total": n.Params.Total}
					mcpServer.SendNotificationToClient(ctx, "notifications/progress", params)
				}

				// Sleep before next poll
				time.Sleep(pollInterval)
			}

			// If we got here, we timed out
			return mcp.NewToolResultError(fmt.Sprintf("Timeout waiting for PR checks to complete after %d seconds", timeoutSecs)), nil
		}
}

// waitForPRReview creates a tool to wait for a new review to be added to a pull request.
func waitForPRReview(mcpServer *server.MCPServer, client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("wait_for_pr_review",
			mcp.WithDescription(t("TOOL_WAIT_FOR_PR_REVIEW_DESCRIPTION", "Wait for a new review to be added to a pull request")),
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
			mcp.WithNumber("timeout_seconds",
				mcp.Description("How long to wait before giving up (default 600 seconds)"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Get required parameters
			owner, err := requiredParam[string](request, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			repo, err := requiredParam[string](request, "repo")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			pullNumber, err := requiredInt(request, "pullNumber")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Get optional parameters
			lastReviewID, err := optionalIntParam(request, "last_review_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Get timeout parameter with default value
			timeoutSecs, err := optionalIntParamWithDefault(request, "timeout_seconds", 600)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Initialize start time for this operation
			startTime := time.Now()

			// Set up polling interval
			pollInterval := 5 * time.Second
			timeoutDuration := time.Duration(timeoutSecs) * time.Second
			endTime := startTime.Add(timeoutDuration)

			// Extract the client's progress token (if any)
			var progressToken interface{}
			if request.Params.Meta != nil {
				progressToken = request.Params.Meta.ProgressToken
			}

			// Enter polling loop
			for time.Now().Before(endTime) {
				// Calculate elapsed time
				elapsed := time.Since(startTime)

				// Get the current reviews
				reviews, resp, err := client.PullRequests.ListReviews(ctx, owner, repo, pullNumber, nil)
				if err != nil {
					return nil, fmt.Errorf("failed to get pull request reviews: %w", err)
				}
				defer func() { _ = resp.Body.Close() }()

				if resp.StatusCode != http.StatusOK {
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						return nil, fmt.Errorf("failed to read response body: %w", err)
					}
					return mcp.NewToolResultError(fmt.Sprintf("failed to get pull request reviews: %s", string(body))), nil
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

				// If the client provided a progress token, send a progress notification
				if progressToken != nil {
					// Calculate progress as percentage of time elapsed
					progress := elapsed.Seconds() / timeoutDuration.Seconds()
					total := 1.0

					// Create and send a progress n with the client's token
					n := mcp.NewProgressNotification(progressToken, progress, &total)

					// In a real implementation, you would send this notification to the client
					// For now, we're just creating it but not sending it
					params := map[string]any{"progressToken": n.Params.ProgressToken, "progress": n.Params.Progress, "total": n.Params.Total}
					mcpServer.SendNotificationToClient(ctx, "notifications/progress", params)
				}

				// Sleep before next poll
				time.Sleep(pollInterval)
			}

			// If we got here, we timed out
			return mcp.NewToolResultError(fmt.Sprintf("Timeout waiting for PR review after %d seconds", timeoutSecs)), nil
		}
}
