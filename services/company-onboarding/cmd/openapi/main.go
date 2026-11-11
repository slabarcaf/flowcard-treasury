package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/apidocs"
)

func main() {
	checkFlag := flag.Bool("check", false, "Verify generated specification files are up to date without modifying disk")
	repoRootFlag := flag.String("repo-root", "", "Path to repository root directory (auto-detected if empty)")
	moduleRootFlag := flag.String("module-root", "", "Path to company-onboarding Go module root (auto-detected if empty)")
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to determine working directory: %v", err)
	}

	repoRoot := *repoRootFlag
	if repoRoot == "" {
		repoRoot = detectRepoRoot(cwd)
		if repoRoot == "" {
			log.Fatalf("could not auto-detect repository root from %s; use -repo-root", cwd)
		}
	}

	moduleRoot := *moduleRootFlag
	if moduleRoot == "" {
		moduleRoot = detectModuleRoot(cwd)
		if moduleRoot == "" {
			log.Fatalf("could not auto-detect module root from %s; use -module-root", cwd)
		}
	}

	if err := apidocs.WriteSpecs(repoRoot, moduleRoot, *checkFlag); err != nil {
		if *checkFlag {
			fmt.Fprintf(os.Stderr, "OpenAPI drift check failed:\n  %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "OpenAPI generation failed:\n  %v\n", err)
		}
		os.Exit(1)
	}

	if *checkFlag {
		fmt.Println("ok: all generated OpenAPI 3.1 specifications are up to date")
	} else {
		fmt.Printf("ok: generated OpenAPI 3.1 specifications in %s and %s\n",
			filepath.Join(repoRoot, "docs", "api"),
			filepath.Join(moduleRoot, "internal", "apidocs", "specs"),
		)
	}
}

// detectModuleRoot traverses upward looking for go.mod with module company-onboarding.
func detectModuleRoot(startDir string) string {
	curr := startDir
	for {
		goModPath := filepath.Join(curr, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			content, rErr := os.ReadFile(goModPath)
			if rErr == nil && (stringsContains(string(content), "module github.com/slabarcaf/flowcard-treasury/services/company-onboarding") ||
				stringsContains(string(content), "module company-onboarding")) {
				return curr
			}
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return ""
}

// detectRepoRoot traverses upward looking for .git or docs/plans directory.
func detectRepoRoot(startDir string) string {
	curr := startDir
	for {
		gitPath := filepath.Join(curr, ".git")
		docsPlansPath := filepath.Join(curr, "docs", "plans")
		if _, err := os.Stat(gitPath); err == nil {
			return curr
		}
		if _, err := os.Stat(docsPlansPath); err == nil {
			return curr
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return ""
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || filepath.Base(s) == substr || (len(s) > 0 && len(substr) > 0 && hasSubstr(s, substr)))
}

func hasSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
