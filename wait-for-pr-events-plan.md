# Plan: Add PR Event Waiting Functions

## Overview
We need to add two new functions that will pause the AI's execution until certain events happen on a Pull Request:
1. All status checks have completed (either passed or failed)
2. A review has been added or updated

Both functions will work by checking GitHub repeatedly until the event happens or we timeout.

## How GitHub MCP Progress Tokens Work
Before diving in, it's important to understand how our server handles long-running operations:

1. When a tool needs to wait for something:
   - First call: Returns a special "progress token" (like a bookmark)
   - Next calls: Include that token to continue where we left off
   - Final call: Returns actual results when done

2. The AI assistant automatically handles these tokens - we just need to return them correctly

## Implementation Plan: Wait for PR Checks

### 1. Create Basic Function Structure
Add new function in `pkg/github/pullrequests.go`:
```go
func waitForPRChecks(client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
    // We'll fill this in next
}
```

### 2. Define the Tool Parameters
We need:
- `owner`: Repository owner (required)
- `repo`: Repository name (required)
- `pullNumber`: PR number (required)
- `timeout_seconds`: How long to wait before giving up (optional, default 600)

### 3. Create Handler Function Steps
The handler needs to:

a. Get the parameters from the request:
```go
owner, _ := requiredParam[string](request, "owner")
repo, _ := requiredParam[string](request, "repo") 
pullNumber, _ := requiredInt(request, "pullNumber")
```

b. Handle the timeout parameter:
```go
timeoutSecs := 600  // Default 10 minutes
if val, exists := request.Params.Arguments["timeout_seconds"] {
    timeoutSecs = int(val.(float64))
}
```

c. Track how long we've been waiting:
- If first call: Save current time in progress token
- If continuing: Get saved time from progress token
- Check if we've exceeded timeout

d. Check current PR status:
- Use `client.Repositories.GetCombinedStatus()` 
- Look at the `state` field of the response

e. Decide what to do:
- If checks still running: Return progress token to continue waiting
- If checks complete (success/failure): Return final status
- If timeout reached: Return error
- If API error: Return error

### 4. Register the Tool
Add to list of tools in `pkg/github/github.go`:
```go
func NewGitHubTools(client *github.Client, t translations.TranslationHelperFunc) []server.Tool {
    return []server.Tool{
        // ... existing tools ...
        waitForPRChecks(client, t),
    }
}
```

## Implementation Plan: Wait for PR Review

### 1. Create Basic Function Structure
Similar to above, but named `waitForPRReview`

### 2. Define Tool Parameters
Same as above, plus:
- `last_review_id`: ID of most recent review (optional)
  - If provided: Wait for newer reviews
  - If not provided: Wait for any review

### 3. Create Handler Function Steps
Similar flow to above, but:

a. Get current reviews using:
```go
reviews, _, err := client.PullRequests.ListReviews(ctx, owner, repo, pullNumber, nil)
```

b. Compare with last_review_id:
- If new reviews found: Return latest review data
- If no new reviews: Return progress token to keep waiting
- If timeout reached: Return error

### 4. Register the Tool
Add to tools list like above

## Testing Plan
Test these scenarios for each function:

1. Happy path:
   - Call function
   - Event happens before timeout
   - Get correct result

2. Timeout path:
   - Call function
   - Event doesn't happen
   - Get timeout error

3. Error handling:
   - Invalid PR number
   - No permissions
   - API errors

## Usage Examples
Once implemented, tools can be used like:

```go
// Wait for checks
result, err := tools.CallTool(ctx, "wait_for_pr_checks", map[string]interface{}{
    "owner": "octocat",
    "repo": "Hello-World",
    "pullNumber": 123,
    "timeout_seconds": 300  // 5 minutes
})

// Wait for review
result, err := tools.CallTool(ctx, "wait_for_pr_review", map[string]interface{}{
    "owner": "octocat",
    "repo": "Hello-World",
    "pullNumber": 123,
    "last_review_id": 456789  // Optional
})
```

## Implementation Notes
- Use exponential backoff for polling (start with short intervals, gradually increase)
- Include helpful error messages
- Document all parameters
- Add debug logging
- Consider adding optional parameters for specific check names or review states