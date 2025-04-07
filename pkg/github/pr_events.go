package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v69/github"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// PREventContext holds common state for PR event handlers
type PREventContext struct {
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

// handleResponse is a helper function to handle GitHub API responses and properly close the body
func handleResponse(resp *github.Response, errorPrefix string) error {
	fmt.Fprintf(os.Stderr, "[DEBUG] handleResponse: Processing response with status code %d for %s\n", resp.StatusCode, errorPrefix)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "[DEBUG] handleResponse: Non-OK status code %d detected\n", resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] handleResponse: Failed to read response body: %v\n", err)
			return fmt.Errorf("failed to read response body: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] handleResponse: Error response body: %s\n", string(body))
		return fmt.Errorf("%s: %s", errorPrefix, string(body))
	}
	fmt.Fprintf(os.Stderr, "[DEBUG] handleResponse: Successfully processed response\n")
	return nil
}

// sendProgressNotification sends a progress notification to the client
func sendProgressNotification(ctx context.Context, eventCtx *PREventContext) {
	fmt.Fprintf(os.Stderr, "[DEBUG] sendProgressNotification: Starting progress notification process\n")
	if eventCtx.ProgressToken == nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] sendProgressNotification: No progress token, skipping notification\n")
		return
	}

	// Calculate elapsed time
	elapsed := time.Since(eventCtx.StartTime)
	fmt.Fprintf(os.Stderr, "[DEBUG] sendProgressNotification: Elapsed time: %v\n", elapsed)

	// Calculate progress value
	var progress float64
	var total *float64
	// Just increment progress endlessly
	progress = elapsed.Seconds()
	// No total value when incrementing endlessly
	total = nil
	fmt.Fprintf(os.Stderr, "[DEBUG] sendProgressNotification: Progress without timeout: %f (no total)\n", progress)

	// Create and send a progress notification with the client's token
	n := mcp.NewProgressNotification(eventCtx.ProgressToken, progress, total)
	fmt.Fprintf(os.Stderr, "[DEBUG] sendProgressNotification: Created notification with token: %v\n", eventCtx.ProgressToken)

	// Send the progress notification to the client
	params := map[string]any{"progressToken": n.Params.ProgressToken, "progress": n.Params.Progress, "total": n.Params.Total}
	fmt.Fprintf(os.Stderr, "[DEBUG] sendProgressNotification: Sending notification with params: %+v\n", params)
	if err := eventCtx.MCPServer.SendNotificationToClient(ctx, "notifications/progress", params); err != nil {
		// Log the error but continue - notification failures shouldn't stop the process
		fmt.Printf("Failed to send progress notification: %v\n", err)
		fmt.Fprintf(os.Stderr, "[DEBUG] sendProgressNotification: ERROR sending notification: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] sendProgressNotification: Successfully sent notification\n")
	}
}

// parsePREventParams parses common parameters for PR event handlers and sets up the context
func parsePREventParams(ctx context.Context, mcpServer *server.MCPServer, client *github.Client, request mcp.CallToolRequest) (*PREventContext, *mcp.CallToolResult, context.CancelFunc, error) {
	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Starting parameter parsing for method: %s\n", request.Method)
	eventCtx := &PREventContext{
		MCPServer:    mcpServer,
		Client:       client,
		Ctx:          ctx,
		Request:      request,
		PollInterval: 5 * time.Second,
		StartTime:    time.Now(),
	}
	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Created event context with poll interval: %v\n", eventCtx.PollInterval)

	// Get required parameters
	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Extracting required 'owner' parameter\n")
	owner, err := requiredParam[string](request, "owner")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: ERROR extracting 'owner' parameter: %v\n", err)
		return nil, mcp.NewToolResultError(err.Error()), nil, nil
	}
	eventCtx.Owner = owner
	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Owner: %s\n", owner)

	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Extracting required 'repo' parameter\n")
	repo, err := requiredParam[string](request, "repo")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: ERROR extracting 'repo' parameter: %v\n", err)
		return nil, mcp.NewToolResultError(err.Error()), nil, nil
	}
	eventCtx.Repo = repo
	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Repo: %s\n", repo)

	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Extracting required 'pullNumber' parameter\n")
	pullNumber, err := requiredInt(request, "pullNumber")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: ERROR extracting 'pullNumber' parameter: %v\n", err)
		return nil, mcp.NewToolResultError(err.Error()), nil, nil
	}
	eventCtx.PullNumber = pullNumber
	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Pull Number: %d\n", pullNumber)

	// Create a no-op cancel function
	var cancel context.CancelFunc = func() {} // No-op cancel function
	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Using context without timeout\n")

	// Extract the client's progress token (if any)
	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Checking for progress token\n")
	if request.Params.Meta != nil {
		eventCtx.ProgressToken = request.Params.Meta.ProgressToken
		fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Progress token found: %v\n", eventCtx.ProgressToken)
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: No progress token found\n")
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] parsePREventParams: Parameter parsing complete\n")
	return eventCtx, nil, cancel, nil
}

