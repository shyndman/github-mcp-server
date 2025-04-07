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
func waitForPRChecks(client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
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

			// Define a type for our progress token
			type prChecksProgressToken struct {
				StartTime time.Time `json:"start_time"`
			}

			// Check if we have a progress token
			var progressToken prChecksProgressToken
			if request.Params.Meta != nil && request.Params.Meta.ProgressToken != nil {
				// Try to parse the progress token
				if tokenData, ok := request.Params.Meta.ProgressToken.(map[string]interface{}); ok {
					if startTimeStr, ok := tokenData["start_time"].(string); ok {
						startTime, err := time.Parse(time.RFC3339, startTimeStr)
						if err == nil {
							progressToken.StartTime = startTime
						}
					}
				}
			}

			// If this is the first call, initialize the progress token
			if progressToken.StartTime.IsZero() {
				progressToken.StartTime = time.Now()
			}

			// Check if we've exceeded the timeout
			elapsed := time.Since(progressToken.StartTime)
			if elapsed > time.Duration(timeoutSecs)*time.Second {
				return mcp.NewToolResultError(fmt.Sprintf("Timeout waiting for PR checks to complete after %d seconds", timeoutSecs)), nil
			}

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

			// Checks are still pending, return a progress token to continue waiting
			tokenJSON, err := json.Marshal(map[string]string{
				"start_time": progressToken.StartTime.Format(time.RFC3339),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to marshal progress token: %w", err)
			}

			// Create a progress result
			result := mcp.NewToolResultText(fmt.Sprintf("Waiting for PR checks to complete (elapsed: %.1f seconds)", elapsed.Seconds()))

			// Set the progress token
			var progressTokenMap map[string]interface{}
			if err := json.Unmarshal(tokenJSON, &progressTokenMap); err != nil {
				return nil, fmt.Errorf("failed to unmarshal progress token: %w", err)
			}

			// Create a progress notification
			_ = mcp.NewProgressNotification(progressTokenMap, elapsed.Seconds()/float64(timeoutSecs), nil)
			return result, nil
		}
}

// waitForPRReview creates a tool to wait for a new review to be added to a pull request.
func waitForPRReview(client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
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

			// Define a type for our progress token
			type prReviewProgressToken struct {
				StartTime    time.Time `json:"start_time"`
				LastReviewID int       `json:"last_review_id"`
			}

			// Check if we have a progress token
			var progressToken prReviewProgressToken
			if request.Params.Meta != nil && request.Params.Meta.ProgressToken != nil {
				// Try to parse the progress token
				if tokenData, ok := request.Params.Meta.ProgressToken.(map[string]interface{}); ok {
					if startTimeStr, ok := tokenData["start_time"].(string); ok {
						startTime, err := time.Parse(time.RFC3339, startTimeStr)
						if err == nil {
							progressToken.StartTime = startTime
						}
					}
					if lastReviewIDFloat, ok := tokenData["last_review_id"].(float64); ok {
						progressToken.LastReviewID = int(lastReviewIDFloat)
					}
				}
			}

			// If this is the first call, initialize the progress token
			if progressToken.StartTime.IsZero() {
				progressToken.StartTime = time.Now()
				progressToken.LastReviewID = lastReviewID
			}

			// Check if we've exceeded the timeout
			elapsed := time.Since(progressToken.StartTime)
			if elapsed > time.Duration(timeoutSecs)*time.Second {
				return mcp.NewToolResultError(fmt.Sprintf("Timeout waiting for PR review after %d seconds", timeoutSecs)), nil
			}

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
				if reviewID > progressToken.LastReviewID {
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

			// No new reviews, return a progress token to continue waiting
			tokenJSON, err := json.Marshal(map[string]interface{}{
				"start_time":     progressToken.StartTime.Format(time.RFC3339),
				"last_review_id": progressToken.LastReviewID,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to marshal progress token: %w", err)
			}

			// Create a progress result
			result := mcp.NewToolResultText(fmt.Sprintf("Waiting for new PR review (elapsed: %.1f seconds)", elapsed.Seconds()))

			// Set the progress token
			var progressTokenMap map[string]interface{}
			if err := json.Unmarshal(tokenJSON, &progressTokenMap); err != nil {
				return nil, fmt.Errorf("failed to unmarshal progress token: %w", err)
			}

			// Create a progress notification
			_ = mcp.NewProgressNotification(progressTokenMap, elapsed.Seconds()/float64(timeoutSecs), nil)
			return result, nil
		}
}
