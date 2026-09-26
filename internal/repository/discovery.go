package repository

import (
	"context"
	"path/filepath"
	"strings"
)

// RepositoryInfo holds detected metadata about a repository during discovery.
type RepositoryInfo struct {
	Languages       []string `json:"languages"`
	Frameworks      []string `json:"frameworks"`
	PackageManagers []string `json:"packageManagers"`
	BuildSystems    []string `json:"buildSystems"`
	TestFrameworks  []string `json:"testFrameworks"`
	EntryPoints     []string `json:"entryPoints"`
	ConfigFiles     []string `json:"configFiles"`
	HasDocker       bool     `json:"hasDocker"`
	HasCICD         bool     `json:"hasCICD"`
	HasMigrations   bool     `json:"hasMigrations"`
	HasAPIDefs      bool     `json:"hasAPIDefs"`
	HasDocs         bool     `json:"hasDocs"`
}

// Discoverer detects repository characteristics, stack, dependencies, and configuration.
type Discoverer interface {
	Discover(ctx context.Context, repo Repository) (*RepositoryInfo, error)
}

// LocalDiscoverer analyzes a repository using its FileSystem.
type LocalDiscoverer struct{}

// NewLocalDiscoverer creates a new LocalDiscoverer instance.
func NewLocalDiscoverer() *LocalDiscoverer {
	return &LocalDiscoverer{}
}

// Discover analyzes the repository files to populate RepositoryInfo.
func (d *LocalDiscoverer) Discover(ctx context.Context, repo Repository) (*RepositoryInfo, error) {
	fs := repo.FileSystem()
	info := &RepositoryInfo{}

	langMap := make(map[string]bool)
	fwMap := make(map[string]bool)
	pmMap := make(map[string]bool)
	bsMap := make(map[string]bool)
	tfMap := make(map[string]bool)

	var walk func(path string) error
	walk = func(path string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		files, err := fs.ReadDir(ctx, path)
		if err != nil {
			return nil
		}

		for _, f := range files {
			name := f.Name
			fullPath := f.Path
			if fullPath == "" {
				fullPath = filepath.Join(path, name)
			}

			if f.IsDir {
				// Don't skip hidden dir if it might contain CI/CD workflows like .github
				if strings.HasPrefix(name, ".") && name != ".github" && name != ".gitlab" {
					continue
				}
				if name == "node_modules" || name == "vendor" || name == "dist" || name == "build" {
					continue
				}
				if name == "migrations" || name == "db/migrations" {
					info.HasMigrations = true
				}
				if name == "docs" || name == "doc" {
					info.HasDocs = true
				}
				if err := walk(fullPath); err != nil {
					return err
				}
				continue
			}

			lowerName := strings.ToLower(name)
			ext := strings.ToLower(filepath.Ext(name))

			// Language & Framework & Build System detection
			switch ext {
			case ".go":
				langMap["Go"] = true
				if lowerName == "main.go" {
					info.EntryPoints = append(info.EntryPoints, fullPath)
				}
				if strings.HasSuffix(lowerName, "_test.go") {
					tfMap["testing"] = true
				}
			case ".ts", ".tsx":
				langMap["TypeScript"] = true
				if lowerName == "index.ts" || lowerName == "main.ts" || lowerName == "app.ts" || lowerName == "server.ts" {
					info.EntryPoints = append(info.EntryPoints, fullPath)
				}
			case ".js", ".jsx", ".mjs", ".cjs":
				langMap["JavaScript"] = true
				if lowerName == "index.js" || lowerName == "main.js" || lowerName == "app.js" || lowerName == "server.js" {
					info.EntryPoints = append(info.EntryPoints, fullPath)
				}
			case ".py":
				langMap["Python"] = true
				if lowerName == "main.py" || lowerName == "app.py" || lowerName == "manage.py" {
					info.EntryPoints = append(info.EntryPoints, fullPath)
				}
			case ".rs":
				langMap["Rust"] = true
				if lowerName == "main.rs" || lowerName == "lib.rs" {
					info.EntryPoints = append(info.EntryPoints, fullPath)
				}
			case ".java":
				langMap["Java"] = true
			case ".rb":
				langMap["Ruby"] = true
			case ".c", ".cpp", ".cc", ".h", ".hpp":
				langMap["C/C++"] = true
			}

			// Configuration and Build files
			switch lowerName {
			case "go.mod":
				langMap["Go"] = true
				pmMap["go modules"] = true
				bsMap["go build"] = true
				info.ConfigFiles = append(info.ConfigFiles, fullPath)
				// Inspect go.mod content for frameworks
				if data, err := fs.ReadFile(ctx, fullPath); err == nil {
					content := string(data)
					if strings.Contains(content, "github.com/gin-gonic/gin") {
						fwMap["Gin"] = true
					}
					if strings.Contains(content, "github.com/labstack/echo") {
						fwMap["Echo"] = true
					}
					if strings.Contains(content, "github.com/gofiber/fiber") {
						fwMap["Fiber"] = true
					}
				}
			case "package.json":
				pmMap["npm"] = true
				info.ConfigFiles = append(info.ConfigFiles, fullPath)
				if data, err := fs.ReadFile(ctx, fullPath); err == nil {
					content := string(data)
					if strings.Contains(content, "\"react\"") {
						fwMap["React"] = true
					}
					if strings.Contains(content, "\"next\"") {
						fwMap["Next.js"] = true
					}
					if strings.Contains(content, "\"express\"") {
						fwMap["Express"] = true
					}
					if strings.Contains(content, "\"jest\"") {
						tfMap["Jest"] = true
					}
					if strings.Contains(content, "\"vitest\"") {
						tfMap["Vitest"] = true
					}
				}
			case "yarn.lock":
				pmMap["yarn"] = true
			case "pnpm-lock.yaml":
				pmMap["pnpm"] = true
			case "cargo.toml":
				langMap["Rust"] = true
				pmMap["cargo"] = true
				bsMap["cargo"] = true
				info.ConfigFiles = append(info.ConfigFiles, fullPath)
			case "requirements.txt", "pipfile", "pyproject.toml":
				langMap["Python"] = true
				pmMap["pip"] = true
				info.ConfigFiles = append(info.ConfigFiles, fullPath)
			case "makefile":
				bsMap["make"] = true
			case "dockerfile", "docker-compose.yml", "docker-compose.yaml":
				info.HasDocker = true
			case "openapi.yaml", "openapi.json", "swagger.yaml", "swagger.json":
				info.HasAPIDefs = true
			case "readme.md":
				info.HasDocs = true
			}

			normalizedPath := filepath.ToSlash(fullPath)
			if strings.Contains(normalizedPath, ".github/workflows") || strings.Contains(normalizedPath, ".gitlab-ci.yml") {
				info.HasCICD = true
			}
		}
		return nil
	}

	if err := walk("."); err != nil {
		return nil, err
	}

	for l := range langMap {
		info.Languages = append(info.Languages, l)
	}
	for f := range fwMap {
		info.Frameworks = append(info.Frameworks, f)
	}
	for pm := range pmMap {
		info.PackageManagers = append(info.PackageManagers, pm)
	}
	for bs := range bsMap {
		info.BuildSystems = append(info.BuildSystems, bs)
	}
	for tf := range tfMap {
		info.TestFrameworks = append(info.TestFrameworks, tf)
	}

	return info, nil
}