// pollForPREvent runs a polling loop for PR events with proper context handling
func pollForPREvent(eventCtx *PREventContext, checkFn func() (*mcp.CallToolResult, error)) (*mcp.CallToolResult, error) {
	fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Starting polling loop for method: %s\n", eventCtx.Request.Method)
	// Use a defer to ensure we send a final progress update if needed
	defer func() {
		fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Exiting polling loop, sending final progress update\n")
		if eventCtx.ProgressToken != nil {
			sendProgressNotification(eventCtx.Ctx, eventCtx)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: No progress token, skipping final update\n")
		}
	}()

	// Enter polling loop
	pollCount := 0
	for {
		pollCount++
		fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Poll iteration #%d\n", pollCount)

		// Check if context is done (canceled or deadline exceeded)
		select {
		case <-eventCtx.Ctx.Done():
			fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Context done with error: %v\n", eventCtx.Ctx.Err())
			if eventCtx.Ctx.Err() == context.DeadlineExceeded {
				// Customize the timeout message based on the tool name
				var operation string
				if strings.Contains(eventCtx.Request.Method, "wait_for_pr_checks") {
					operation = "PR checks to complete"
				} else if strings.Contains(eventCtx.Request.Method, "wait_for_pr_review") {
					operation = "PR review"
				} else {
					operation = "operation"
				}
				fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Timeout exceeded waiting for %s\n", operation)
				return mcp.NewToolResultError(fmt.Sprintf("Timeout waiting for %s", operation)), nil
			}
			fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Operation canceled: %v\n", eventCtx.Ctx.Err())
			return mcp.NewToolResultError(fmt.Sprintf("Operation canceled: %v", eventCtx.Ctx.Err())), nil
		default:
			// Continue with current logic
			fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Context still active, continuing\n")
		}

		// Call the check function
		fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Calling check function\n")
		result, err := checkFn()
		// nil will be returned for result AND nil when we have not yet completed
		// our check
		if err != nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Check function returned error: %v\n", err)
			return nil, err
		}
		if result != nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Check function returned result, exiting poll loop\n")
			return result, nil
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Check function returned no result, continuing polling\n")

		// Send progress notification
		fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Sending progress notification\n")
		sendProgressNotification(eventCtx.Ctx, eventCtx)

		// Sleep before next poll
		fmt.Fprintf(os.Stderr, "[DEBUG] pollForPREvent: Sleeping for %v before next poll\n", eventCtx.PollInterval)
		time.Sleep(eventCtx.PollInterval)
	}
}

// waitForPRChecks creates a tool to wait for all status checks to complete on a pull request.
func waitForPRChecks(mcpServer *server.MCPServer, client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks: Registering wait_for_pr_checks tool\n")
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
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks: Tool handler called with method: %s\n", request.Method)
			// Parse common parameters and set up context
			eventCtx, result, cancel, err := parsePREventParams(ctx, mcpServer, client, request)
			if result != nil || err != nil {
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks: Parameter parsing failed: %v\n", err)
				return result, err
			}
			defer cancel()
			fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks: Parameters parsed successfully, starting polling\n")

			// Run the polling loop with a check function for PR checks
			return pollForPREvent(eventCtx, func() (*mcp.CallToolResult, error) {
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: Checking PR status\n")
				// First get the PR to find the head SHA
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: Getting PR details for %s/%s #%d\n", eventCtx.Owner, eventCtx.Repo, eventCtx.PullNumber)
				pr, resp, err := client.PullRequests.Get(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, eventCtx.PullNumber)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: ERROR getting PR: %v\n", err)
					return nil, fmt.Errorf("failed to get pull request: %w", err)
				}

				// Handle the response
				if err := handleResponse(resp, "failed to get pull request"); err != nil {
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: ERROR handling PR response: %v\n", err)
					return mcp.NewToolResultError(err.Error()), nil
				}

				if pr.Head == nil || pr.Head.SHA == nil {
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: ERROR: PR head or SHA is nil\n")
					return mcp.NewToolResultError("PR head SHA is missing"), nil
				}

				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: Got PR head SHA: %s\n", *pr.Head.SHA)

				// Get combined status for the head SHA
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: Getting combined status for SHA: %s\n", *pr.Head.SHA)
				status, resp, err := client.Repositories.GetCombinedStatus(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, *pr.Head.SHA, nil)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: ERROR getting combined status: %v\n", err)
					return nil, fmt.Errorf("failed to get combined status: %w", err)
				}

				// Handle the response
				if err := handleResponse(resp, "failed to get combined status"); err != nil {
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: ERROR handling status response: %v\n", err)
					return mcp.NewToolResultError(err.Error()), nil
				}

				// Check if all checks are complete
				if status.State == nil {
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: Status state is nil, continuing polling\n")
					return nil, nil
				}

				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: Current status state: %s\n", *status.State)
				if *status.State == "success" || *status.State == "failure" || *status.State == "error" {
					// All checks are complete, return the status
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: Checks complete with state: %s\n", *status.State)
					r, err := json.Marshal(status)
					if err != nil {
						fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: ERROR marshaling response: %v\n", err)
						return nil, fmt.Errorf("failed to marshal response: %w", err)
					}
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: Returning successful result\n")
					return mcp.NewToolResultText(string(r)), nil
				}

				// Return nil to continue polling
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRChecks.checkFn: Checks still in progress (state: %s), continuing polling\n", *status.State)
				return nil, nil
			})
		}
}

