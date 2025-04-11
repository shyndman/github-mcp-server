package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/ccoveille/go-safecast"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v69/github"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
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

// PullRequestActivityQuery represents the GraphQL query structure for PR activity
type PullRequestActivityQuery struct {
	Repository struct {
		PullRequest struct {
			Commits struct {
				Nodes []struct {
					Commit struct {
						Author struct {
							Email githubv4.String
						}
						CommittedDate githubv4.DateTime
					}
				}
			} `graphql:"commits(last: 10)"`
			Reviews struct {
				TotalCount githubv4.Int
				Nodes      []struct {
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
				}
			} `graphql:"reviews(last: 100)"`
			Author struct {
				Login githubv4.String
			}
		} `graphql:"pullRequest(number: $pr)"`
	} `graphql:"repository(owner: $owner, name: $repo)"`
}

// ActivityResult represents the processed result of PR activity
type ActivityResult struct {
	ViewerDates      []string  `json:"viewerDates"`
	ViewerMaxDate    time.Time `json:"viewerMaxDate"`
	NonViewerDates   []string  `json:"nonViewerDates"`
	NonViewerMaxDate time.Time `json:"nonViewerMaxDate"`
}

// GraphQLQuerier defines the minimal interface needed for GraphQL operations
type GraphQLQuerier interface {
	Query(ctx context.Context, q any, variables map[string]any) error
}

