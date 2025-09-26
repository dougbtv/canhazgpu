package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"k8s.io/klog/v2"
)

// applyVLLMDiffs applies local diffs from a ConfigMap to a git repository
func applyVLLMDiffs(repoDir, configMapPath string) error {
	if configMapPath == "" {
		klog.V(4).Infof("No diff ConfigMap specified for %s", repoDir)
		return nil
	}

	// Check if ConfigMap directory exists
	if _, err := os.Stat(configMapPath); os.IsNotExist(err) {
		klog.Warningf("Diff ConfigMap not found at %s", configMapPath)
		return nil
	}

	klog.Infof("Applying vLLM diffs from %s to %s", configMapPath, repoDir)

	// Read diff data
	diffPatchPath := filepath.Join(configMapPath, "diff.patch")
	modifiedFilesPath := filepath.Join(configMapPath, "modified_files")
	untrackedFilesPath := filepath.Join(configMapPath, "untracked_files")

	// Apply git patch for modified files
	if _, err := os.Stat(diffPatchPath); err == nil {
		patchData, err := os.ReadFile(diffPatchPath)
		if err != nil {
			return fmt.Errorf("failed to read diff patch: %w", err)
		}

		if len(patchData) > 0 && strings.TrimSpace(string(patchData)) != "" {
			klog.Infof("Applying git patch to %s", repoDir)
			if err := applyGitPatch(repoDir, string(patchData)); err != nil {
				klog.Errorf("Failed to apply git patch: %v", err)
				// Don't fail completely - continue with untracked files
			} else {
				klog.Infof("Successfully applied git patch")
			}
		}
	}

	// Handle untracked files
	if _, err := os.Stat(untrackedFilesPath); err == nil {
		untrackedData, err := os.ReadFile(untrackedFilesPath)
		if err != nil {
			return fmt.Errorf("failed to read untracked files list: %w", err)
		}

		if len(untrackedData) > 0 {
			untrackedFiles := strings.Split(strings.TrimSpace(string(untrackedData)), "\n")
			if err := restoreUntrackedFiles(repoDir, configMapPath, untrackedFiles); err != nil {
				klog.Errorf("Failed to restore untracked files: %v", err)
				// Don't fail completely
			} else {
				klog.Infof("Successfully restored %d untracked files", len(untrackedFiles))
			}
		}
	}

	klog.Infof("vLLM diff application completed for %s", repoDir)
	return nil
}

// applyGitPatch applies a git patch to a repository
func applyGitPatch(repoDir, patchData string) error {
	if strings.TrimSpace(patchData) == "" {
		return nil
	}

	// Create a temporary patch file
	tmpFile, err := os.CreateTemp("", "vllm-diff-*.patch")
	if err != nil {
		return fmt.Errorf("failed to create temp patch file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write patch data
	if _, err := tmpFile.WriteString(patchData); err != nil {
		return fmt.Errorf("failed to write patch data: %w", err)
	}
	tmpFile.Close()

	// Apply the patch using git apply
	cmd := exec.Command("git", "apply", "--ignore-whitespace", tmpFile.Name())
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Try with --3way merge if regular apply fails
		klog.V(4).Infof("Regular git apply failed, trying 3-way merge: %v", err)
		cmd = exec.Command("git", "apply", "--3way", "--ignore-whitespace", tmpFile.Name())
		cmd.Dir = repoDir
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git apply failed: %v, output: %s", err, string(output))
		}
	}

	klog.V(4).Infof("Git apply output: %s", string(output))
	return nil
}

// restoreUntrackedFiles restores untracked files from the diff data
func restoreUntrackedFiles(repoDir, configMapPath string, untrackedFiles []string) error {
	// Read the full diff data to extract untracked file contents
	diffPatchPath := filepath.Join(configMapPath, "diff.patch")
	diffData, err := os.ReadFile(diffPatchPath)
	if err != nil {
		return fmt.Errorf("failed to read diff data: %w", err)
	}

	diffContent := string(diffData)

	// Parse and restore each untracked file
	for _, filename := range untrackedFiles {
		if strings.TrimSpace(filename) == "" {
			continue
		}

		klog.V(4).Infof("Restoring untracked file: %s", filename)

		// Look for the file content in the diff data
		fileMarker := fmt.Sprintf("# New file: %s\n", filename)
		startIdx := strings.Index(diffContent, fileMarker)
		if startIdx == -1 {
			klog.Warningf("Content for untracked file %s not found in diff data", filename)
			continue
		}

		// Find the start of the content (after the marker)
		contentStart := startIdx + len(fileMarker)

		// Find the end of the content (next file marker or end of string)
		nextMarker := strings.Index(diffContent[contentStart:], "\n# ")
		var content string
		if nextMarker == -1 {
			content = diffContent[contentStart:]
		} else {
			content = diffContent[contentStart : contentStart+nextMarker]
		}

		// Clean up content (remove trailing newlines)
		content = strings.TrimRight(content, "\n")

		// Create the file path
		filePath := filepath.Join(repoDir, filename)

		// Create directory if needed
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			klog.Warningf("Failed to create directory for %s: %v", filename, err)
			continue
		}

		// Write the file
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			klog.Warningf("Failed to write untracked file %s: %v", filename, err)
			continue
		}

		klog.V(4).Infof("Successfully restored untracked file: %s", filename)
	}

	return nil
}

// isVLLMDiffApplied checks if diffs have already been applied to avoid reapplication
func isVLLMDiffApplied(repoDir string) bool {
	// Check for a marker file that indicates diffs have been applied
	markerFile := filepath.Join(repoDir, ".k8shazgpu-diffs-applied")
	_, err := os.Stat(markerFile)
	return err == nil
}

// markVLLMDiffApplied creates a marker file to indicate diffs have been applied
func markVLLMDiffApplied(repoDir string) error {
	markerFile := filepath.Join(repoDir, ".k8shazgpu-diffs-applied")
	return os.WriteFile(markerFile, []byte("diffs applied\n"), 0644)
}