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

// CheckRunDetails represents detailed information about a single check run
type CheckRunDetails struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion,omitempty"`
	HTMLURL      string `json:"html_url"`
	DetailsURL   string `json:"details_url,omitempty"`
	Title        string `json:"title,omitempty"`
	Summary      string `json:"summary,omitempty"`
	Text         string `json:"text,omitempty"`
	WorkflowPath string `json:"workflow_path,omitempty"`
}

// CheckRunsResult represents the processed result of check runs with detailed information
type CheckRunsResult struct {
	Total         int               `json:"total"`
	AllComplete   bool              `json:"all_complete"`
	AllSuccessful bool              `json:"all_successful"`
	CheckRuns     []CheckRunDetails `json:"check_runs"`
	FailedChecks  []CheckRunDetails `json:"failed_checks,omitempty"`
	PendingChecks []CheckRunDetails `json:"pending_checks,omitempty"`
}

// GraphQLQuerier defines the minimal interface needed for GraphQL operations
type GraphQLQuerier interface {
	Query(ctx context.Context, q any, variables map[string]any) error
}

// WaitForPullRequestReview creates a tool to wait for a new review to be added to a pull request.
func WaitForPullRequestReview(mcpServer *server.MCPServer, getClient GetClientFn, gql GraphQLQuerier, logger *logrus.Logger, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
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

			// Get the GitHub client
			client, err := getClient(ctx)
			if err != nil {
				logger.WithError(err).Error("Failed to get GitHub client")
				return mcp.NewToolResultError(fmt.Sprintf("Failed to get GitHub client: %v", err)), nil
			}

			// Parse common parameters and set up context
			eventCtx, result, cancel, err := parsePullRequestEventParams(ctx, mcpServer, client, request)
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

				// Get the GitHub client for this request
				client, err := getClient(eventCtx.Ctx)
				if err != nil {
					logger.WithError(err).Error("Failed to get GitHub client")
					return nil, fmt.Errorf("failed to get GitHub client: %w", err)
				}

				// First, get the PR to determine the author's email
				pr, resp, err := client.PullRequests.Get(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, eventCtx.PullNumber)
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

// WaitForPullRequestChecks creates a tool to wait for all status checks to complete on a pull request.
func WaitForPullRequestChecks(mcpServer *server.MCPServer, getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
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
			// Get a logger from the context if available
			logger := logrus.StandardLogger()
			logger.Info("waitForPullRequestChecks()")

			// Get the GitHub client
			client, err := getClient(ctx)
			if err != nil {
				logger.WithError(err).Error("Failed to get GitHub client")
				return mcp.NewToolResultError(fmt.Sprintf("Failed to get GitHub client: %v", err)), nil
			}

			// Parse common parameters and set up context
			eventCtx, result, cancel, err := parsePullRequestEventParams(ctx, mcpServer, client, request)
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
			}).Info("Starting to wait for pull request checks")

			// Run the polling loop with a check function for pull request checks
			return pollForPullRequestEvent(eventCtx, func() (*mcp.CallToolResult, error) {
				// Log the polling iteration
				logger.WithFields(logrus.Fields{
					"owner":      eventCtx.Owner,
					"repo":       eventCtx.Repo,
					"pullNumber": eventCtx.PullNumber,
					"elapsed":    time.Since(eventCtx.StartTime).Round(time.Second),
				}).Debug("Polling for pull request checks")

				// Get the GitHub client for this request
				client, err := getClient(eventCtx.Ctx)
				if err != nil {
					logger.WithError(err).Error("Failed to get GitHub client")
					return nil, fmt.Errorf("failed to get GitHub client: %w", err)
				}

				// First get the pull request to find the head SHA
				logger.Debug("Getting pull request to find head SHA")
				pr, resp, err := client.PullRequests.Get(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, eventCtx.PullNumber)
				if err != nil {
					logger.WithError(err).Error("Failed to get pull request")
					return nil, fmt.Errorf("failed to get pull request: %w", err)
				}

				// Handle the response
				if err := handleResponse(resp, "failed to get pull request"); err != nil {
					logger.WithError(err).Error("Failed to handle pull request response")
					return mcp.NewToolResultError(err.Error()), nil
				}

				if pr.Head == nil || pr.Head.SHA == nil {
					logger.Error("Pull request head SHA is missing")
					return mcp.NewToolResultError("Pull request head SHA is missing"), nil
				}

				logger.WithField("headSHA", *pr.Head.SHA).Debug("Retrieved pull request head SHA")

				// Get check runs for the head SHA
				logger.Debug("Getting check runs for head SHA")
				checkRuns, resp, err := client.Checks.ListCheckRunsForRef(eventCtx.Ctx, eventCtx.Owner, eventCtx.Repo, *pr.Head.SHA, nil)
				if err != nil {
					logger.WithError(err).Error("Failed to get check runs")
					return nil, fmt.Errorf("failed to get check runs: %w", err)
				}

				// Handle the response
				if err := handleResponse(resp, "failed to get check runs"); err != nil {
					logger.WithError(err).Error("Failed to handle check runs response")
					return mcp.NewToolResultError(err.Error()), nil
				}

				// Create our enriched result structure
				result := CheckRunsResult{
					Total:         int(checkRuns.GetTotal()),
					AllComplete:   true,
					AllSuccessful: true,
					CheckRuns:     make([]CheckRunDetails, 0, len(checkRuns.CheckRuns)),
					FailedChecks:  make([]CheckRunDetails, 0),
					PendingChecks: make([]CheckRunDetails, 0),
				}

				// Check if there are any check runs
				logger.WithField("totalCheckRuns", checkRuns.GetTotal()).Debug("Retrieved check runs")
				if checkRuns.GetTotal() == 0 {
					// If there are no check runs, we should consider the checks complete
					// Otherwise, we'd poll indefinitely for repositories without checks
					logger.Info("No check runs found, considering checks complete")
					r, err := json.Marshal(result)
					if err != nil {
						logger.WithError(err).Error("Failed to marshal response")
						return nil, fmt.Errorf("failed to marshal response: %w", err)
					}
					return mcp.NewToolResultText(string(r)), nil
				}

				// Process each check run to extract detailed information
				logger.Debug("Processing check runs to extract detailed information")
				completedCount := 0
				pendingCount := 0
				for _, checkRun := range checkRuns.CheckRuns {
					logger.WithFields(logrus.Fields{
						"checkName": checkRun.GetName(),
						"status":    checkRun.GetStatus(),
					}).Debug("Processing check run")

					// Create detailed information for this check run
					details := CheckRunDetails{
						ID:         checkRun.GetID(),
						Name:       checkRun.GetName(),
						Status:     checkRun.GetStatus(),
						HTMLURL:    checkRun.GetHTMLURL(),
						DetailsURL: checkRun.GetDetailsURL(),
					}

					// Add output information if available
					if checkRun.Output != nil {
						details.Title = checkRun.Output.GetTitle()
						details.Summary = checkRun.Output.GetSummary()
						details.Text = checkRun.Output.GetText()
					}

					// Check if this run is completed
					if checkRun.GetStatus() == "completed" {
						completedCount++
						details.Conclusion = checkRun.GetConclusion()

						// Check if this run failed
						conclusion := checkRun.GetConclusion()
						if conclusion != "success" && conclusion != "neutral" && conclusion != "skipped" {
							result.AllSuccessful = false
							result.FailedChecks = append(result.FailedChecks, details)
						}
					} else {
						// This check is not complete
						pendingCount++
						result.AllComplete = false
						result.PendingChecks = append(result.PendingChecks, details)
					}

					// Add to the overall list of check runs
					result.CheckRuns = append(result.CheckRuns, details)
				}

				logger.WithFields(logrus.Fields{
					"totalChecks":    checkRuns.GetTotal(),
					"completedCount": completedCount,
					"pendingCount":   pendingCount,
					"allComplete":    result.AllComplete,
					"allSuccessful":  result.AllSuccessful,
					"failedChecks":   len(result.FailedChecks),
				}).Debug("Check runs status summary")

				// If all checks are complete, return the result
				if result.AllComplete {
					// All checks are complete, return the check runs
					logger.Info("All checks are complete, returning results")
					r, err := json.Marshal(result)
					if err != nil {
						logger.WithError(err).Error("Failed to marshal response")
						return nil, fmt.Errorf("failed to marshal response: %w", err)
					}
					return mcp.NewToolResultText(string(r)), nil
				}

				// Return nil to continue polling
				logger.Debug("Checks are not complete yet, continuing to poll")
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