// waitForPRReview creates a tool to wait for a new review to be added to a pull request.
func waitForPRReview(mcpServer *server.MCPServer, client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview: Registering wait_for_pr_review tool\n")
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
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview: Tool handler called with method: %s\n", request.Method)
			// Parse common parameters and set up context
			eventCtx, result, cancel, err := parsePREventParams(ctx, mcpServer, client, request)
			if result != nil || err != nil {
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview: Parameter parsing failed: %v\n", err)
				return result, err
			}
			defer cancel()
			fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview: Parameters parsed successfully\n")

			// Get optional last_review_id parameter
			fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview: Extracting optional 'last_review_id' parameter\n")
			lastReviewID, err := optionalIntParam(request, "last_review_id")
			if err != nil {
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview: ERROR extracting 'last_review_id' parameter: %v\n", err)
				return mcp.NewToolResultError(err.Error()), nil
			}
			fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview: Last review ID: %d\n", lastReviewID)
			fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview: Starting polling for new reviews\n")

			// Run the polling loop with a check function for PR reviews
			return pollForPREvent(eventCtx, func() (*mcp.CallToolResult, error) {
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: Checking for new reviews\n")
				// Get the current reviews
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: Listing reviews for %s/%s #%d\n", eventCtx.Owner, eventCtx.Repo, eventCtx.PullNumber)
				reviews, resp, err := client.PullRequests.ListReviews(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, eventCtx.PullNumber, nil)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: ERROR listing reviews: %v\n", err)
					return nil, fmt.Errorf("failed to get pull request reviews: %w", err)
				}

				// Handle the response
				if err := handleResponse(resp, "failed to get pull request reviews"); err != nil {
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: ERROR handling reviews response: %v\n", err)
					return mcp.NewToolResultError(err.Error()), nil
				}

				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: Found %d reviews, checking for new ones after ID %d\n", len(reviews), lastReviewID)
				// Check if there are any new reviews
				var latestReview *github.PullRequestReview
				for _, review := range reviews {
					if review.ID == nil {
						fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: Skipping review with nil ID\n")
						continue
					}

					reviewID := int(*review.ID)
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: Checking review ID: %d\n", reviewID)
					if reviewID > lastReviewID {
						fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: Found newer review with ID: %d\n", reviewID)
						if latestReview == nil || reviewID > int(*latestReview.ID) {
							fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: This is the latest review so far\n")
							latestReview = review
						}
					}
				}

				// If we found a new review, return it
				if latestReview != nil {
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: Found new review with ID: %d\n", *latestReview.ID)
					r, err := json.Marshal(latestReview)
					if err != nil {
						fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: ERROR marshaling review: %v\n", err)
						return nil, fmt.Errorf("failed to marshal response: %w", err)
					}
					fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: Returning new review result\n")
					return mcp.NewToolResultText(string(r)), nil
				}

				// Return nil to continue polling
				fmt.Fprintf(os.Stderr, "[DEBUG] waitForPRReview.checkFn: No new reviews found, continuing polling\n")
				return nil, nil
			})
		}
}