// waitForPullRequestReview creates a tool to wait for a new review to be added to a pull request.
func waitForPullRequestReview(mcpServer *server.MCPServer, gh *github.Client, gql GraphQLQuerier, logger *logrus.Logger, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
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
				mcp.Max(math.MaxInt32),
				mcp.Min(math.MinInt32),
				mcp.Required(),
				mcp.Description("Pull request number"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			logger.Info("waitForPullRequestReview()")

			// Parse common parameters and set up context
			eventCtx, result, cancel, err := parsePullRequestEventParams(ctx, mcpServer, gh, request)
			if result != nil || err != nil {
				logger.WithError(err).Error("Failed to parse pull request event parameters")
				return result, err
			}
			defer cancel()

			logger.WithFields(logrus.Fields{
				"owner":        eventCtx.Owner,
				"repo":         eventCtx.Repo,
				"pullNumber":   eventCtx.PullNumber,
				"pollInterval": eventCtx.PollInterval,
			}).Info("Starting to wait for pull request review")

			// Run the polling loop with a check function for pull request reviews
			return pollForPullRequestEvent(eventCtx, func() (*mcp.CallToolResult, error) {
				// Log the polling iteration
				logger.WithFields(logrus.Fields{
					"owner":      eventCtx.Owner,
					"repo":       eventCtx.Repo,
					"pullNumber": eventCtx.PullNumber,
					"elapsed":    time.Since(eventCtx.StartTime).Round(time.Second),
				}).Debug("Polling for pull request review activity")

				// First, get the PR to determine the author's email
				pr, resp, err := gh.PullRequests.Get(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, eventCtx.PullNumber)
				if err != nil {
					logger.WithError(err).Error("Failed to get pull request")
					return nil, fmt.Errorf("failed to get pull request: %w", err)
				}

				// Handle the response
				if err := handleResponse(resp, "failed to get pull request"); err != nil {
					logger.WithError(err).Error("Failed to handle pull request response")
					return mcp.NewToolResultError(err.Error()), nil
				}

				if pr.User == nil || pr.User.Login == nil {
					logger.Error("Pull request author information is missing")
					return mcp.NewToolResultError("Pull request author information is missing"), nil
				}

				prAuthorLogin := *pr.User.Login
				logger.WithField("prAuthor", prAuthorLogin).Debug("Retrieved pull request author")

				// Execute GraphQL query to get PR activity
				var query PullRequestActivityQuery
				// Convert pull number to int32 safely using safecast
				prNumber, err := safecast.ToInt32(eventCtx.PullNumber)
				if err != nil {
					logger.WithError(err).Error("Pull request number is too large for GraphQL query")
					return nil, fmt.Errorf("pull request number %d is too large for GraphQL query: %w", eventCtx.PullNumber, err)
				}

				variables := map[string]any{
					"owner": githubv4.String(eventCtx.Owner),
					"repo":  githubv4.String(eventCtx.Repo),
					"pr":    githubv4.Int(prNumber),
				}

				logger.Debug("Executing GraphQL query for PR activity")
				err = gql.Query(eventCtx.Ctx, &query, variables)
				if err != nil {
					logger.WithError(err).Error("Failed to execute GraphQL query")
					return nil, fmt.Errorf("failed to execute GraphQL query: %w", err)
				}

				// Process the query results to find the most recent activity
				viewerDates := []time.Time{}
				nonViewerDates := []time.Time{}

				// Process commits
				commitCount := len(query.Repository.PullRequest.Commits.Nodes)
				logger.WithField("commitCount", commitCount).Debug("Processing commits")
				for _, node := range query.Repository.PullRequest.Commits.Nodes {
					commitDate := node.Commit.CommittedDate.Time
					commitAuthorEmail := string(node.Commit.Author.Email)

					// Check if the commit is from the PR author
					if strings.Contains(commitAuthorEmail, string(prAuthorLogin)) {
						viewerDates = append(viewerDates, commitDate)
					} else {
						nonViewerDates = append(nonViewerDates, commitDate)
					}
				}

				// Process reviews
				reviewCount := len(query.Repository.PullRequest.Reviews.Nodes)
				logger.WithField("reviewCount", reviewCount).Debug("Processing reviews")
				for _, review := range query.Repository.PullRequest.Reviews.Nodes {
					reviewDate := review.UpdatedAt.Time

					// Check if the review is from the PR author
					if review.ViewerDidAuthor {
						viewerDates = append(viewerDates, reviewDate)
					} else {
						nonViewerDates = append(nonViewerDates, reviewDate)
					}

					// Process review comments
					commentCount := len(review.Comments.Nodes)
					logger.WithField("commentCount", commentCount).Debug("Processing review comments")
					for _, comment := range review.Comments.Nodes {
						commentDate := comment.UpdatedAt.Time

						// Check if the comment is from the PR author
						if comment.ViewerDidAuthor {
							viewerDates = append(viewerDates, commentDate)
						} else {
							nonViewerDates = append(nonViewerDates, commentDate)
						}
					}
				}

				// Find the most recent dates
				var viewerMaxDate, nonViewerMaxDate time.Time
				for _, date := range viewerDates {
					if date.After(viewerMaxDate) {
						viewerMaxDate = date
					}
				}

				for _, date := range nonViewerDates {
					if date.After(nonViewerMaxDate) {
						nonViewerMaxDate = date
					}
				}

				// Log the activity summary
				logger.WithFields(logrus.Fields{
					"viewerActivityCount":    len(viewerDates),
					"nonViewerActivityCount": len(nonViewerDates),
					"viewerMaxDate":          viewerMaxDate,
					"nonViewerMaxDate":       nonViewerMaxDate,
				}).Debug("Pull request activity summary")

				// Convert dates to strings for JSON output
				viewerDateStrings := make([]string, len(viewerDates))
				for i, date := range viewerDates {
					viewerDateStrings[i] = date.Format(time.RFC3339)
				}

				nonViewerDateStrings := make([]string, len(nonViewerDates))
				for i, date := range nonViewerDates {
					nonViewerDateStrings[i] = date.Format(time.RFC3339)
				}

				// Check if a non-author has added information more recently than the author
				if !nonViewerMaxDate.IsZero() && nonViewerMaxDate.After(viewerMaxDate) {
					// A reviewer has added information more recently than the author
					logger.Info("Detected new reviewer activity - completing wait")
					activityResult := ActivityResult{
						ViewerDates:      viewerDateStrings,
						ViewerMaxDate:    viewerMaxDate,
						NonViewerDates:   nonViewerDateStrings,
						NonViewerMaxDate: nonViewerMaxDate,
					}

					r, err := json.Marshal(activityResult)
					if err != nil {
						logger.WithError(err).Error("Failed to marshal response")
						return nil, fmt.Errorf("failed to marshal response: %w", err)
					}
					return mcp.NewToolResultText(string(r)), nil
				}

				// Return nil to continue polling
				logger.Debug("No new reviewer activity detected, continuing to poll")
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
	// Get a logger from the context if available
	logger := logrus.StandardLogger()

	// Use a defer to ensure we send a final progress update if needed
	defer func() {
		if eventCtx.ProgressToken != nil {
			logger.Debug("Sending final progress notification")
			sendProgressNotification(eventCtx.Ctx, eventCtx)
		}
	}()

	logger.WithFields(logrus.Fields{
		"owner":        eventCtx.Owner,
		"repo":         eventCtx.Repo,
		"pullNumber":   eventCtx.PullNumber,
		"pollInterval": eventCtx.PollInterval,
		"method":       eventCtx.Request.Method,
	}).Debug("Starting poll loop for pull request event")

	pollCount := 0

	// Enter polling loop
	for {
		pollCount++

		// Check if context is done (canceled or deadline exceeded)
		select {
		case <-eventCtx.Ctx.Done():
			if eventCtx.Ctx.Err() == context.DeadlineExceeded {
				var operation string
				switch {
				case strings.Contains(eventCtx.Request.Method, "wait_for_pullrequest_checks"):
					operation = "pull request checks to complete"
				case strings.Contains(eventCtx.Request.Method, "wait_for_pullrequest_review"):
					operation = "pull request review"
				default:
					operation = "operation"
				}
				logger.WithField("operation", operation).Warn("Timeout waiting for operation to complete")
				return mcp.NewToolResultError(fmt.Sprintf("Timeout waiting for %s", operation)), nil
			}
			logger.WithError(eventCtx.Ctx.Err()).Warn("Operation canceled")
			return mcp.NewToolResultError(fmt.Sprintf("Operation canceled: %v", eventCtx.Ctx.Err())), nil
		default:
			// Continue with current logic
		}

		logger.WithFields(logrus.Fields{
			"pollCount": pollCount,
			"elapsed":   time.Since(eventCtx.StartTime).Round(time.Second),
		}).Debug("Executing poll iteration")

		// Call the check function
		result, err := checkFn()
		// nil will be returned for result AND nil when we have not yet completed
		// our check
		if err != nil {
			logger.WithError(err).Error("Error during poll check function")
			return nil, err
		}
		if result != nil {
			logger.WithField("pollCount", pollCount).Info("Poll completed successfully")
			return result, nil
		}

		// Send progress notification
		sendProgressNotification(eventCtx.Ctx, eventCtx)

		// Sleep before next poll
		logger.WithField("pollInterval", eventCtx.PollInterval).Debug("Sleeping before next poll")
		time.Sleep(eventCtx.PollInterval)
	}
}

// sendProgressNotification sends a progress notification to the client
func sendProgressNotification(ctx context.Context, eventCtx *PullRequestEventContext) {
	// Get a logger from the context if available
	logger := logrus.StandardLogger()

	if eventCtx.ProgressToken == nil {
		logger.Debug("No progress token available, skipping progress notification")
		return
	}

	// Calculate elapsed time
	elapsed := time.Since(eventCtx.StartTime)

	// Calculate progress value - increment progress endlessly with no total
	progress := elapsed.Seconds()
	var total *float64

	logger.WithFields(logrus.Fields{
		"elapsed":  elapsed.Round(time.Second),
		"progress": progress,
	}).Debug("Sending progress notification")

	// Create and send a progress notification with the client's token
	n := mcp.NewProgressNotification(eventCtx.ProgressToken, progress, total)
	params := map[string]any{"progressToken": n.Params.ProgressToken, "progress": n.Params.Progress, "total": n.Params.Total}

	if err := eventCtx.MCPServer.SendNotificationToClient(ctx, "notifications/progress", params); err != nil {
		// Log the error but continue - notification failures shouldn't stop the process
		logger.WithError(err).Warn("Failed to send progress notification")
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
	owner, ok := request.Params.Arguments["owner"].(string)
	if !ok || owner == "" {
		return nil, mcp.NewToolResultError("missing required parameter: owner"), nil, nil
	}
	eventCtx.Owner = owner

	repo, ok := request.Params.Arguments["repo"].(string)
	if !ok || repo == "" {
		return nil, mcp.NewToolResultError("missing required parameter: repo"), nil, nil
	}
	eventCtx.Repo = repo

	pullNumberFloat, ok := request.Params.Arguments["pullNumber"].(float64)
	if !ok || pullNumberFloat == 0 {
		return nil, mcp.NewToolResultError("missing required parameter: pullNumber"), nil, nil
	}
	eventCtx.PullNumber = int(pullNumberFloat)

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
