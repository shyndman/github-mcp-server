package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v69/github"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func GetCommit(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_commit",
			mcp.WithDescription(t("TOOL_GET_COMMITS_DESCRIPTION", "Get details for a commit from a GitHub repository")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("Repository owner"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithString("sha",
				mcp.Required(),
				mcp.Description("Commit SHA, branch name, or tag name"),
			),
			WithPagination(),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := requiredParam[string](request, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			repo, err := requiredParam[string](request, "repo")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			sha, err := requiredParam[string](request, "sha")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			pagination, err := OptionalPaginationParams(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			opts := &github.ListOptions{
				Page:    pagination.page,
				PerPage: pagination.perPage,
			}

			client, err := getClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get GitHub client: %w", err)
			}
			commit, resp, err := client.Repositories.GetCommit(ctx, owner, repo, sha, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to get commit: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != 200 {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get commit: %s", string(body))), nil
			}

			r, err := json.Marshal(commit)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// ListCommits creates a tool to get commits of a branch in a repository.
func ListCommits(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_commits",
			mcp.WithDescription(t("TOOL_LIST_COMMITS_DESCRIPTION", "Get list of commits of a branch in a GitHub repository")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("Repository owner"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithString("sha",
				mcp.Description("Branch name"),
			),
			WithPagination(),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := requiredParam[string](request, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			repo, err := requiredParam[string](request, "repo")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			sha, err := OptionalParam[string](request, "sha")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			pagination, err := OptionalPaginationParams(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			opts := &github.CommitsListOptions{
				SHA: sha,
				ListOptions: github.ListOptions{
					Page:    pagination.page,
					PerPage: pagination.perPage,
				},
			}

			client, err := getClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get GitHub client: %w", err)
			}
			commits, resp, err := client.Repositories.ListCommits(ctx, owner, repo, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to list commits: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != 200 {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to list commits: %s", string(body))), nil
			}

			r, err := json.Marshal(commits)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// CreateOrUpdateFile creates a tool to create or update a file in a GitHub repository.
func CreateOrUpdateFile(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("create_or_update_file",
			mcp.WithDescription(t("TOOL_CREATE_OR_UPDATE_FILE_DESCRIPTION", "Create or update a single file in a GitHub repository")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("Repository owner (username or organization)"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithString("path",
				mcp.Required(),
				mcp.Description("Path where to create/update the file"),
			),
			mcp.WithString("content",
				mcp.Required(),
				mcp.Description("Content of the file"),
			),
			mcp.WithString("message",
				mcp.Required(),
				mcp.Description("Commit message"),
			),
			mcp.WithString("branch",
				mcp.Required(),
				mcp.Description("Branch to create/update the file in"),
			),
			mcp.WithString("sha",
				mcp.Description("SHA of file being replaced (for updates)"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := requiredParam[string](request, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			repo, err := requiredParam[string](request, "repo")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			path, err := requiredParam[string](request, "path")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			content, err := requiredParam[string](request, "content")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			message, err := requiredParam[string](request, "message")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			branch, err := requiredParam[string](request, "branch")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Convert content to base64
			contentBytes := []byte(content)

			// Create the file options
			opts := &github.RepositoryContentFileOptions{
				Message: github.Ptr(message),
				Content: contentBytes,
				Branch:  github.Ptr(branch),
			}

			// If SHA is provided, set it (for updates)
			sha, err := OptionalParam[string](request, "sha")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if sha != "" {
				opts.SHA = github.Ptr(sha)
			}

			// Create or update the file
			client, err := getClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get GitHub client: %w", err)
			}
			fileContent, resp, err := client.Repositories.CreateFile(ctx, owner, repo, path, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to create/update file: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != 200 && resp.StatusCode != 201 {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to create/update file: %s", string(body))), nil
			}

			r, err := json.Marshal(fileContent)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// CreateRepository creates a tool to create a new GitHub repository.
func CreateRepository(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("create_repository",
			mcp.WithDescription(t("TOOL_CREATE_REPOSITORY_DESCRIPTION", "Create a new GitHub repository in your account")),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithString("description",
				mcp.Description("Repository description"),
			),
			mcp.WithBoolean("private",
				mcp.Description("Whether repo should be private"),
			),
			mcp.WithBoolean("autoInit",
				mcp.Description("Initialize with README"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, err := requiredParam[string](request, "name")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			description, err := OptionalParam[string](request, "description")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			private, err := OptionalParam[bool](request, "private")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			autoInit, err := OptionalParam[bool](request, "autoInit")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			repo := &github.Repository{
				Name:        github.Ptr(name),
				Description: github.Ptr(description),
				Private:     github.Ptr(private),
				AutoInit:    github.Ptr(autoInit),
			}

			client, err := getClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get GitHub client: %w", err)
			}
			createdRepo, resp, err := client.Repositories.Create(ctx, "", repo)
			if err != nil {
				return nil, fmt.Errorf("failed to create repository: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusCreated {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to create repository: %s", string(body))), nil
			}

			r, err := json.Marshal(createdRepo)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// GetFileContents creates a tool to get the contents of a file or directory from a GitHub repository.
func GetFileContents(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_file_contents",
			mcp.WithDescription(t("TOOL_GET_FILE_CONTENTS_DESCRIPTION", "Get the contents of a file or directory from a GitHub repository")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("Repository owner (username or organization)"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithString("path",
				mcp.Required(),
				mcp.Description("Path to file/directory"),
			),
			mcp.WithString("branch",
				mcp.Description("Branch to get contents from"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := requiredParam[string](request, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			repo, err := requiredParam[string](request, "repo")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			path, err := requiredParam[string](request, "path")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			branch, err := OptionalParam[string](request, "branch")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get GitHub client: %w", err)
			}
			opts := &github.RepositoryContentGetOptions{Ref: branch}
			fileContent, dirContent, resp, err := client.Repositories.GetContents(ctx, owner, repo, path, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to get file contents: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != 200 {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get file contents: %s", string(body))), nil
			}

			var result interface{}
			if fileContent != nil {
				result = fileContent
			} else {
				result = dirContent
			}

			r, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// ForkRepository creates a tool to fork a repository.
func ForkRepository(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("fork_repository",
			mcp.WithDescription(t("TOOL_FORK_REPOSITORY_DESCRIPTION", "Fork a GitHub repository to your account or specified organization")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("Repository owner"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithString("organization",
				mcp.Description("Organization to fork to"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := requiredParam[string](request, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			repo, err := requiredParam[string](request, "repo")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			org, err := OptionalParam[string](request, "organization")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			opts := &github.RepositoryCreateForkOptions{}
			if org != "" {
				opts.Organization = org
			}

			client, err := getClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get GitHub client: %w", err)
			}
			forkedRepo, resp, err := client.Repositories.CreateFork(ctx, owner, repo, opts)
			if err != nil {
				// Check if it's an acceptedError. An acceptedError indicates that the update is in progress,
				// and it's not a real error.
				if resp != nil && resp.StatusCode == http.StatusAccepted && isAcceptedError(err) {
					return mcp.NewToolResultText("Fork is in progress"), nil
				}
				return nil, fmt.Errorf("failed to fork repository: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusAccepted {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to fork repository: %s", string(body))), nil
			}

			r, err := json.Marshal(forkedRepo)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// CreateBranch creates a tool to create a new branch.
func CreateBranch(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("create_branch",
			mcp.WithDescription(t("TOOL_CREATE_BRANCH_DESCRIPTION", "Create a new branch in a GitHub repository")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("Repository owner"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithString("branch",
				mcp.Required(),
				mcp.Description("Name for new branch"),
			),
			mcp.WithString("from_branch",
				mcp.Description("Source branch (defaults to repo default)"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := requiredParam[string](request, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			repo, err := requiredParam[string](request, "repo")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			branch, err := requiredParam[string](request, "branch")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			fromBranch, err := OptionalParam[string](request, "from_branch")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get GitHub client: %w", err)
			}

			// Get the source branch SHA
			var ref *github.Reference

			if fromBranch == "" {
				// Get default branch if from_branch not specified
				repository, resp, err := client.Repositories.Get(ctx, owner, repo)
				if err != nil {
					return nil, fmt.Errorf("failed to get repository: %w", err)
				}
				defer func() { _ = resp.Body.Close() }()

				fromBranch = *repository.DefaultBranch
			}

			// Get SHA of source branch
			ref, resp, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+fromBranch)
			if err != nil {
				return nil, fmt.Errorf("failed to get reference: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			// Create new branch
			newRef := &github.Reference{
				Ref:    github.Ptr("refs/heads/" + branch),
				Object: &github.GitObject{SHA: ref.Object.SHA},
			}

			createdRef, resp, err := client.Git.CreateRef(ctx, owner, repo, newRef)
			if err != nil {
				return nil, fmt.Errorf("failed to create branch: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			r, err := json.Marshal(createdRef)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// PushFiles creates a tool to push multiple files in a single commit to a GitHub repository.
func PushFiles(getClient GetClientFn, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("push_files",
			mcp.WithDescription(t("TOOL_PUSH_FILES_DESCRIPTION", "Push multiple files to a GitHub repository in a single commit")),
			mcp.WithString("owner",
				mcp.Required(),
				mcp.Description("Repository owner"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithString("branch",
				mcp.Required(),
				mcp.Description("Branch to push to"),
			),
			mcp.WithArray("files",
				mcp.Required(),
				mcp.Items(
					map[string]interface{}{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"path", "content"},
						"properties": map[string]interface{}{
							"path": map[string]interface{}{
								"type":        "string",
								"description": "path to the file",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "file content",
							},
						},
					}),
				mcp.Description("Array of file objects to push, each object with path (string) and content (string)"),
			),
			mcp.WithString("message",
				mcp.Required(),
				mcp.Description("Commit message"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			owner, err := requiredParam[string](request, "owner")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			repo, err := requiredParam[string](request, "repo")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			branch, err := requiredParam[string](request, "branch")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			message, err := requiredParam[string](request, "message")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Parse files parameter - this should be an array of objects with path and content
			filesObj, ok := request.Params.Arguments["files"].([]interface{})
			if !ok {
				return mcp.NewToolResultError("files parameter must be an array of objects with path and content"), nil
			}

			client, err := getClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get GitHub client: %w", err)
			}

			// Get the reference for the branch
			ref, resp, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
			if err != nil {
				return nil, fmt.Errorf("failed to get branch reference: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			// Get the commit object that the branch points to
			baseCommit, resp, err := client.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
			if err != nil {
				return nil, fmt.Errorf("failed to get base commit: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			// Create tree entries for all files
			var entries []*github.TreeEntry

			for _, file := range filesObj {
				fileMap, ok := file.(map[string]interface{})
				if !ok {
					return mcp.NewToolResultError("each file must be an object with path and content"), nil
				}

				path, ok := fileMap["path"].(string)
				if !ok || path == "" {
					return mcp.NewToolResultError("each file must have a path"), nil
				}

				content, ok := fileMap["content"].(string)
				if !ok {
					return mcp.NewToolResultError("each file must have content"), nil
				}

				// Create a tree entry for the file
				entries = append(entries, &github.TreeEntry{
					Path:    github.Ptr(path),
					Mode:    github.Ptr("100644"), // Regular file mode
					Type:    github.Ptr("blob"),
					Content: github.Ptr(content),
				})
			}

			// Create a new tree with the file entries
			newTree, resp, err := client.Git.CreateTree(ctx, owner, repo, *baseCommit.Tree.SHA, entries)
			if err != nil {
				return nil, fmt.Errorf("failed to create tree: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			// Create a new commit
			commit := &github.Commit{
				Message: github.Ptr(message),
				Tree:    newTree,
				Parents: []*github.Commit{{SHA: baseCommit.SHA}},
			}
			newCommit, resp, err := client.Git.CreateCommit(ctx, owner, repo, commit, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create commit: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			// Update the reference to point to the new commit
			ref.Object.SHA = newCommit.SHA
			updatedRef, resp, err := client.Git.UpdateRef(ctx, owner, repo, ref, false)
			if err != nil {
				return nil, fmt.Errorf("failed to update reference: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			r, err := json.Marshal(updatedRef)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}
